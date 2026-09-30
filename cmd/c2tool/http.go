package main

import (
	"net/http"
	"time"

	"c2tool/internal/ui/api"
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

// ensure the api package is referenced even if Routes() moves.
var _ = api.New
