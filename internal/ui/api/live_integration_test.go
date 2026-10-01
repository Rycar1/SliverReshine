package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// This file runs the console the way an operator does: over a real TCP socket,
// against the real router, the real middleware chain and the real embedded
// frontend. Every other test in this package drives the handler in-process, so
// none of them exercise HTTP framing, the listener, or the static file path --
// which is exactly where the audit's middleware changes landed.
//
// The Sliver client is deliberately absent, so the /api routes answer 503. That
// is the point: it proves the request reached its handler, which a 404 or a 415
// would not.

// liveConsole starts the console on a real port and returns its base URL.
func liveConsole(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(New().Routes())
	t.Cleanup(srv.Close)
	return srv.URL
}

// liveConsoleWithAuth starts the console behind HTTP Basic.
func liveConsoleWithAuth(t *testing.T, user, pass string) string {
	t.Helper()
	api := New()
	api.SetBasicAuth(&BasicAuth{User: user, Pass: pass})
	srv := httptest.NewServer(api.Routes())
	t.Cleanup(srv.Close)
	return srv.URL
}

func get(t *testing.T, url string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

// The frontend must actually be served. If web/dist is empty the console answers
// with a placeholder string instead, which is a real failure mode for anyone who
// builds the backend without building the frontend first.
func TestLiveConsoleServesTheFrontend(t *testing.T) {
	base := liveConsole(t)

	resp, body := get(t, base+"/", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "<div id=\"root\"") && !strings.Contains(body, "<div id=root") {
		// Distinguish "the frontend is missing" from "the frontend changed shape"
		// so the failure names the right problem.
		if strings.Contains(body, "Run `npm run build`") {
			t.Fatal("the embedded frontend is missing; build frontend/ and copy dist/ into web/dist")
		}
		t.Fatalf("GET / did not return the SPA shell; first 200 bytes: %.200s", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// The hashed asset bundle has to be reachable, or the page loads and then shows
// nothing.
func TestLiveConsoleServesBuiltAssets(t *testing.T) {
	base := liveConsole(t)

	_, index := get(t, base+"/", nil)
	start := strings.Index(index, "/assets/")
	if start < 0 {
		t.Skip("the built index does not reference an asset bundle")
	}
	rest := index[start:]
	end := strings.IndexAny(rest, `"'`)
	if end < 0 {
		t.Fatal("could not parse the asset path out of index.html")
	}
	asset := rest[:end]

	resp, body := get(t, base+asset, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", asset, resp.StatusCode)
	}
	if len(body) == 0 {
		t.Fatalf("GET %s returned an empty body", asset)
	}
}

// An unknown client-side route must return the SPA shell, not a 404: the console
// uses history routing and a refresh on /sessions/abc has to work.
func TestLiveConsoleSpaFallback(t *testing.T) {
	base := liveConsole(t)

	resp, body := get(t, base+"/sessions/some-id/terminal", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deep link = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "<html") && !strings.Contains(body, "<!doctype") && !strings.Contains(body, "<!DOCTYPE") {
		t.Errorf("the SPA fallback did not return HTML; first 120 bytes: %.120s", body)
	}
}

// Auth must challenge over a real socket, and must not leak a stack banner.
func TestLiveConsoleAuthOverSocket(t *testing.T) {
	base := liveConsoleWithAuth(t, "operator", "secret-pass")

	resp, _ := get(t, base+"/api/info", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /api/info = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic ") {
		t.Errorf("WWW-Authenticate = %q, want a Basic challenge", got)
	}
	if got := resp.Header.Get("Server"); got != "" {
		t.Errorf("Server header leaked: %q", got)
	}

	// The right credentials must be accepted.
	req, _ := http.NewRequest(http.MethodGet, base+"/api/info", nil)
	req.SetBasicAuth("operator", "secret-pass")
	ok, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusOK {
		t.Fatalf("authenticated /api/info = %d, want 200", ok.StatusCode)
	}

	// The wrong ones must not.
	bad, _ := http.NewRequest(http.MethodGet, base+"/api/info", nil)
	bad.SetBasicAuth("operator", "wrong")
	badResp, err := http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	defer badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", badResp.StatusCode)
	}
}

// The CSRF guard has to hold over a real socket, including the simple-request
// shape a cross-origin form produces.
func TestLiveConsoleCSRFOverSocket(t *testing.T) {
	base := liveConsole(t)

	// A form post: no preflight, Content-Type a simple request can set.
	form, _ := http.NewRequest(http.MethodPost, base+"/api/sessions/abc/kill",
		strings.NewReader("a=1"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Header.Set("Origin", "http://evil.example")
	formResp, err := http.DefaultClient.Do(form)
	if err != nil {
		t.Fatal(err)
	}
	formResp.Body.Close()
	if formResp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("cross-site form post = %d, want 415", formResp.StatusCode)
	}

	// JSON but a foreign origin.
	foreign, _ := http.NewRequest(http.MethodPost, base+"/api/sessions/abc/kill",
		strings.NewReader(`{}`))
	foreign.Header.Set("Content-Type", "application/json")
	foreign.Header.Set("Origin", "http://evil.example")
	foreignResp, err := http.DefaultClient.Do(foreign)
	if err != nil {
		t.Fatal(err)
	}
	foreignResp.Body.Close()
	if foreignResp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign-origin JSON post = %d, want 403", foreignResp.StatusCode)
	}

	// Same-origin JSON reaches the handler, which answers 503 with no client.
	good, _ := http.NewRequest(http.MethodPost, base+"/api/sessions/abc/kill",
		strings.NewReader(`{}`))
	good.Header.Set("Content-Type", "application/json")
	good.Header.Set("Origin", base)
	goodResp, err := http.DefaultClient.Do(good)
	if err != nil {
		t.Fatal(err)
	}
	goodResp.Body.Close()
	if goodResp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("same-origin JSON post = %d, want 503 (handler reached)", goodResp.StatusCode)
	}
}

// The security headers have to survive a real response, including on the static
// path where a browser would actually act on them.
func TestLiveConsoleSecurityHeadersOverSocket(t *testing.T) {
	base := liveConsole(t)

	for _, path := range []string{"/", "/api/info", "/sessions/deep-link"} {
		resp, _ := get(t, base+path, nil)
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, got)
		}
		if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options = %q", path, got)
		}
	}
}

