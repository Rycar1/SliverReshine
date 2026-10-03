package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"c2tool/internal/ui/sliver"
)

// Server holds the HTTP API handlers and the current Sliver connection.
type Server struct {
	mu     sync.RWMutex
	client *sliver.Client
	// auth guards the console when configured; nil disables authentication.
	auth *BasicAuth
	// avLookupURL overrides the process-identification endpoint. Empty means the
	// built-in default; "off" disables the feature. See SetAVLookupURL.
	avLookupURL string
}

// New creates an API server.
func New() *Server {
	return &Server{}
}

// SetAVLookupURL configures the process-identification endpoint.
//
// The endpoint receives the target's process list, so this is the switch that
// decides whether any such data leaves the operator's network and where it goes.
// An empty value keeps the built-in public default; "off" makes the handlers
// refuse rather than silently falling back, so a deployment that has opted out
// cannot be talked into a disclosure by a request body field.
func (s *Server) SetAVLookupURL(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.avLookupURL = strings.TrimSpace(url)
}

// avLookupEndpoint resolves the per-request override against the deployment
// setting, and reports whether the lookup is permitted at all.
//
// Precedence: the request override wins, so an operator can point one scan at a
// URL they control; then the deployment setting; then the built-in default. A
// deployment that set "off" refuses the override too -- opt-out should not be
// reversible from a request body.
func (s *Server) avLookupEndpoint(requestURL string) (string, bool) {
	s.mu.RLock()
	configured := s.avLookupURL
	s.mu.RUnlock()

	if strings.EqualFold(configured, "off") {
		return "", false
	}
	if u := strings.TrimSpace(requestURL); u != "" {
		return u, true
	}
	if configured != "" {
		return configured, true
	}
	return sliver.DefaultAVLookupURL, true
}

// SetBasicAuth installs credentials for the console. Passing nil disables
// authentication, which is the default so a fresh install on a trusted network
// behaves exactly as before.
func (s *Server) SetBasicAuth(cfg *BasicAuth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auth = cfg
}

// basicAuth returns the installed credentials, or an inert config when auth is
// off. Never nil, so callers can read the current account without a nil check
// on every handler.
func (s *Server) basicAuth() *BasicAuth {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.auth == nil {
		return &BasicAuth{}
	}
	return s.auth
}

func (s *Server) Client() *sliver.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.client
}

func (s *Server) SetClient(c *sliver.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
	}
	s.client = c
}

