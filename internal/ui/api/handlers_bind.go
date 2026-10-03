package api

import (
	"net/http"
	"strconv"
)

// Forward (bind) listeners are the mirror image of the listeners on the
// /api/listeners routes: nothing binds a local port and the server dials out to
// a port the implant is listening on. They are kept on their own routes so the
// two directions cannot be confused in the UI, which matters because the
// failure modes look nothing alike — a reverse listener that shows no sessions
// means the implant has not called home, while a forward listener that shows no
// sessions means the dial is not reaching the target.

// handleBindList lists the running forward dialers.
//
// The list comes from the server's job table rather than from a dedicated RPC,
// so a dialer started by any client shows up here and stopping one is the same
// operation as stopping any other job.
func (s *Server) handleBindList(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}

	listeners, err := c.BindListeners()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listeners": listeners})
}

// handleBindStart starts a forward dialer.
//
// A 200 here means the dialer is running, not that a session exists. The
// implant may not have reached its listen path yet and the server retries until
// it does; the caller watches the session list for the result. Reporting the
// start rather than the outcome is deliberate — waiting for the outcome would
// hold the request open for an unbounded time on a target that is simply not
// listening yet.
func (s *Server) handleBindStart(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}

	var req struct {
		Host string `json:"host"`
		Port uint32 `json:"port"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	listener, err := c.DialBind(req.Host, req.Port)
	if err != nil {
		// A blanket 502 said "the upstream is broken" for every failure,
		// including "host is required" -- where the request is what is wrong and
		// nothing upstream was contacted. Deriving the status from the error
		// makes a validation problem a 400 while a real failure to reach the
		// target still reads as 502.
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listener)
}

// handleBindStop stops a forward dialer.
//
// This gives up on reaching the target. It does not disconnect a session that
// has already been established — that is the ordinary session controls' job,
// and conflating the two would make "stop retrying" close a working channel.
func (s *Server) handleBindStop(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}

	raw := r.PathValue("id")
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid job id")
		return
	}

	if err := c.StopBindListener(uint32(id)); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
