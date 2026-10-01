package api

import (
	"math"
	"net/http"
	"strconv"
)

// --- WireGuard tunnels ---

func (s *Server) handleWGClientConfig(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	cfg, err := c.GenerateWGClientConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleWGUniqueIP(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	ip, err := c.GenerateUniqueIP()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ip": ip})
}

func (s *Server) handleWGForwarders(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	forwarders, err := c.WGForwarders(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forwarders": forwarders})
}

func (s *Server) handleWGStartPortForward(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		LocalPort     int32  `json:"local_port"`
		RemoteAddress string `json:"remote_address"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	fwd, async, err := c.WGStartPortForward(id, req.LocalPort, req.RemoteAddress)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forwarder": fwd, "async": async})
}

func (s *Server) handleWGStopPortForward(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	fwdID, err := strconv.Atoi(r.PathValue("fwdID"))
	if err != nil || fwdID < 0 || fwdID > math.MaxInt32 {
		// Bounded before the narrowing conversion. Atoi accepts anything an int64
		// holds, so 2147483648 would otherwise wrap to a negative int32 and be
		// sent to the server as an id that cannot exist -- producing an error
		// that describes the server's confusion rather than the request's.
		writeErr(w, http.StatusBadRequest, "invalid forwarder id")
		return
	}
	fwd, async, err := c.WGStopPortForward(id, int32(fwdID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forwarder": fwd, "async": async})
}

func (s *Server) handleWGSocksServers(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	servers, err := c.WGSocksServers(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

func (s *Server) handleWGStartSocks(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Port int32 `json:"port"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	server, async, err := c.WGStartSocks(id, req.Port)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": server, "async": async})
}

func (s *Server) handleWGStopSocks(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	serverID, err := strconv.Atoi(r.PathValue("serverID"))
	if err != nil || serverID < 0 || serverID > math.MaxInt32 {
		writeErr(w, http.StatusBadRequest, "invalid socks id")
		return
	}
	server, async, err := c.WGStopSocks(id, int32(serverID))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": server, "async": async})
}
