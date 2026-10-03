package api

import (
	"c2tool/internal/ui/sliver"
	"net/http"
	"strconv"
)

func (s *Server) handleRportFwdList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	listeners, err := c.RportFwdListeners(id)
	writeResult(w, map[string]any{"listeners": listeners}, err)
}

func (s *Server) handleRportFwdStart(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		BindAddress    string `json:"bindAddress"`
		BindPort       uint32 `json:"bindPort"`
		ForwardAddress string `json:"forwardAddress"`
		ForwardPort    uint32 `json:"forwardPort"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	l, err := c.StartRportFwdListener(id, req.BindAddress, req.BindPort, req.ForwardAddress, req.ForwardPort)
	writeResult(w, l, err)
}

func (s *Server) handleRportFwdStop(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	raw := r.PathValue("fwdID")
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid forward id")
		return
	}
	if err := c.StopRportFwdListener(id, uint32(n)); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Certificates
// ---------------------------------------------------------------------------

func (s *Server) handleCACertificates(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	certs, err := c.CertificateAuthority()
	writeResult(w, map[string]any{"certificates": certs}, err)
}

func (s *Server) handleCertificates(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	category := uint32(0)
	if v := r.URL.Query().Get("category"); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid category")
			return
		}
		category = uint32(n)
	}
	certs, err := c.Certificates(category, r.URL.Query().Get("cn"))
	writeResult(w, map[string]any{"certificates": certs}, err)
}

// ---------------------------------------------------------------------------
// Tunnels
// ---------------------------------------------------------------------------

func (s *Server) handleTunnelCreate(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	tunnelID, err := c.CreateTunnel(id)
	writeResult(w, map[string]any{"tunnelID": tunnelID}, err)
}

func (s *Server) handleTunnelClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TunnelID  uint64 `json:"tunnelID"`
		SessionID string `json:"sessionID"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	if err := c.CloseTunnel(req.TunnelID, req.SessionID); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Windows services (detail / start by name)
// ---------------------------------------------------------------------------