func (s *Server) ClearClient() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	// charset=utf-8 is not decoration. JSON is UTF-8 by definition (RFC 8259),
	// but a client that is not told so falls back to its own default, and
	// PowerShell 5.1 defaults to ISO-8859-1. Every non-ASCII character then
	// arrives Latin-1-expanded: the console returned 找不到用户名。 and
	// Invoke-RestMethod rendered it as 忙聣戮盲赂聧氓聢掳莽聰篓, which reads exactly
	// like a server-side encoding bug and sends the operator hunting for one.
	// Declaring the charset costs nothing and removes the whole class of report.
	//
	// The body is rendered into memory before the status is written. Encoding
	// straight to w means the failure is discovered after a 200 has already gone
	// out, and a status cannot be taken back -- which is exactly how a successful
	// upload used to reach the operator as a failure, as an empty body the
	// console's client could not parse.
	buf, err := json.Marshal(v)
	if err != nil {
		// The value could not be represented at all. Saying so beats an empty 200
		// that the client reports as a network error.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"response could not be encoded"}`))
		log.Printf("[api] response encoding failed: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if _, err := w.Write(buf); err != nil {
		// The client hung up mid-write. The status is already committed, so all
		// that is left is to note it.
		log.Printf("[api] response write failed: %v", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) requireClient(w http.ResponseWriter) *sliver.Client {
	c := s.Client()
	if c == nil {
		writeErr(w, http.StatusServiceUnavailable, "not connected to sliver-server")
		return nil
	}
	return c
}

// clientFor returns the console client for one request.
//
// It is requireClient plus the request: the client is wrapped in a view bound
// to r.Context() (see sliver.Client.WithRequestContext) so every gRPC call the
// handler makes inherits the request's deadline and is cancelled if the browser
// disconnects. Handlers should use this rather than requireClient so a request
// cannot leave work running on the server after nobody is listening.
func (s *Server) clientFor(w http.ResponseWriter, r *http.Request) *sliver.Client {
	c := s.requireClient(w)
	if c == nil {
		return nil
	}
	return c.WithRequestContext(r.Context())
}

// withClient adapts a handler that needs a live sliver-server connection.
//
// These handlers all opened with the same two lines -- take the request-scoped
// client, return a 503 if there is none -- which is the kind of repeated
// conditional on the same shape the review flagged as a missing dispatcher.
// Registering through this wrapper moves the requirement into the route table:
// a handler that needs a client cannot be registered without one, and the 503
// is written in exactly one place.
//
// The client is the request-scoped view from clientFor, so the deadline and
// cancellation behaviour described there is unchanged. handleTunnelClose and
// the sessionID-based handlers keep their own guard: they must validate the
// request before deciding whether a connection is even relevant.
func (s *Server) withClient(h func(c *sliver.Client, w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c := s.clientFor(w, r)
		if c == nil {
			return
		}
		h(c, w, r)
	}
}

// route is a single HTTP handler registration.
type route struct {
	method  string
	pattern string
	handler http.HandlerFunc
}

// RoutePattern is the machine-readable contract for a registered route. The
// frontend contract test (frontend/src/lib/__tests__/routes.test.ts) compares
// every path the UI calls through api.ts against RoutePatterns(), so a path
// renamed here (or in api.ts) fails CI instead of silently 404ing.
type RoutePattern struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
}

// Routes registers all HTTP handlers.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	for _, r := range s.apiRoutes() {
		mux.HandleFunc(r.method+" "+r.pattern, withRequestCeiling(r.handler))
	}

	mux.HandleFunc("/ws/sessions/{id}/terminal", s.handleTerminalWS)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/ws/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	mux.HandleFunc("/", handleStatic)

	// Auth runs before CORS. If the order were reversed, an unauthenticated
	// OPTIONS preflight would be answered 204 by the CORS layer and never reach
	// the credential check — a scanner could then confirm the service exists and
	// enumerate which methods it accepts without ever authenticating.
	// The body limit is applied in the middleware chain rather than in each
	// handler, so a new endpoint cannot forget it. Every route below the mux reads
	// r.Body, and an unbounded read is a way for one client to exhaust the console.
	return s.wrap(mux)
}

// apiRequestCeiling bounds one non-streaming API request end to end.
//
// The per-call budgets in internal/ui/sliver already bound each gRPC call; this
// is the outer limit on the handler that makes them, so a handler that loops,
// makes many calls, or blocks on something else cannot hold the connection
// indefinitely. It is deliberately above the largest per-call budget (the
// 10-minute process dump) so it never truncates work the call policy allows,
// and it only ever covers /api: the terminal WebSocket and the static bundle
// are registered outside apiRoutes and keep their long-lived connections.
const apiRequestCeiling = 12 * time.Minute

// withRequestCeiling gives a non-streaming handler a request context that is
// bounded and cancelled with the connection.
//
// This is what turns "the browser went away" into "the work stops": handlers
// pass r.Context() into sliver.Client (via clientFor), so cancelling here
// cancels the gRPC call the handler is waiting on rather than leaving it to
// finish on the server.
func withRequestCeiling(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), apiRequestCeiling)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}

