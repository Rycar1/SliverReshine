package api

import (
	"net/http"
)

// --- Metasploit ---

func (s *Server) handleMsf(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Payload    string `json:"payload"`
		LHost      string `json:"lhost"`
		LPort      uint32 `json:"lport"`
		Encoder    string `json:"encoder"`
		Iterations int32  `json:"iterations"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Msf(id, req.Payload, req.LHost, req.LPort, req.Encoder, req.Iterations); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMsfRemote(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Payload    string `json:"payload"`
		LHost      string `json:"lhost"`
		LPort      uint32 `json:"lport"`
		Encoder    string `json:"encoder"`
		Iterations int32  `json:"iterations"`
		PID        uint32 `json:"pid"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MsfRemote(id, req.Payload, req.LHost, req.LPort, req.Encoder, req.Iterations, req.PID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMsfStage(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Arch     string   `json:"arch"`
		Format   string   `json:"format"`
		Port     uint32   `json:"port"`
		Host     string   `json:"host"`
		OS       string   `json:"os"`
		Protocol string   `json:"protocol"`
		BadChars []string `json:"bad_chars"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	stager, err := c.MsfStage(req.Arch, req.Format, req.Port, req.Host, req.OS, req.Protocol, req.BadChars)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stager)
}
