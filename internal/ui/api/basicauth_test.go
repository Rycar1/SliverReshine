package api

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func basicHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

// guarded wires a Server with credentials around a trivial handler that records
// whether it was reached.
func guarded(cfg *BasicAuth) (http.Handler, *bool) {
	reached := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	return basicAuth(cfg, inner), &reached
}

func TestBasicAuthDisabledByDefault(t *testing.T) {
	h, reached := guarded(nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("nil config must not authenticate, got %d", rec.Code)
	}
	if !*reached {
		t.Fatal("request should reach the handler when auth is disabled")
	}
}

func TestBasicAuthEmptyUserDisablesAuth(t *testing.T) {
	// An empty username means "not configured" rather than "username is empty",
	// so a missing -auth-user cannot silently become an auth bypass with a
	// guessable empty credential.
	h, reached := guarded(&BasicAuth{User: "", Pass: "secret"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK || !*reached {
		t.Fatalf("empty user should disable auth, got %d", rec.Code)
	}
}

func TestBasicAuthRejectsMissingCredentials(t *testing.T) {
	h, reached := guarded(&BasicAuth{User: "ops", Pass: "hunter2"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if *reached {
		t.Fatal("unauthenticated request must not reach the handler")
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("401 must carry a WWW-Authenticate challenge for browsers")
	}
}

func TestBasicAuthAcceptsCorrectCredentials(t *testing.T) {
	h, reached := guarded(&BasicAuth{User: "ops", Pass: "hunter2"})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", basicHeader("ops", "hunter2"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !*reached {
		t.Fatalf("valid credentials should pass, got %d", rec.Code)
	}
}

func TestBasicAuthRejectsWrongCredentials(t *testing.T) {
	h, reached := guarded(&BasicAuth{User: "ops", Pass: "hunter2"})
	for _, bad := range []string{
		basicHeader("ops", "wrong"),
		basicHeader("root", "hunter2"),
		basicHeader("", ""),
		"Basic not-base64!!",
		"Bearer hunter2",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", bad)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("credential %q should be rejected, got %d", bad, rec.Code)
		}
		if *reached {
			t.Fatalf("credential %q must not reach the handler", bad)
		}
	}
}

func TestBasicAuthCoversWebSocketUpgrade(t *testing.T) {
	// The terminal socket is the obvious hole in a front-end-only auth check:
	// an Upgrade request that skips the middleware would hand out a shell.
	h, reached := guarded(&BasicAuth{User: "ops", Pass: "hunter2"})
	req := httptest.NewRequest(http.MethodGet, "/ws/sessions/abc/terminal", nil)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized || *reached {
		t.Fatalf("websocket upgrade must be authenticated, got %d", rec.Code)
	}
}

func TestBasicAuthExemptPrefixSkipsChallenge(t *testing.T) {
	// Implant listeners live behind an exempt prefix because a payload cannot
	// answer a 401. If this regressed, every implant would stop checking in.
	h, reached := guarded(&BasicAuth{User: "ops", Pass: "hunter2", Exempt: []string{"/implant/"}})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/implant/beacon", nil))
	if rec.Code != http.StatusOK || !*reached {
		t.Fatalf("exempt path should bypass auth, got %d", rec.Code)
	}

	*reached = false
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	if rec.Code != http.StatusUnauthorized || *reached {
		t.Fatalf("non-exempt path must still be guarded, got %d", rec.Code)
	}
}

func TestBasicAuthRealmIsNotProductIdentifying(t *testing.T) {
	// The realm is echoed to unauthenticated clients and lands in scanner
	// fingerprints, so it must not name the tool.
	h, _ := guarded(&BasicAuth{User: "ops", Pass: "hunter2"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	got := rec.Header().Get("WWW-Authenticate")
	if got != `Basic realm="Restricted", charset="UTF-8"` {
		t.Fatalf("unexpected challenge: %q", got)
	}
}

func TestBasicAuthCustomRealmHonoured(t *testing.T) {
	h, _ := guarded(&BasicAuth{User: "ops", Pass: "hunter2", Realm: "Staff Only"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="Staff Only", charset="UTF-8"` {
		t.Fatalf("custom realm not applied: %q", got)
	}
}

// TestServerRoutesAreGuarded is the integration-level check: it goes through
// Routes() rather than a bare handler, so it catches a middleware wired in the
// wrong order (e.g. CORS answering preflight before auth runs).
func TestServerRoutesAreGuarded(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "ops", Pass: "hunter2"})
	h := s.Routes()

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"root", httptest.NewRequest(http.MethodGet, "/", nil)},
		{"api", httptest.NewRequest(http.MethodGet, "/api/info", nil)},
		{"terminal ws", httptest.NewRequest(http.MethodGet, "/ws/sessions/x/terminal", nil)},
		// Preflight must not be answered before the credential check.
		{"preflight", httptest.NewRequest(http.MethodOptions, "/api/info", nil)},
		{"mutating", httptest.NewRequest(http.MethodPost, "/api/generate", nil)},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, tc.req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401 without credentials, got %d", tc.name, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.Header.Set("Authorization", basicHeader("ops", "hunter2"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("valid credentials were rejected through Routes()")
	}
}

// TestServerRoutesUnauthenticatedByDefault pins the backwards-compatible
// default: an existing deployment that passes no credentials keeps working.
func TestServerRoutesUnauthenticatedByDefault(t *testing.T) {
	s := New()
	h := s.Routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("auth must stay off when not configured")
	}
}