// wrap applies the middleware chain to a mux.
//
// Split out of Routes so a test can drive the real chain -- including the auth
// layer -- with a handler that panics. Testing withRecover on its own would pass
// even if Routes forgot to install it, which is exactly the mistake worth
// catching: a guard that is written but not wired looks identical to no guard at
// all from the outside.
//
// Order, outermost first: security headers, auth, body limit, CSRF, CORS,
// logging, recover, mux. withRecover sits inside withLogging so the access log
// records the 500 instead of losing the request, and outside the mux so every
// registered handler is covered. See recover.go for why net/http's own
// per-connection recover is not enough.
func (s *Server) wrap(mux http.Handler) http.Handler {
	return withSecurityHeaders(basicAuth(
		s.auth,
		withBodyLimit(
			withCSRF(
				withCORS(
					withLogging(
						withRecover(mux)))))))
}

// RoutePatterns returns the full HTTP contract (method + path pattern) of the
// registered routes, including the terminal WebSocket endpoint.
func (s *Server) RoutePatterns() []RoutePattern {
	out := make([]RoutePattern, 0, len(s.apiRoutes())+1)
	for _, r := range s.apiRoutes() {
		out = append(out, RoutePattern{Method: r.method, Pattern: r.pattern})
	}
	out = append(out, RoutePattern{Method: "GET", Pattern: "/ws/sessions/{id}/terminal"})
	return out
}

// statusRecorder captures the response status code for the request log. It
// forwards Flush and Hijack so WebSocket upgrades (terminal) keep working.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("response writer does not support hijacking")
}

// withLogging mirrors every API request into the app log (sliver-ui.log on
// Windows). Mutating methods are tagged so destructive operations (generate,
// kill, delete, start/stop jobs, ...) form a lightweight audit trail.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// One word, not two. This used to print a derived op and the method side
		// by side, which rendered as "GET GET /api/info" for every read -- the
		// derived value is already the method in that case. Printing the method
		// once, and MUTATE in its place only for a write, says the same thing
		// without the stutter.
		op := r.Method
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			op = "MUTATE " + r.Method
		}
		log.Printf("[api] %s %s -> %d (%s)", op, r.URL.Path, rec.status, time.Since(start))
	})
}

// withCORS answers preflight and, for same-origin callers, echoes the Origin.
//
// The wildcard this used to send was wrong for an authenticated API. "*" tells
// every origin on the internet that the browser may hand it the response, and
// while a browser withholds credentialed bodies from a wildcard response, the
// header still advertises the console to any page the operator happens to have
// open. A console is a same-origin application: it is served from the same
// address it calls, so it never needs cross-origin access at all, and granting
// none costs nothing.
//
// Vary: Origin is set unconditionally, including on the 204, so a shared cache
// cannot hand one origin's response to another.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Origin")

		if origin := r.Header.Get("Origin"); origin != "" && sameOrigin(origin, r.Host) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// maxRequestBody bounds every request body the console will read.
//
// The largest legitimate body is a base64 tool upload (an implant, an assembly
// or a shellcode blob), which inflates the payload by 4/3; 128 MiB of JSON is
// therefore roughly a 96 MiB binary, comfortably above anything an operator
// pushes through the console. Anything past that is either a mistake or an
// attempt to make the console allocate until it dies.
//
// The limit is enforced by http.MaxBytesReader rather than by a counter, so the
// read itself fails: a handler that streams, ignores the error, or loops until
// EOF cannot keep pulling bytes past the cap.
const maxRequestBody = 128 << 20

// withBodyLimit caps the size of every request body.
//
// It sits in the middleware chain rather than in each handler so a new endpoint
// cannot forget it. Without it a single client can POST an unbounded stream and
// exhaust the console's memory, and the console is the one process an operator
// cannot afford to lose. MaxBytesReader also closes the connection when the cap
// trips, so the client learns immediately instead of after a timeout.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			// A declared length past the cap is refused before a single byte is
			// read. MaxBytesReader below catches it too, but only after the
			// handler has already pulled maxRequestBody into memory, and the
			// point of the cap is that an oversized request costs nothing.
			// ContentLength is -1 when the length is unknown (chunked), which
			// falls through to MaxBytesReader as before.
			if r.ContentLength > maxRequestBody {
				writeErr(w, http.StatusRequestEntityTooLarge,
					"request body too large")
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}
