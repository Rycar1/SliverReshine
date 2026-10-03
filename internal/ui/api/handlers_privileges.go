package api

import (
	"net/http"
)

// --- Privilege escalation / tokens ---

func (s *Server) handleGetPrivs(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	privs, err := c.GetPrivs(id)
	writeResult(w, map[string]any{"privileges": privs}, err)
}

func (s *Server) handleCurrentTokenOwner(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	owner, err := c.CurrentTokenOwner(id)
	writeResult(w, map[string]string{"owner": owner}, err)
}

func (s *Server) handleExecuteToken(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path   string   `json:"path"`
		Args   []string `json:"args"`
		Output bool     `json:"output"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.ExecuteToken(id, req.Path, req.Args, req.Output)
	writeResult(w, res, err)
}

func (s *Server) handleRunAs(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Username    string `json:"username"`
		ProcessName string `json:"process_name"`
		Args        string `json:"args"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	output, async, err := c.RunAs(id, req.Username, req.ProcessName, req.Args)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"output": output, "async": async})
}
