package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
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
		mux.HandleFunc(r.method+" "+r.pattern, r.handler)
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

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	c := s.Client()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	ver, err := c.Version()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "version": ver})
}

// handleOverview aggregates top-level counts for the sidebar badges and dashboard.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	type countResult struct {
		key string
		n   int
		err error
	}
	results := make(chan countResult, 8)
	run := func(key string, fn func() (int, error)) {
		go func() {
			n, err := fn()
			results <- countResult{key: key, n: n, err: err}
		}()
	}

	run("sessions", func() (int, error) {
		ss, err := c.Sessions()
		return len(ss), err
	})
	run("beacons", func() (int, error) {
		bs, err := c.Beacons()
		return len(bs), err
	})
	run("jobs", func() (int, error) {
		js, err := c.Jobs()
		return len(js), err
	})
	run("builders", func() (int, error) {
		bs, err := c.ImplantBuilds()
		return len(bs), err
	})
	run("socks", func() (int, error) {
		return len(c.Socks().List()), nil
	})

	out := map[string]int{"sessions": 0, "beacons": 0, "jobs": 0, "builders": 0, "socks": 0}
	timeout := time.After(12 * time.Second)
	for i := 0; i < 5; i++ {
		select {
		case res := <-results:
			if res.err == nil {
				out[res.key] = res.n
			}
		case <-timeout:
			writeErr(w, http.StatusGatewayTimeout, "overview collection timed out")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": out})
}

// connectRequest carries the raw sliver-client profile config loaded from a
// file by the UI. Connection now depends entirely on this config file rather
// than manually supplied connection parameters.
type connectRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Content == "" {
		writeErr(w, http.StatusBadRequest, "missing config file content")
		return
	}
	cfg, err := sliver.ParseProfile([]byte(req.Content))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	client, err := sliver.Connect(cfg)
	if err != nil {
		writeClientError(w, err)
		return
	}
	s.SetClient(client)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.ClearClient()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": sliver.ListProfiles()})
}

func (s *Server) handleUseProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "invalid profile name")
		return
	}
	cfg, err := sliver.LoadProfile(name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	client, err := sliver.Connect(cfg)
	if err != nil {
		writeClientError(w, err)
		return
	}
	s.SetClient(client)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	sessions, err := c.Sessions()
	writeResult(w, map[string]any{"sessions": sessions}, err)
}

func (s *Server) handleBeacons(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	beacons, err := c.Beacons()
	writeResult(w, map[string]any{"beacons": beacons}, err)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	jobs, err := c.Jobs()
	writeResult(w, map[string]any{"jobs": jobs}, err)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	events, err := c.Events()
	if err != nil {
		writeClientError(w, err)
		return
	}
	events, ok := paginate(w, r, events)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleKillSession(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if err := c.KillSession(id); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleBuilders(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	builds, err := c.ImplantBuilds()
	writeResult(w, map[string]any{"builders": builds}, err)
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := c.GenerateImplant(&req)
	writeResult(w, result, err)
}

func (s *Server) handleListeners(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Type string `json:"type"`
		Addr string `json:"addr"`
		Port int    `json:"port"`
		TLS  bool   `json:"tls"`
		// Website lets an HTTP listener serve files published by WebDelivery.
		// It was not accepted here at all, so the field was silently dropped:
		// the listener started, reported success, and answered 404 for every
		// published path.
		Website string `json:"website"`
		// Domain is what the implant's callback URIs are built from.
		Domain string `json:"domain"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	addr := req.Addr
	if addr == "" {
		addr = "0.0.0.0"
	}
	port := uint32(req.Port)
	if port == 0 {
		switch req.Type {
		case "mtls":
			port = 8888
		case "dns":
			port = 53
		case "wireguard":
			// 51820 is the WireGuard convention. This said 53, which is the DNS
			// port copied from the case above -- so a caller that omitted the
			// port got a wireguard listener on the DNS port.
			port = 51820
		default:
			port = 80
		}
	}
	// An HTTP listener needs a website to serve staged content. Defaulting it
	// here rather than requiring the operator to know the concept means
	// "start a listener, then ask for a one-liner" produces matching names
	// without either step having to know about the other.
	website := req.Website
	if website == "" && (req.Type == "http" || req.Type == "https") {
		website = defaultDeliverySite
	}
	jobID, err := c.StartListener(req.Type, addr, port, req.TLS, website, req.Domain)
	writeResult(w, map[string]any{"success": true, "job_id": jobID}, err)
}

func (s *Server) handleStopListener(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid job id")
		return
	}
	if err := c.StopJob(uint32(id)); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
