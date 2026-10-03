package api

import (
	"net/http"
)

func (s *Server) handleServiceDetail(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Hostname string `json:"hostname"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	detail, err := c.ServiceDetail(id, req.Name, req.Hostname)
	writeResult(w, detail, err)
}

func (s *Server) handleServiceStartByName(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name     string `json:"name"`
		Hostname string `json:"hostname"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.StartServiceByName(id, req.Name, req.Hostname); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Registry hive extraction
// ---------------------------------------------------------------------------

// handleRegistryHive dumps a hive and streams it back as a download. The bytes

func (s *Server) handleRegistryHive(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		RootHive      string `json:"rootHive"`
		RequestedHive string `json:"requestedHive"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, encoder, err := c.RegistryReadHive(id, req.RootHive, req.RequestedHive)
	if err != nil {
		writeClientError(w, err)
		return
	}
	// The implant may compress or base64 the hive depending on platform; report
	// the encoder in a header so the caller knows how to decode what it received.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Hive-Encoder", encoder)
	// The name is sanitised before it becomes a header value. It comes from the
	// request body, and the other two download sites already run it through
	// headerSafeFilename -- this one concatenated it raw, so a value containing a
	// double quote closed the filename early and one containing CR or LF put those
	// bytes on the wire inside the header value. The helper reduces the name to one
	// path element and strips the header metacharacters; the fallback keeps the
	// download nameable when the name is nothing but filtered characters.
	hiveName := headerSafeFilename(req.RequestedHive, req.RootHive+".hive")
	w.Header().Set("Content-Disposition", `attachment; filename="`+hiveName+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
