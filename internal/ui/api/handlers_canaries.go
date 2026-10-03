package api

import (
	"net/http"
)

// --- DNS canaries ---

func (s *Server) handleCanaries(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	canaries, err := c.Canaries()
	if err != nil {
		writeClientError(w, err)
		return
	}
	canaries, ok := paginate(w, r, canaries)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canaries": canaries})
}
