package api

import (
	"net/http"
	"strconv"
)

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	res, err := c.Ping(id)
	writeResult(w, res, err)
}

func (s *Server) handleDeleteImplantBuild(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "missing build name")
		return
	}
	if err := c.DeleteImplantBuild(name); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRegenerate(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		ImplantName string `json:"implantName"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.Regenerate(req.ImplantName)
	writeResult(w, res, err)
}

func (s *Server) handleGetOperators(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	ops, err := c.GetOperators()
	writeResult(w, map[string]any{"operators": ops}, err)
}

func (s *Server) handlePortfwdList(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	pfm, err := c.PortForwards()
	writeResult(w, map[string]any{"forwards": pfm.List()}, err)
}

func (s *Server) handlePortfwdStart(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		SessionID  string `json:"session_id"`
		BindAddr   string `json:"bind_addr"`
		BindPort   uint32 `json:"bind_port"`
		RemoteHost string `json:"remote_host"`
		RemotePort uint32 `json:"remote_port"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.SessionID == "" || req.RemotePort == 0 {
		writeErr(w, http.StatusBadRequest, "session_id and remote_port required")
		return
	}
	pfm, err := c.PortForwards()
	if err != nil {
		writeClientError(w, err)
		return
	}
	pf, err := pfm.Forward(req.SessionID, req.BindAddr, req.BindPort, req.RemotePort, req.RemoteHost)
	writeResult(w, map[string]any{
		"success":   true,
		"localAddr": pf.LocalAddr,
		"localPort": pf.LocalPort,
	}, err)
}

func (s *Server) handlePortfwdStop(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	port, err := strconv.ParseUint(r.PathValue("port"), 10, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid port")
		return
	}
	pfm, err := c.PortForwards()
	if err != nil {
		writeClientError(w, err)
		return
	}
	if err := pfm.Stop(uint32(port)); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
