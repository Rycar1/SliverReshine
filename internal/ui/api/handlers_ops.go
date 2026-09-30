package api

import (
	"c2tool/internal/ui/sliver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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


func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
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
	data, name, err := c.Download(id, path)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"Data": data, "Name": name})
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
