package api

import (
	"net/http"
	"strconv"

	"c2tool/internal/ui/sliver"
)

// --- Session / Beacon management ---

func (s *Server) handleRenameSession(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RenameSession(r.PathValue("id"), req.Name); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRenameBeacon(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.RenameBeacon(r.PathValue("id"), req.Name); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleRmBeacon(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.RmBeacon(r.PathValue("id")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleBeaconTasks(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	tasks, err := c.BeaconTasks(r.PathValue("id"))
	writeResult(w, map[string]any{"tasks": tasks}, err)
}

func (s *Server) handleBeacon(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	beacon, err := c.Beacon(r.PathValue("id"))
	if err != nil {
		// A beacon that does not exist is the caller's mistake, not a console
		// fault, and the server grades it as Internal -- so the status has to be
		// derived from the error rather than assumed.
		writeClientError(w, err)
		return
	}
	if beacon == nil {
		writeErr(w, http.StatusNotFound, "beacon not found")
		return
	}
	writeJSON(w, http.StatusOK, beacon)
}

func (s *Server) handleReconfigure(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		ReconnectInterval int64 `json:"reconnect_interval"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.ReconfigureSession(id, req.ReconnectInterval); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleOpenSession(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	async, err := c.OpenSessionFromBeacon(r.PathValue("id"))
	writeResult(w, map[string]any{"success": true, "async": async}, err)
}

func (s *Server) handleCloseSession(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	if err := c.CloseSession(id); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMonitorStart(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.MonitorStart(); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMonitorStop(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.MonitorStop(); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleBeaconTaskContent(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	task, err := c.BeaconTaskContent(r.PathValue("taskID"))
	writeResult(w, task, err)
}

// --- Implant profiles ---

func (s *Server) handleImplantProfiles(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	profiles, err := c.ImplantProfiles()
	writeResult(w, map[string]any{"profiles": profiles}, err)
}

func (s *Server) handleSaveImplantProfile(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Name     string                 `json:"name"`
		IsBeacon bool                   `json:"is_beacon"`
		Config   sliver.GenerateRequest `json:"config"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.SaveImplantProfile(req.Name, &req.Config, req.IsBeacon); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleDeleteImplantProfile(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.DeleteImplantProfile(r.PathValue("name")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleCompiler(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	compiler, err := c.CompilerInfo()
	writeResult(w, compiler, err)
}

// --- SOCKS5 proxies ---

func (s *Server) handleSocksList(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxies": c.Socks().List()})
}

func (s *Server) handleSocksStart(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		SessionID string `json:"session_id"`
		BindAddr  string `json:"bind_addr"`
		BindPort  uint32 `json:"bind_port"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	p, err := c.Socks().Start(req.SessionID, req.BindAddr, req.BindPort, req.Username, req.Password)
	writeResult(w, map[string]any{
		"success":  true,
		"id":       p.ID,
		"bindAddr": p.BindAddr,
		"bindPort": p.BindPort,
	}, err)
}

func (s *Server) handleSocksStop(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid proxy id")
		return
	}
	if err := c.Socks().Stop(id); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Loot ---

func (s *Server) handleLootAll(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var (
		loot []sliver.LootView
		err  error
	)
	if kind := r.URL.Query().Get("type"); kind != "" {
		loot, err = c.LootAllOf(kind)
	} else {
		loot, err = c.LootAll()
	}
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loot": loot})
}

func (s *Server) handleLootAdd(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.LootAddRequest
	if !decodeBody(w, r, &req) {
		return
	}
	id, err := c.LootAdd(&req)
	writeResult(w, map[string]any{"success": true, "id": id}, err)
}

func (s *Server) handleLootRename(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.LootRename(r.PathValue("id"), req.Name); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleLootContent(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing loot id")
		return
	}
	loot, err := c.LootContent(id)
	writeResult(w, loot, err)
}

func (s *Server) handleLootRemove(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing loot id")
		return
	}
	if err := c.LootRemove(id); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
