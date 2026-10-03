package api

import (
	"encoding/json"
	"net/http"
	"time"

	"c2tool/internal/ui/sliver"
)

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	c := s.Client()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	c = c.WithRequestContext(r.Context())
	ver, err := c.Version()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "version": ver})
}

// handleOverview aggregates top-level counts for the sidebar badges and dashboard.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	type countResult struct {
		key string
		n   int
		err error
	}
	results := make(chan countResult, 8)
	run := func(key string, fn func() (int, error)) {
		go func() {
			n, err := fn()
			results <- countResult{key: key, n: n, err: err}
		}()
	}

	run("sessions", func() (int, error) {
		ss, err := c.Sessions()
		return len(ss), err
	})
	run("beacons", func() (int, error) {
		bs, err := c.Beacons()
		return len(bs), err
	})
	run("jobs", func() (int, error) {
		js, err := c.Jobs()
		return len(js), err
	})
	run("builders", func() (int, error) {
		bs, err := c.ImplantBuilds()
		return len(bs), err
	})
	run("socks", func() (int, error) {
		return len(c.Socks().List()), nil
	})

	out := map[string]int{"sessions": 0, "beacons": 0, "jobs": 0, "builders": 0, "socks": 0}
	timeout := time.After(12 * time.Second)
	for i := 0; i < 5; i++ {
		select {
		case res := <-results:
			if res.err == nil {
				out[res.key] = res.n
			}
		case <-timeout:
			writeErr(w, http.StatusGatewayTimeout, "overview collection timed out")
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": out})
}

// connectRequest carries the raw sliver-client profile config loaded from a
// file by the UI. Connection now depends entirely on this config file rather
// than manually supplied connection parameters.
type connectRequest struct {
	Content string `json:"content"`
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	var req connectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Content == "" {
		writeErr(w, http.StatusBadRequest, "missing config file content")
		return
	}
	cfg, err := sliver.ParseProfile([]byte(req.Content))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	client, err := sliver.Connect(cfg)
	if err != nil {
		writeClientError(w, err)
		return
	}
	s.SetClient(client)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.ClearClient()
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"profiles": sliver.ListProfiles()})
}

func (s *Server) handleUseProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "invalid profile name")
		return
	}
	cfg, err := sliver.LoadProfile(name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	client, err := sliver.Connect(cfg)
	if err != nil {
		writeClientError(w, err)
		return
	}
	s.SetClient(client)
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
