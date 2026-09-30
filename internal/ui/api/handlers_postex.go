package api

import (
	"encoding/base64"
	"net/http"
	"strconv"

	"github.com/bishopfox/sliver/protobuf/clientpb"

	"c2tool/internal/ui/sliver"
)

// ---------------------------------------------------------------------------
// Credential vault
// ---------------------------------------------------------------------------

func (s *Server) handleCreds(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	creds, err := c.Creds()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

func (s *Server) handleCredsAdd(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Credentials []sliver.CredentialView `json:"credentials"`
		// Convenience form for a single entry typed straight into the UI.
		Username   string `json:"username"`
		Plaintext  string `json:"plaintext"`
		Hash       string `json:"hash"`
		HashType   int32  `json:"hashType"`
		Collection string `json:"collection"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	batch := req.Credentials
	if len(batch) == 0 {
		batch = []sliver.CredentialView{{
			Username:   req.Username,
			Plaintext:  req.Plaintext,
			Hash:       req.Hash,
			HashType:   req.HashType,
			Collection: req.Collection,
		}}
	}
	if err := c.CredsAdd(batch); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": len(batch)})
}

func (s *Server) handleCredsRemove(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
		ID  string   `json:"id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ids := req.IDs
	if len(ids) == 0 && req.ID != "" {
		ids = []string{req.ID}
	}
	if err := c.CredsRm(ids); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCredsUpdate(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Credentials []sliver.CredentialView `json:"credentials"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.CredsUpdate(req.Credentials); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCredByID(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	cred, err := c.GetCredByID(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cred)
}

func (s *Server) handleCredsSniff(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Hash string `json:"hash"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	cred, err := c.CredsSniffHashType(req.Hash)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cred)
}

// handleCredsByHashType serves both filtered variants: ?plaintext=1 narrows to
// entries whose plaintext is already recovered.
func (s *Server) handleCredsByHashType(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	hashType := int32(0)
	if v := r.URL.Query().Get("type"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid hash type")
			return
		}
		hashType = int32(n)
	}
	var (
		creds []sliver.CredentialView
		err   error
	)
	if r.URL.Query().Get("plaintext") == "1" {
		creds, err = c.GetPlaintextCredsByHashType(hashType)
	} else {
		creds, err = c.GetCredsByHashType(hashType)
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

// ---------------------------------------------------------------------------
// Memfiles
// ---------------------------------------------------------------------------

func (s *Server) handleMemfilesList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	dir, err := c.MemfilesList(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dir)
}

func (s *Server) handleMemfilesAdd(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	fd, err := c.MemfilesAdd(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fd": fd})
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		Path      string `json:"path"`
		UID       string `json:"uid"`
		GID       string `json:"gid"`
		Recursive bool   `json:"recursive"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Chown(id, req.Path, req.UID, req.GID, req.Recursive); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChtimes timestomps a file. Operators use this to restore the original
// timestamps of files they touched, so the change does not show up in a timeline.
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------------------------------------------------------------------------
// Monitoring providers
// ---------------------------------------------------------------------------

func (s *Server) handleMonitorProviders(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	providers, err := c.MonitorListConfig()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) handleMonitorAdd(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.MonitorProviderView
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MonitorAddConfig(req); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMonitorRemove(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.MonitorProviderView
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MonitorDelConfig(req); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// C2 profiles
// ---------------------------------------------------------------------------

func (s *Server) handleC2Profiles(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	profiles, err := c.HTTPC2Profiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) handleC2Profile(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	cfg, err := c.HTTPC2Profile(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleC2ProfileSave(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Traffic encoders
// ---------------------------------------------------------------------------

func (s *Server) handleTrafficEncoders(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	names, err := c.TrafficEncoders()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"encoders": names})
}

func (s *Server) handleTrafficEncoderAdd(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleTrafficEncoderRemove(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.TrafficEncoderRm(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Shellcode encoders
// ---------------------------------------------------------------------------

func (s *Server) handleShellcodeEncoders(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	encoders, err := c.ShellcodeEncoders()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"encoders": encoders})
}

func (s *Server) handleShellcodeEncode(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":   base64.StdEncoding.EncodeToString(out),
		"length": len(out),
	})
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"extensions": names})
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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

func (s *Server) handleRportFwdList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	listeners, err := c.RportFwdListeners(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listeners": listeners})
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, l)
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Certificates
// ---------------------------------------------------------------------------

func (s *Server) handleCACertificates(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	certs, err := c.CertificateAuthority()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificates": certs})
}

func (s *Server) handleCertificates(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"certificates": certs})
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tunnelID": tunnelID})
}

func (s *Server) handleTunnelClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TunnelID  uint64 `json:"tunnelID"`
		SessionID string `json:"sessionID"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.CloseTunnel(req.TunnelID, req.SessionID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Windows services (detail / start by name)
// ---------------------------------------------------------------------------

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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// Registry hive extraction
// ---------------------------------------------------------------------------

// handleRegistryHive dumps a hive and streams it back as a download. The bytes
// are raw, so the response is a binary attachment rather than JSON.
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The implant may compress or base64 the hive depending on platform; report
	// the encoder in a header so the caller knows how to decode what it received.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Hive-Encoder", encoder)
	w.Header().Set("Content-Disposition", `attachment; filename="`+req.RequestedHive+`.hive"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
