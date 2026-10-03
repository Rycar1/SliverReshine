package api

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withRecover turns a handler panic into a 500 instead of a dropped connection.
//
// The guard exists because net/http's own per-connection recover leaves the
// client with an EOF and no status line, and prints the panic to stderr rather
// than to the console's log -- so a handler that panics on every call is
// invisible in sliverreshine.log.
func TestRecoverConvertsPanicTo500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		var p *int
		_ = *p // nil dereference, the shape the C2-profile bug had
	})
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
	})

	srv := httptest.NewServer(withRecover(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/boom")
	if err != nil {
		t.Fatalf("the connection was dropped instead of answered: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want JSON so the frontend can parse the error", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("the error response was not JSON: %v", err)
	}
	if body["error"] == "" {
		t.Error("the error response carried no message")
	}
	// The panic text must not reach the client: it can name internal state.
	if strings.Contains(body["error"], "nil pointer") {
		t.Errorf("the panic text leaked to the client: %q", body["error"])
	}

	// The panic must not have taken anything else down.
	resp2, err := http.Get(srv.URL + "/ok")
	if err != nil {
		t.Fatalf("the server stopped serving after a panic: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("follow-up request status = %d, want 200", resp2.StatusCode)
	}
}

// A panic after the response has started cannot become a 500, because the status
// is already on the wire. What must NOT happen is a silent truncation that the
// client reads as a complete response.
func TestRecoverDoesNotCorruptAStartedResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/partial", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial body"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic("died mid-response")
	})

	srv := httptest.NewServer(withRecover(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/partial")
	if err != nil {
		// A dropped connection is the acceptable outcome; that is the point.
		t.Logf("connection dropped mid-response, as intended: %v", err)
		return
	}
	defer resp.Body.Close()

	// If a response did arrive it must be the real 200, never a 500 spliced into
	// the middle of a body.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want either 200 with a dropped body or a dropped "+
			"connection; a 500 mid-stream would corrupt the response", resp.StatusCode)
	}
}

// The terminal WebSocket upgrade and the request logger both type-assert the
// writer. Wrapping it must not break either, or the panic guard would introduce a
// worse bug than the one it fixes.
func TestRecoverPreservesWriterInterfaces(t *testing.T) {
	// The concrete wrapper must expose the same optional interfaces as the writer
	// it wraps. httptest.ResponseRecorder implements Flusher, and the real
	// net/http writer implements both.
	pw := &panicWriter{ResponseWriter: httptest.NewRecorder()}

	if _, ok := interface{}(pw).(http.Flusher); !ok {
		t.Error("panicWriter does not implement http.Flusher; streaming responses " +
			"(terminal output, file downloads) would buffer or fail")
	}
	if _, ok := interface{}(pw).(http.Hijacker); !ok {
		t.Error("panicWriter does not implement http.Hijacker; the terminal WebSocket " +
			"upgrade would fail with \"response writer does not support hijacking\"")
	}
}

// A writer whose Hijack works must be reachable through the wrapper, not just
// present as a method that errors.
func TestPanicWriterHijackReachesTheUnderlyingWriter(t *testing.T) {
	rec := &hijackRecorder{}
	pw := &panicWriter{ResponseWriter: rec}

	hj, ok := interface{}(pw).(http.Hijacker)
	if !ok {
		t.Fatal("panicWriter is not a Hijacker")
	}
	_, _, err := hj.Hijack()
	if err == nil {
		t.Error("Hijack reached the underlying writer but reported success; the stub " +
			"should have surfaced http.ErrNotSupported")
	}
	if !rec.called {
		t.Error("Hijack on the wrapper did not reach the underlying writer")
	}
}

// hijackRecorder records that Hijack was reached.
type hijackRecorder struct {
	http.ResponseWriter
	called bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.called = true
	return nil, nil, http.ErrNotSupported
}

// Flush must survive the wrapper: streaming responses rely on it.
//
// The handler reports through the RESPONSE rather than through a captured
// variable. A shared bool would be written by the server's goroutine and read by
// the test's, which -race correctly flags -- a data race in the test is still a
// data race, and one that would make this test flaky rather than wrong.
func TestRecoverPreservesFlush(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/flush", func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "not a flusher"})
			return
		}
		_, _ = w.Write([]byte("streaming"))
		f.Flush()
	})

	srv := httptest.NewServer(withRecover(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/flush")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; the wrapped writer did not support Flush",
			resp.StatusCode)
	}
}

// http.ErrAbortHandler is net/http's documented sentinel for "abort without
// logging". The guard must not convert it into a 500, which would change
// documented behaviour for any handler that uses it.
func TestRecoverHonoursErrAbortHandler(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/abort", func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	})

	srv := httptest.NewServer(withRecover(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/abort")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusInternalServerError {
			t.Error("ErrAbortHandler became a 500; net/http documents it as an abort " +
				"that must not be logged or answered")
		}
	}
}

// The guard must not rewrite a normal response.
func TestRecoverPassesNormalResponsesThrough(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/normal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusTeapot, map[string]int{"n": 42})
	})

	srv := httptest.NewServer(withRecover(mux))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/normal")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want 418 (the guard must not rewrite normal responses)", resp.StatusCode)
	}
	var got map[string]int
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("body was altered: %v", err)
	}
	if got["n"] != 42 {
		t.Errorf("body = %v, want n=42", got)
	}
}

// The guard is only useful if it is wired in. Testing withRecover on its own
// would pass even if Routes() forgot to install it, so this drives the REAL
// chain -- from a fresh Server, through auth, logging and recover -- with a
// handler that panics.
func TestRoutesChainInstallsRecover(t *testing.T) {
	s := New() // auth disabled, so the request reaches the mux

	inner := http.NewServeMux()
	inner.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("probe: the guard is not installed")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	s.wrap(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500. A panic through the assembled chain means "+
			"withRecover is missing from wrap(), so the connection would be dropped "+
			"and the request would never appear in sliverreshine.log", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "error") {
		t.Errorf("the panic produced no JSON error body: %q", rec.Body.String())
	}
}

// Auth must still gate the panic: an unauthenticated caller gets 401, never the
// handler.
func TestRecoverDoesNotBypassAuth(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "op", Pass: "secret", Realm: "test"})

	inner := http.NewServeMux()
	inner.HandleFunc("/panic", func(w http.ResponseWriter, r *http.Request) {
		panic("must not be reached without credentials")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	s.wrap(inner).ServeHTTP(rec, req)

	if rec.Code == http.StatusInternalServerError {
		t.Error("an unauthenticated request reached the panicking handler; auth must " +
			"run outside the mux")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}
