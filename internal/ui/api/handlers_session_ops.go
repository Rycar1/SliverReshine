package api

import (
	"net/http"
)

func (s *Server) handleIfconfig(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	ifaces, err := c.Ifconfig(id)
	writeResult(w, map[string]any{"interfaces": ifaces}, err)
}

func (s *Server) handlePs(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	procs, err := c.Ps(id)
	writeResult(w, map[string]any{"processes": procs}, err)
}

func (s *Server) handleKillProcess(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		PID   int32 `json:"pid"`
		Force bool  `json:"force"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.KillProcess(id, req.PID, req.Force); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleNetstat(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	entries, err := c.Netstat(id)
	writeResult(w, map[string]any{"entries": entries}, err)
}

func (s *Server) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	env, err := c.GetEnv(id)
	writeResult(w, map[string]any{"env": env}, err)
}

func (s *Server) handleSetEnv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.SetEnv(id, req.Key, req.Value); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleUnsetEnv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	key := r.PathValue("key")
	if err := c.UnsetEnv(id, key); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string   `json:"path"`
		Args []string `json:"args"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}
	result, err := c.Execute(id, req.Path, req.Args)
	writeResult(w, result, err)
}

func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	data, err := c.Screenshot(id)
	writeResult(w, map[string]string{"Data": data}, err)
}

// --- Registry (windows sessions) ---

func (s *Server) handleRegSubKeys(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	keys, err := c.RegistryListSubKeys(id, q.Get("hive"), q.Get("path"))
	writeResult(w, map[string]any{"keys": keys}, err)
}

func (s *Server) handleRegValues(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	values, err := c.RegistryListValues(id, q.Get("hive"), q.Get("path"))
	writeResult(w, map[string]any{"values": values}, err)
}

func (s *Server) handleRegRead(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	result, err := c.RegistryRead(id, q.Get("hive"), q.Get("path"), q.Get("key"))
	writeResult(w, result, err)
}

func (s *Server) handleRegWrite(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hive  string `json:"hive"`
		Path  string `json:"path"`
		Key   string `json:"key"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RegistryWrite(id, req.Hive, req.Path, req.Key, req.Value, req.Type); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Extended session operations (P1 features) ---

func (s *Server) handleRegCreateKey(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hive string `json:"hive"`
		Path string `json:"path"`
		Key  string `json:"key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RegistryCreateKey(id, req.Hive, req.Path, req.Key); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRegDeleteKey(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hive string `json:"hive"`
		Path string `json:"path"`
		Key  string `json:"key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RegistryDeleteKey(id, req.Hive, req.Path, req.Key); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Port forwarding ---
