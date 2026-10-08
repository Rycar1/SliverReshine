package api

import (
	"net/http"
	"strings"

	"sliverreshine/internal/ui/sliver"
)

// handleWebDeliveryFormats lists the fetch-and-run templates the UI offers.
//
// This is a static list, but it is served from the backend rather than hardcoded
// in the frontend so the two cannot disagree about which format id is valid:
// a mismatch there produces a one-liner for the wrong platform.
func (s *Server) handleWebDeliveryFormats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"formats": sliver.WebDeliveryFormats()})
}

// handleWebDelivery builds a stage, publishes it, and returns the one-liner.
//
// It is a POST because it has side effects: an implant is built if the profile
// has none, content is published, and a listener is started.
func (s *Server) handleWebDelivery(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req sliver.WebDeliveryRequest
	if !decodeBody(w, r, &req) {
		return
	}
	// An empty host means "the address the operator reached this console on":
	// it routes here by construction, which beats a listener's wildcard bind.
	// ConsoleHostFromHeader drops loopback and wildcard headers, so this only
	// fills in something a target could actually fetch from.
	if strings.TrimSpace(req.Host) == "" {
		req.Host = sliver.ConsoleHostFromHeader(r.Host)
	}
	res, err := c.WebDelivery(req)
	writeResult(w, res, err)
}
