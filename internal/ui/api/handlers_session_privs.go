package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) handleExecAssembly(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Assembly string `json:"assembly"` // base64
		Args     string `json:"arguments"`
		Process  string `json:"process"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Assembly)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 assembly")
		return
	}
	res, err := c.ExecuteAssembly(id, data, req.Args, req.Process)
	writeResult(w, res, err)
}

func (s *Server) handleSideload(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Data        string `json:"data"` // base64
		ProcessName string `json:"processName"`
		Args        string `json:"args"`
		EntryPoint  string `json:"entryPoint"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	res, err := c.Sideload(id, data, req.ProcessName, req.Args, req.EntryPoint)
	writeResult(w, res, err)
}

func (s *Server) handleSpawnDll(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Data        string `json:"data"` // base64
		ProcessName string `json:"processName"`
		Args        string `json:"args"`
		EntryPoint  string `json:"entryPoint"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	res, err := c.SpawnDll(id, data, req.ProcessName, req.Args, req.EntryPoint)
	writeResult(w, res, err)
}

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Pid      uint32 `json:"pid"`
		ProcName string `json:"procName"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Pid == 0 && req.ProcName == "" {
		writeErr(w, http.StatusBadRequest, "either pid or procName is required")
		return
	}
	if err := c.Migrate(id, req.Pid, req.ProcName); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleProcessDump(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Pid int32 `json:"pid"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.ProcessDump(id, req.Pid)
	if err != nil {
		writeClientError(w, err)
		return
	}
	// The minidump streams straight to the browser as a file download. Base64
	// through JSON inflated a 500 MB dump by a third and forced the whole thing
	// to be buffered as a string before it could be sent.
	filename := fmt.Sprintf("dump-%d-%s.dmp", req.Pid, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(len(res.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Data)
}

func (s *Server) handleImpersonate(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Impersonate(id, req.Username); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMakeToken(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Domain   string `json:"domain"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MakeToken(id, req.Username, req.Password, req.Domain); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRevToSelf(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	if err := c.RevToSelf(id); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		HostingProcess string `json:"hostingProcess"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.GetSystem(id, req.HostingProcess); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
