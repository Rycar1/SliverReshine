package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// tripwireReader records whether anything ever read from the body. It is the
// point of the Content-Length pre-check: an oversized upload must be refused
// without the console allocating or copying a byte of it.
type tripwireReader struct {
	read bool
}

func (t *tripwireReader) Read([]byte) (int, error) {
	t.read = true
	return 0, io.EOF
}

// A declared Content-Length past the cap is answered with 413 and the body is
// never touched. Without the pre-check the request still ends in an error, but
// only after the handler has read maxRequestBody bytes into memory, which is
// exactly the allocation the cap exists to prevent.
func TestBodyLimitRefusesOversizedContentLengthWithoutReading(t *testing.T) {
	innerCalled := false
	h := withBodyLimit(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		innerCalled = true
	}))

	body := &tripwireReader{}
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/x/fs/upload", nil)
	r.Body = io.NopCloser(body)
	r.ContentLength = maxRequestBody + 1
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, r)

	if innerCalled {
		t.Fatal("the handler ran for a request already known to be oversized")
	}
	if body.read {
		t.Fatal("the body was read before the size check refused it")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if got := rec.Body.String(); !strings.Contains(got, "too large") {
		t.Fatalf("body = %q, want it to explain the size limit", got)
	}
}

// A request whose length is unknown (chunked) cannot be refused up front, so it
// falls through to MaxBytesReader. This pins the mapping from that reader's
// error to 413 rather than 400: the JSON is well formed, only too big.
func TestDecodeBodyMapsBodyCapTo413(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"a":"`+strings.Repeat("x", 64)+`"}`))
	r.Body = http.MaxBytesReader(rec, r.Body, 16)

	var v struct {
		A string `json:"a"`
	}
	if decodeBody(rec, r, &v) {
		t.Fatal("decodeBody accepted a body past the cap")
	}
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
}

// A body under the cap must still reach the handler untouched, so the pre-check
// cannot silently break ordinary requests.
func TestBodyLimitPassesNormalRequest(t *testing.T) {
	called := false
	h := withBodyLimit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if _, err := io.ReadAll(r.Body); err != nil {
			t.Errorf("reading the body: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	r := httptest.NewRequest(http.MethodPost, "/api/x", strings.NewReader("hello"))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if !called {
		t.Fatal("the handler was not reached for a normal request")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}
