package api

import (
	"net/http"

	"c2tool/internal/ui/sliver"
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
func (s *Server) handleWebDelivery(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.WebDeliveryRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.WebDelivery(req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}