// A cross-origin WebSocket upgrade must be refused before it becomes a socket.
func TestLiveConsoleTerminalRejectsForeignOrigin(t *testing.T) {
	base := liveConsole(t)

	req, _ := http.NewRequest(http.MethodGet, base+"/ws/sessions/abc/terminal", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// 101 would mean the hijack succeeded. The handshake check must stop it
	// before the upgrade, so anything else is acceptable -- but not 101.
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("a cross-origin WebSocket upgrade was accepted")
	}
}

// An empty session id must be a plain 400, not an upgraded socket that dies.
func TestLiveConsoleTerminalRejectsEmptySessionID(t *testing.T) {
	base := liveConsole(t)

	req, _ := http.NewRequest(http.MethodGet, base+"/ws/sessions//terminal", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatal("an empty session id was upgraded to a WebSocket")
	}
}

// The path-traversal fix has to hold through the router, not just at the
// function boundary -- the whole finding was that ServeMux decodes %2F before
// PathValue sees it.
func TestLiveConsoleAliasTraversalIsRefused(t *testing.T) {
	base := liveConsole(t)

	req, _ := http.NewRequest(http.MethodDelete,
		base+"/api/aliases/..%2F..%2F..%2FWindows", nil)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	// Either the guard rejected the name (400) or the handler refused before
	// touching the filesystem. What must not happen is a 200.
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("a traversal alias name was accepted: %s", body)
	}
	if strings.Contains(string(body), "not inside") {
		t.Logf("rejected by the containment guard: %s", body)
	}
}
