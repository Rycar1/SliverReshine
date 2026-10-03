package api

import (
	"net/http"
	"sliverreshine/internal/ui/sliver"
)

// --- Hosts / IOC management ---

func (s *Server) handleHosts(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	hosts, err := c.Hosts()
	if err != nil {
		writeClientError(w, err)
		return
	}
	hosts, ok := paginate(w, r, hosts)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": hosts})
}

func (s *Server) handleHost(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	host, err := c.Host(r.PathValue("uuid"))
	if err != nil {
		writeClientError(w, err)
		return
	}
	if host == nil {
		writeErr(w, http.StatusNotFound, "host not found")
		return
	}
	writeJSON(w, http.StatusOK, host)
}

func (s *Server) handleHostRm(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.HostRm(r.PathValue("uuid")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleHostIOCRm(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.HostIOCRm(r.PathValue("iocID")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
