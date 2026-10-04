package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Server) handleMemfilesList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	dir, err := c.MemfilesList(id)
	writeResult(w, dir, err)
}

func (s *Server) handleMemfilesAdd(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	fd, err := c.MemfilesAdd(id)
	writeResult(w, map[string]any{"fd": fd}, err)
}

func (s *Server) handleMemfilesRemove(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Fd int64 `json:"fd"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MemfilesRm(id, req.Fd); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// File attributes and content search
// ---------------------------------------------------------------------------

func (s *Server) handleChmod(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path      string `json:"path"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Chmod(id, req.Path, req.Mode, req.Recursive); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleChown(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path      string     `json:"path"`
		UID       flexString `json:"uid"`
		GID       flexString `json:"gid"`
		Recursive bool       `json:"recursive"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Chown(id, req.Path, string(req.UID), string(req.GID), req.Recursive); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChtimes timestomps a file. Operators use this to restore the original

func (s *Server) handleChtimes(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path  string `json:"path"`
		ATime int64  `json:"atime"`
		MTime int64  `json:"mtime"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Chtimes(id, req.Path, req.ATime, req.MTime); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleGrep(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Pattern   string `json:"pattern"`
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
		Before    int32  `json:"before"`
		After     int32  `json:"after"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Path == "" {
		req.Path = "."
	}
	out, err := c.Grep(id, req.Pattern, req.Path, req.Recursive, req.Before, req.After)
	writeResult(w, out, err)
}

// ---------------------------------------------------------------------------
// Monitoring providers
// ---------------------------------------------------------------------------

// flexString decodes a JSON string or number and keeps its text.
//
// chown forwards uid and gid to the target as text, and a caller writing the
// JSON by hand naturally sends them as numbers ("uid":0). Decoding into a plain
// string rejected that with `cannot unmarshal number into Go struct field ...
// of type string` -- a 400 that named a Go type rather than the field, for a
// shape the API never documented. Both spellings now arrive as "0", so the wire
// format is unchanged and the confusing refusal is gone.
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*s = ""
		return nil
	}
	if data[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = flexString(v)
		return nil
	}
	if data[0] == '-' || (data[0] >= '0' && data[0] <= '9') {
		*s = flexString(string(data))
		return nil
	}
	return fmt.Errorf("expected a string or a number")
}
