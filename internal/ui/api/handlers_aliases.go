package api

import (
	"net/http"

	"c2tool/internal/ui/sliver"
)

// --- Prune ---

func (s *Server) handlePruneBeacons(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Days int `json:"days"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Days <= 0 {
		writeErr(w, http.StatusBadRequest, "days must be a positive integer")
		return
	}
	pruned, err := c.PruneBeacons(req.Days)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "pruned": pruned})
}

func (s *Server) handlePruneSessions(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	pruned, err := c.PruneSessions()
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "pruned": pruned})
}

// --- Aliases ---

func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	aliases, err := sliver.ListAliases()
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"aliases": aliases})
}

func (s *Server) handleAliasInstall(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		BundleB64 string `json:"bundle_b64"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.BundleB64 == "" {
		writeErr(w, http.StatusBadRequest, "bundle_b64 is required")
		return
	}
	alias, err := sliver.InstallAlias(req.BundleB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "alias": alias})
}

func (s *Server) handleAliasRemove(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "missing alias name")
		return
	}
	if err := sliver.RemoveAlias(name); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleAliasRun(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "missing alias name")
		return
	}
	var req struct {
		Args    string `json:"args"`
		Process string `json:"process"`
		Arch    string `json:"arch"`
		Method  string `json:"method"`
		Class   string `json:"class"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	alias, result, err := c.RunAlias(id, name, req.Args, req.Process, req.Arch, req.Method, req.Class)
	if err != nil {
		writeClientError(w, err)
		return
	}
	result["success"] = true
	result["alias"] = alias
	writeJSON(w, http.StatusOK, result)
}
