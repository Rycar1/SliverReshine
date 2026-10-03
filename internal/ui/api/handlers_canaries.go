package api

import (
	"net/http"
	"sliverreshine/internal/ui/sliver"
)

// --- DNS canaries ---

func (s *Server) handleCanaries(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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
