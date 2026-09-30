package api

import (
	"encoding/base64"
	"net/http"
)

// --- Windows services ---

func (s *Server) handleStartService(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		ServiceName string `json:"service_name"`
		Description string `json:"description"`
		BinPath     string `json:"bin_path"`
		Hostname    string `json:"hostname"`
		Arguments   string `json:"arguments"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.StartService(id, req.ServiceName, req.Description, req.BinPath, req.Hostname, req.Arguments); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleStopService(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		ServiceName string `json:"service_name"`
		Hostname    string `json:"hostname"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.StopService(id, req.ServiceName, req.Hostname); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRemoveService(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		ServiceName string `json:"service_name"`
		Hostname    string `json:"hostname"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RemoveService(id, req.ServiceName, req.Hostname); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- SSH ---

func (s *Server) handleRunSSHCommand(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Hostname string `json:"hostname"`
		Port     uint32 `json:"port"`
		Command  string `json:"command"`
		Password string `json:"password"`
		PrivKey  string `json:"priv_key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var privKey []byte
	if req.PrivKey != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.PrivKey)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid priv_key: must be base64")
			return
		}
		privKey = decoded
	}
	result, err := c.RunSSHCommand(id, req.Username, req.Hostname, req.Port, req.Command, req.Password, privKey)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// --- Extensions ---

func (s *Server) handleListExtensions(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	names, err := c.ListExtensions(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if names == nil {
		names = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"names": names})
}

func (s *Server) handleRegisterExtension(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name    string `json:"name"`
		OS      string `json:"os"`
		Init    string `json:"init"`
		DataB64 string `json:"data_b64"`
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
	if err := c.RegisterExtension(id, req.Name, req.OS, req.Init, data); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleCallExtension(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Export      string `json:"export"`
		ServerStore bool   `json:"server_store"`
		ArgsB64     string `json:"args_b64"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var args []byte
	if req.ArgsB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(req.ArgsB64)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid args_b64")
			return
		}
		args = decoded
	}
	result, err := c.CallExtension(id, req.Name, req.Export, req.ServerStore, args)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}
