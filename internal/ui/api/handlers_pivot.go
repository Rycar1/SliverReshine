package api

import (
	"net/http"
	"strconv"
)

// --- Pivots ---

// handleTopology renders the aggregate network view. Unlike the pivot tree this
// is not a single Sliver call: it is assembled console-side from every source
// that describes a relationship, so it takes no parameters.
func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	graph, err := c.TopologyGraph()
	writeResult(w, graph, err)
}

func (s *Server) handlePivotGraph(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	graph, err := c.PivotGraph()
	writeResult(w, graph, err)
}

func (s *Server) handlePivotListeners(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	listeners, err := c.PivotSessionListeners(id)
	writeResult(w, map[string]any{"listeners": listeners}, err)
}

func (s *Server) handlePivotStartListener(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Type        string `json:"type"`
		BindAddress string `json:"bind_address"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	listener, err := c.PivotStartListener(id, req.Type, req.BindAddress)
	writeResult(w, listener, err)
}

func (s *Server) handlePivotStopListener(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	rawID := r.PathValue("pivotID")
	pivotID, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid pivot listener id")
		return
	}
	if err := c.PivotStopListener(id, uint32(pivotID)); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
