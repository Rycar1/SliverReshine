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
	writeResult(w, map[string]any{"canaries": canaries}, err)
}
