package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"c2tool/internal/ui/sliver"
)

// withClient is the dispatcher that replaced the two-line "fetch the client,
// return a 503 if there is none" guard that used to open 88 handlers. Both
// halves of its contract matter, and both are easy to break silently:
//
//   - the refusal must still be a 503 with the same body, because the frontend
//     keys "not connected" off it, and
//   - the handler must receive the request-scoped view from clientFor, not the
//     console-wide client. Handing over the console client would compile, pass
//     a smoke test, and quietly drop the request deadline and cancellation that
//     clientFor exists to provide.

func TestWithClientRefusesWhenDisconnected(t *testing.T) {
	called := false
	h := New().withClient(func(_ *sliver.Client, _ http.ResponseWriter, _ *http.Request) {
		called = true
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))

	if called {
		t.Fatal("the wrapped handler ran while disconnected")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "not connected") {
		t.Fatalf("body = %q, want the not-connected error", body)
	}
}

func TestWithClientPassesTheRequestScopedView(t *testing.T) {
	s := serverWithStub(&rpcStub{})

	var got *sliver.Client
	h := s.withClient(func(c *sliver.Client, _ http.ResponseWriter, _ *http.Request) {
		got = c
	})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))

	if got == nil {
		t.Fatal("the wrapped handler was not called while connected")
	}
	if got == s.Client() {
		t.Fatal("the handler got the console-wide client instead of the request-scoped view")
	}
	if got.Profile != s.Client().Profile {
		t.Errorf("view profile = %q, want %q", got.Profile, s.Client().Profile)
	}
}
