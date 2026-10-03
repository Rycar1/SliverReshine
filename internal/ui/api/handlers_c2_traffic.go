package api

import (
	"encoding/base64"
	"net/http"

	"github.com/bishopfox/sliver/protobuf/clientpb"

	"c2tool/internal/ui/sliver"
)

func (s *Server) handleMonitorProviders(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	providers, err := c.MonitorListConfig()
	writeResult(w, map[string]any{"providers": providers}, err)
}

func (s *Server) handleMonitorAdd(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	var req sliver.MonitorProviderView
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MonitorAddConfig(req); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMonitorRemove(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	var req sliver.MonitorProviderView
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MonitorDelConfig(req); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// C2 profiles
// ---------------------------------------------------------------------------

func (s *Server) handleC2Profiles(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	profiles, err := c.HTTPC2Profiles()
	writeResult(w, map[string]any{"profiles": profiles}, err)
}

func (s *Server) handleC2Profile(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	cfg, err := c.HTTPC2Profile(r.PathValue("name"))
	writeResult(w, cfg, err)
}

func (s *Server) handleC2ProfileSave(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	var req struct {
		Overwrite bool                   `json:"overwrite"`
		Profile   *clientpb.HTTPC2Config `json:"profile"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.SaveHTTPC2Profile(req.Profile, req.Overwrite); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Traffic encoders
// ---------------------------------------------------------------------------

func (s *Server) handleTrafficEncoders(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	names, err := c.TrafficEncoders()
	writeResult(w, map[string]any{"encoders": names}, err)
}

func (s *Server) handleTrafficEncoderAdd(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name      string `json:"name"`
		WasmB64   string `json:"wasm"`
		SkipTests bool   `json:"skipTests"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	wasm, err := base64.StdEncoding.DecodeString(req.WasmB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "wasm is not valid base64")
		return
	}
	report, err := c.TrafficEncoderAdd(req.Name, wasm, req.SkipTests)
	writeResult(w, report, err)
}

func (s *Server) handleTrafficEncoderRemove(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	if err := c.TrafficEncoderRm(r.PathValue("name")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Shellcode encoders
// ---------------------------------------------------------------------------

func (s *Server) handleShellcodeEncoders(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	encoders, err := c.ShellcodeEncoders()
	writeResult(w, map[string]any{"encoders": encoders}, err)
}

func (s *Server) handleShellcodeEncode(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	var req struct {
		Encoder    string `json:"encoder"`
		Arch       string `json:"arch"`
		Iterations uint32 `json:"iterations"`
		BadChars   string `json:"badChars"`
		DataB64    string `json:"data"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.DataB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "shellcode is not valid base64")
		return
	}
	out, err := c.ShellcodeEncode(req.Encoder, req.Arch, data, req.Iterations, []byte(req.BadChars))
	writeResult(w, map[string]any{
		"data":   base64.StdEncoding.EncodeToString(out),
		"length": len(out),
	}, err)
}

// ---------------------------------------------------------------------------
// WASM extensions
// ---------------------------------------------------------------------------

func (s *Server) handleWasmExtensions(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	names, err := c.WasmExtensions(id)
	writeResult(w, map[string]any{"extensions": names}, err)
}

func (s *Server) handleWasmRegister(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name    string `json:"name"`
		WasmB64 string `json:"wasm"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	wasm, err := base64.StdEncoding.DecodeString(req.WasmB64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "wasm is not valid base64")
		return
	}
	if err := c.RegisterWasmExtension(id, req.Name, wasm); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWasmExec(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Name string   `json:"name"`
		Args []string `json:"args"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	stdout, stderr, code, err := c.ExecWasmExtension(id, req.Name, req.Args)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stdout":   stdout,
		"stderr":   stderr,
		"exitCode": code,
	})
}

// ---------------------------------------------------------------------------
// Reverse port forwards
// ---------------------------------------------------------------------------
