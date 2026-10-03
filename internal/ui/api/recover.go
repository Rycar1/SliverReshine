package api

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"

	"golang.org/x/net/websocket"
)

// This file holds the panic guard for the HTTP surface.
//
// # Why it is needed even though net/http already recovers
//
// net/http recovers a panic per connection, so one bad request does not take the
// console process down. What it does NOT do is anything useful for the caller or
// for the operator:
//
//   - The client gets a dropped connection. Measured: the response was an EOF
//     with no status line and no body, so the browser reports a network error.
//     There is no way for the frontend to tell "the server crashed on this
//     request" from "the network blipped".
//   - The panic is printed to stderr by net/http, not to the console's own log,
//     so the request never appears in sliverreshine.log at all. A handler that panics on
//     every call looks, in the log the operator reads, exactly like a handler
//     that was never called.
//
// So the guard turns an invisible crash into a 500 and a log line, and it is
// placed inside withLogging so the request is recorded with its real status.
//
// # Where it sits
//
//	withLogging(withRecover(mux))
//
// Inside withLogging, so the access log shows 500 rather than no line at all.
// Around the mux, so every registered handler is covered without each one having
// to remember.

// withRecover converts a panic in a handler into a 500 and a log entry.
func withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The writer is wrapped so the recover below can tell whether the
		// response has already started. Writing a 500 into the middle of a
		// half-sent download would corrupt it rather than report the failure.
		pw := &panicWriter{ResponseWriter: w}

		defer func() {
			rec := recover()
			if rec == nil {
				return
			}

			// http.ErrAbortHandler is net/http's documented sentinel for
			// "abort this response without logging". A handler that panics with
			// it is asking to be dropped silently, so re-panic and let net/http
			// do exactly that rather than reporting it as a crash.
			if rec == http.ErrAbortHandler {
				panic(rec)
			}

			log.Printf("[api] PANIC serving %s %s from %s: %v\n%s",
				r.Method, r.URL.Path, r.RemoteAddr, rec, debug.Stack())

			if pw.wrote {
				// The status is already on the wire, so no error response can be
				// sent. Abort the connection instead: a truncated body that the
				// client reads as a complete 200 is worse than a dropped
				// connection, which at least fails loudly.
				if hj, ok := pw.ResponseWriter.(http.Hijacker); ok {
					if conn, _, err := hj.Hijack(); err == nil {
						_ = conn.Close()
						return
					}
				}
				// No hijacker: nothing left to do but log, which already
				// happened.
				return
			}

			// Nothing was sent, so a proper error response is still possible.
			// The panic text is deliberately not included: it can name internal
			// state, and the operator has it in the log.
			writeErr(w, http.StatusInternalServerError,
				"the console hit an internal error handling this request; see the console log")
		}()

		next.ServeHTTP(pw, r)
	})
}

// recoverWS logs a panic on a goroutine that runs after a WebSocket upgrade.
//
// net/http recovers a panic per connection, but that protection ends the moment
// the connection is hijacked: the goroutines the terminal runs afterwards -- the
// socket handler x/net/websocket starts, and the tunnel->browser forwarder
// started with go -- have nothing above them. A panic there is not a dropped
// request, it is a dead console in the middle of an engagement, taking every
// other operator session with it.
//
// There is no response left to write on a hijacked socket, so this records the
// crash and closes the socket: the operator sees the terminal drop and finds
// the reason in sliverreshine.log. It is meant to be deferred directly, since recover
// only works when the deferred function itself calls it.
func recoverWS(ws *websocket.Conn, where string) {
	rec := recover()
	if rec == nil {
		return
	}
	log.Printf("[api] PANIC in %s websocket: %v\n%s", where, rec, debug.Stack())
	if ws != nil {
		_ = ws.Close()
	}
}

// panicWriter records whether the response has been started.
//
// Flush and Hijack are forwarded explicitly. Embedding http.ResponseWriter alone
// would not promote them, and both the terminal WebSocket upgrade and the
// request logger type-assert the writer to http.Hijacker / http.Flusher -- so
// without these the upgrade would fail with "response writer does not support
// hijacking", which is a worse bug than the one this file exists to catch.
type panicWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *panicWriter) WriteHeader(code int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *panicWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func (w *panicWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *panicWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("response writer does not support hijacking")
}
