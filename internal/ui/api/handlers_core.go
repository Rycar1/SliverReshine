package api

import (
	"net/http"
	"strconv"

	"sliverreshine/internal/ui/sliver"
)

func (s *Server) handleSessions(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	sessions, err := c.Sessions()
	writeResult(w, map[string]any{"sessions": sessions}, err)
}

func (s *Server) handleBeacons(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	beacons, err := c.Beacons()
	writeResult(w, map[string]any{"beacons": beacons}, err)
}

func (s *Server) handleJobs(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	jobs, err := c.Jobs()
	writeResult(w, map[string]any{"jobs": jobs}, err)
}

func (s *Server) handleEvents(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	events, err := c.Events()
	if err != nil {
		writeClientError(w, err)
		return
	}
	events, ok := paginate(w, r, events)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleKillSession(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if err := c.KillSession(id); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleBuilders(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	builds, err := c.ImplantBuilds()
	writeResult(w, map[string]any{"builders": builds}, err)
}

func (s *Server) handleGenerate(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req sliver.GenerateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := c.GenerateImplant(&req)
	writeResult(w, result, err)
}

func (s *Server) handleListeners(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Addr string `json:"addr"`
		Port int    `json:"port"`
		TLS  bool   `json:"tls"`
		// Website lets an HTTP listener serve files published by WebDelivery.
		// It was not accepted here at all, so the field was silently dropped:
		// the listener started, reported success, and answered 404 for every
		// published path.
		Website string `json:"website"`
		// Domain is what the implant's callback URIs are built from.
		Domain string `json:"domain"`
		// CallbackHost is the address a payload built for this listener should
		// call back to, when that is not the bind address -- a bind of 0.0.0.0
		// behind NAT, or a public name. Empty means "use the bind address".
		CallbackHost string `json:"callback_host"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	addr := req.Addr
	if addr == "" {
		addr = "0.0.0.0"
	}
	port := uint32(req.Port)
	if port == 0 {
		switch req.Type {
		case "mtls":
			port = 8888
		case "dns":
			port = 53
		case "wireguard":
			// 51820 is the WireGuard convention. This said 53, which is the DNS
			// port copied from the case above -- so a caller that omitted the
			// port got a wireguard listener on the DNS port.
			port = 51820
		default:
			port = 80
		}
	}
	// An HTTP listener needs a website to serve staged content. Defaulting it
	// here rather than requiring the operator to know the concept means
	// "start a listener, then ask for a one-liner" produces matching names
	// without either step having to know about the other.
	website := req.Website
	if website == "" && (req.Type == "http" || req.Type == "https") {
		website = defaultDeliverySite
	}
	jobID, err := c.StartListener(req.Type, addr, port, req.TLS, website, req.Domain, req.CallbackHost)
	writeResult(w, map[string]any{"success": true, "job_id": jobID}, err)
}

func (s *Server) handleStopListener(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid job id")
		return
	}
	if err := c.StopJob(uint32(id)); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
