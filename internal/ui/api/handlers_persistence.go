package api

import (
	"net/http"
	"strings"

	"sliverreshine/internal/ui/sliver"
)

// --- Persistence ---

// resolvePlatform decides which command family to build.
//
// The session's own operating system wins whenever it is known. A caller that
// sends "windows" while talking to a Linux implant is describing a bug rather
// than an intention, and the alternative — trusting the request — would build a
// cron line for a Windows host, which fails in a way that looks like a broken
// module instead of a wrong request. An explicit platform is only used when the
// session cannot be resolved, which is the offline catalog case.
//
// Returns "" when neither source yields a platform.
func (s *Server) resolvePlatform(c *sliver.Client, id, requested string) string {
	_, os := s.sessionMetaFor(c, id)
	if os = strings.ToLower(strings.TrimSpace(os)); os != "" {
		return os
	}
	return strings.ToLower(strings.TrimSpace(requested))
}

const errNoPlatform = "platform is required (the session OS could not be resolved)"

func (s *Server) handlePersistenceModules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"modules": sliver.PersistenceModules()})
}

func (s *Server) handlePersistenceList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}

	platform := s.resolvePlatform(c, id, r.URL.Query().Get("platform"))
	if platform == "" {
		writeErr(w, http.StatusBadRequest, errNoPlatform)
		return
	}

	// An optional name lets the inventory answer for name-scoped mechanisms
	// instead of reporting them as uncheckable. The value is never used to build
	// a command here -- PersistenceList passes it through detectPersistence's
	// validation like every other entry point.
	list, err := c.PersistenceList(id, platform, r.URL.Query().Get("name"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handlePersistenceInstall(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Platform string `json:"platform"`
		Module   string `json:"module"`
		Payload  string `json:"payload"`
		Name     string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Module) == "" {
		writeErr(w, http.StatusBadRequest, "module is required")
		return
	}

	platform := s.resolvePlatform(c, id, req.Platform)
	if platform == "" {
		writeErr(w, http.StatusBadRequest, errNoPlatform)
		return
	}

	result, err := c.PersistenceInstall(id, platform, req.Module, req.Payload, req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handlePersistenceRemove(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Platform string `json:"platform"`
		Module   string `json:"module"`
		Payload  string `json:"payload"`
		Name     string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Module) == "" {
		writeErr(w, http.StatusBadRequest, "module is required")
		return
	}

	platform := s.resolvePlatform(c, id, req.Platform)
	if platform == "" {
		writeErr(w, http.StatusBadRequest, errNoPlatform)
		return
	}

	result, err := c.PersistenceRemove(id, platform, req.Module, req.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
