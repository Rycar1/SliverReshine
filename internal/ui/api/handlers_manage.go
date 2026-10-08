package api

import (
	"net/http"
	"strconv"

	"sliverreshine/internal/ui/sliver"
)

// --- Session / Beacon management ---

func (s *Server) handleRenameSession(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleRenameBeacon(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleRmBeacon(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.RmBeacon(r.PathValue("id")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleBeaconTasks(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	tasks, err := c.BeaconTasks(r.PathValue("id"))
	writeResult(w, map[string]any{"tasks": tasks}, err)
}

func (s *Server) handleBeacon(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleOpenSession(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleMonitorStart(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.MonitorStart(); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleMonitorStop(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.MonitorStop(); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleBeaconTaskContent(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	task, err := c.BeaconTaskContent(r.PathValue("taskID"))
	writeResult(w, task, err)
}

// --- Implant profiles ---

func (s *Server) handleImplantProfiles(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	profiles, err := c.ImplantProfiles()
	writeResult(w, map[string]any{"profiles": profiles}, err)
}

func (s *Server) handleSaveImplantProfile(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleDeleteImplantProfile(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.DeleteImplantProfile(r.PathValue("name")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleCompiler(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	compiler, err := c.CompilerInfo()
	writeResult(w, compiler, err)
}

// --- SOCKS5 proxies ---

func (s *Server) handleSocksList(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"proxies": c.Socks().List()})
}

func (s *Server) handleSocksStart(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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
	if err != nil {
		// Start returns a nil proxy on every failure path, so the fields below
		// must not be read until the error is ruled out -- reading them first
		// dereferenced nil and took the console down on a bad request.
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"id":       p.ID,
		"bindAddr": p.BindAddr,
		"bindPort": p.BindPort,
	})
}

func (s *Server) handleSocksStop(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleLootAll(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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
	loot, ok := paginate(w, r, loot)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"loot": loot})
}

func (s *Server) handleLootAdd(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req sliver.LootAddRequest
	if !decodeBody(w, r, &req) {
		return
	}
	id, err := c.LootAdd(&req)
	writeResult(w, map[string]any{"success": true, "id": id}, err)
}

func (s *Server) handleLootRename(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleLootContent(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "missing loot id")
		return
	}
	loot, err := c.LootContent(id)
	writeResult(w, loot, err)
}

func (s *Server) handleLootRemove(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
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
