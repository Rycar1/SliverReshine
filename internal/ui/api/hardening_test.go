package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A typo'd field used to be dropped silently, which meant a 200 for a request
// that did not do what was asked. The settings file already refused unknown keys
// for the same reason; this makes the API agree with it.
func TestDecodeBodyRejectsUnknownFields(t *testing.T) {
	var dst struct {
		Command string `json:"command"`
	}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"command":"a","comand":"b"}`))
	rec := httptest.NewRecorder()

	if decodeBody(rec, req, &dst) {
		t.Fatal("a body with an unknown field was accepted")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	// The message has to name the field, or the operator is left diffing their
	// request against the docs by eye.
	if !strings.Contains(rec.Body.String(), "comand") {
		t.Errorf("the error does not name the offending field: %s", rec.Body.String())
	}
}

func TestDecodeBodyAcceptsKnownFields(t *testing.T) {
	var dst struct {
		Command string   `json:"command"`
		Flags   []string `json:"flags"`
	}
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"command":"a","flags":["b"]}`))
	rec := httptest.NewRecorder()

	if !decodeBody(rec, req, &dst) {
		t.Fatalf("a valid body was rejected: %s", rec.Body.String())
	}
	if dst.Command != "a" || len(dst.Flags) != 1 {
		t.Errorf("decoded %+v, want command=a flags=[b]", dst)
	}
}

// The security headers are the only clickjacking control the console has, so
// their presence is asserted rather than assumed.
func TestSecurityHeadersAreSet(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodGet, "/api/info", nil))

	want := map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "frame-ancestors 'none'; form-action 'self'",
	}
	for header, value := range want {
		if got := rec.Header().Get(header); got != value {
			t.Errorf("%s = %q, want %q", header, got, value)
		}
	}
}

// Headers must be on the error paths too: a 401 page is still a page a browser
// will frame if it is allowed to.
func TestSecurityHeadersAreSetOnRejections(t *testing.T) {
	auth := &BasicAuth{User: "operator", Pass: "secret"}
	srv := New()
	srv.SetBasicAuth(auth)

	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/info", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("an unauthenticated response is framable: X-Frame-Options = %q", got)
	}
}
