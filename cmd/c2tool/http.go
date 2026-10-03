package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"c2tool/internal/ui/api"
)

// Shutdown deadlines for the console.
//
// The console gets a short grace period for in-flight requests and is then
// closed hard; forceExitGrace is the outer ceiling on the whole shutdown, after
// which the process leaves regardless of what is still open. Both are short on
// purpose: an operator who pressed Ctrl-C wants the process gone, and no
// console request is worth blocking that.
const (
	consoleShutdownGrace = 3 * time.Second
	forceExitGrace       = 10 * time.Second
)

// newHTTPServer builds the console HTTP server. Read/write timeouts are left
// generous because several endpoints stream implant builds and terminal data
// over long-lived connections.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// shutdownConsole stops accepting connections and waits, briefly, for in-flight
// requests to finish.
//
// http.Server.Shutdown on its own does not release the console:
//
//   - it never touches a hijacked connection, and the implant terminal is one,
//     so that socket outlives the shutdown;
//   - it returns ctx.Err() when its deadline passes and leaves the remaining
//     connections alone, so the port can stay bound after the call returns.
//
// Keep-alives are disabled first so a browser polling the console is not held
// open for the whole grace period, and Close is the backstop that actually
// drops whatever is left.
func shutdownConsole(srv *http.Server) {
	srv.SetKeepAlivesEnabled(false)
	ctx, cancel := context.WithTimeout(context.Background(), consoleShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[c2tool] console shutdown incomplete (%v); closing remaining connections", err)
		_ = srv.Close()
	}
}

// ensure the api package is referenced even if Routes() moves.
var _ = api.New
