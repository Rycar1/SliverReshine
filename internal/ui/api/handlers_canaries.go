package api

import (
	"net/http"
)

// --- DNS canaries ---

func (s *Server) handleCanaries(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	canaries, err := c.Canaries()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"canaries": canaries})
}
