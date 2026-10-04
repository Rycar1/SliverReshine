package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The CSRF guard is the only thing standing between a page on any origin and
// every mutating endpoint on this console, because Basic credentials are replayed
// by the browser automatically. These tests exercise the two shapes a forged
// request actually takes.

// A cross-origin <form> is the classic attack, and its Content-Type is one a
// simple request can set without a preflight. The guard refuses it before the
// handler runs.
func TestCSRFRejectsFormPost(t *testing.T) {
	for _, ct := range []string{
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x",
		"text/plain",
		"",
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/kill", strings.NewReader("x=1"))
		req.Header.Del("Content-Type")
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		req.Header.Set("Origin", "http://evil.example")

		rec := httptest.NewRecorder()
		New().Routes().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: status = %d, want %d", ct, rec.Code, http.StatusUnsupportedMediaType)
		}
	}
}

// A same-origin request with the right type must still reach its handler. The
// handler answers 503 (no Sliver connection), which is what proves it matched --
// a 415 here would mean the guard is refusing the console's own traffic.
func TestCSRFAllowsSameOriginJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/kill", strings.NewReader(`{}`))
	req.Host = "console.example:8080"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://console.example:8080")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (503 proves the handler ran)", rec.Code, http.StatusServiceUnavailable)
	}
}

// A cross-origin request that does carry JSON still fails on Origin. This is the
// case the type check alone would let through: a page that earns a preflight.
func TestCSRFRejectsForeignOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/kill", strings.NewReader(`{}`))
	req.Host = "console.example:8080"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// The console's own client -- curl, the PowerShell snippets in the README, the Go
// tests -- sends no Origin at all. Refusing those would break every scripted
// deployment to close a hole that only a browser can open.
func TestCSRFAllowsRequestWithNoOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/kill", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (a non-browser client must not be blocked)", rec.Code, http.StatusServiceUnavailable)
	}
}

// Safe methods are not the attack: a cross-origin GET cannot change state, and
// blocking it would break nothing worth breaking but would also gain nothing.
func TestCSRFDoesNotBlockReads(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.Header.Set("Origin", "http://evil.example")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHasJSONContentType(t *testing.T) {
	cases := map[string]bool{
		"application/json":                  true,
		"application/json; charset=utf-8":   true,
		"APPLICATION/JSON":                  true,
		" application/json ":                true,
		"application/json;charset=UTF-8":    true,
		"application/json-patch+json":       false,
		"text/json":                         false,
		"application/x-www-form-urlencoded": false,
		"text/plain":                        false,
		"multipart/form-data":               false,
		"":                                  false,
	}
	for header, want := range cases {
		if got := hasJSONContentType(header); got != want {
			t.Errorf("hasJSONContentType(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin string
		host   string
		want   bool
	}{
		{"http://console.example:8080", "console.example:8080", true},
		{"https://console.example", "console.example", true},
		{"http://CONSOLE.example", "console.example", true},
		// A different port is a different origin, which is the whole point.
		{"http://console.example:9999", "console.example:8080", false},
		// A scheme mismatch is refused even on the same host: an https console
		// must not accept a downgraded origin.
		{"http://console.example", "console.example:443", false},
		{"http://evil.example", "console.example:8080", false},
		{"null", "console.example:8080", false},
		{"", "console.example:8080", false},
		{"console.example:8080", "console.example:8080", false},
	}
	for _, tc := range cases {
		if got := sameOrigin(tc.origin, tc.host); got != tc.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", tc.origin, tc.host, got, tc.want)
		}
	}
}

// The terminal WebSocket does its own origin check, so it is exempt from this
// one. Pinned here so the exemption is a decision rather than an accident.
func TestCSRFExemptionIsOnlyTheTerminalSocket(t *testing.T) {
	exempt := csrfExemptPrefixes()
	if len(exempt) != 1 {
		t.Fatalf("csrfExemptPrefixes() has %d entries, want exactly 1", len(exempt))
	}
	if exempt[0] != "/ws/sessions/" {
		t.Errorf("the terminal socket is no longer exempt; the WebSocket handshake check must cover it")
	}
}

// A body-less DELETE carries no Content-Type -- that is what curl, the README's
// PowerShell snippets and the console's own client send. Requiring JSON of it
// answered the console's own traffic with a CSRF failure, a security control
// refusing a request it exists to let through. DELETE is not a simple method, so
// a browser always preflights a cross-origin one and the console answers no
// preflight; the check is not what stops that request.
func TestCSRFAllowsBodylessDelete(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/abc/fs", nil)
	req.Header.Del("Content-Type")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code == http.StatusUnsupportedMediaType {
		t.Fatalf("a body-less DELETE was refused as a CSRF failure")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d (503 proves the handler ran)", rec.Code, http.StatusServiceUnavailable)
	}
}

// Dropping the type check for DELETE must not drop the origin check with it: a
// DELETE that does carry a foreign Origin is still refused.
func TestCSRFStillRejectsForeignOriginOnDelete(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/abc/fs", nil)
	req.Host = "console.example:8080"
	req.Header.Del("Content-Type")
	req.Header.Set("Origin", "http://evil.example")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

// POST is the one mutating method a simple cross-origin request can carry, so
// its type check must stay load-bearing.
func TestCSRFStillRejectsFormPostAfterDeleteRelaxation(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/abc/kill", strings.NewReader("x=1"))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Origin", "http://evil.example")

	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnsupportedMediaType)
	}
}
