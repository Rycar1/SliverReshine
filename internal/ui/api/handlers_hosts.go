package api

import (
	"net/http"
)

// --- Hosts / IOC management ---

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	hosts, err := c.Hosts()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	host, err := c.Host(r.PathValue("uuid"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if host == nil {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) handleHostRm(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.HostRm(r.PathValue("uuid")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleHostIOCRm(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.HostIOCRm(r.PathValue("iocID")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
