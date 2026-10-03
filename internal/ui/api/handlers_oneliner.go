package api

import (
	"net/http"
	"strconv"
	"strings"

	"c2tool/internal/ui/sliver"
)

// handleOneLiner turns a running listener into a one-line command that gets a
// session on a target.
//
// It is a POST because it has side effects: an implant is built, a profile is
// written and content is published. The listener itself is read, not changed --
// an operator who has already started one should not have it restarted by asking
// for a command.
// defaultDeliverySite is the website an HTTP listener serves when the operator
// did not name one.
//
// It is a constant shared with the one-liner code so the listener and the stage
// it serves cannot disagree -- which is what a 404 on a freshly generated
// one-liner turned out to be.
const defaultDeliverySite = "webdelivery"

func (s *Server) handleOneLiner(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	var req sliver.OneLinerRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.OneLiner(req)
	writeResult(w, res, err)
}

// handleOneLinerTargets reports which listeners can serve a stage, so the UI can
// offer only those.
//
// It exists rather than letting the frontend filter on job name because "can this
// listener serve a file" is a property of the transport, and spelling that out in
// one place keeps the frontend from re-deriving it and getting it wrong -- an
// mTLS listener offered as a delivery target produces a command that cannot work.
func (s *Server) handleOneLinerTargets(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}
	jobs, err := c.Jobs()
	if err != nil {
		writeClientError(w, err)
		return
	}
	targets := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		targets = append(targets, map[string]any{
			"job_id":    jobs[i].ID,
			"name":      jobs[i].Name,
			"port":      jobs[i].Port,
			"domains":   jobs[i].Domains,
			"can_stage": sliver.JobServesStage(jobs[i].Name),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": targets})
}

// handleOneLinerAll builds a stage for several platforms in one request.
//
// The per-listener button needs the Windows and the Linux command together, and
// that is two implant builds. Asking for them one at a time from the browser
// would mean two round trips, two sequential build waits, and a half-populated
// dialog in between -- and if the second failed the operator would be left
// looking at a Windows command with no indication that Linux was still coming or
// had already failed.
//
// Platforms are taken from the body rather than hardcoded, but default to
// Windows and Linux, which is what the button offers.
func (s *Server) handleOneLinerAll(w http.ResponseWriter, r *http.Request) {
	c := s.clientFor(w, r)
	if c == nil {
		return
	}

	var req struct {
		sliver.OneLinerRequest
		Platforms []string `json:"platforms"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.JobID == 0 {
		writeErr(w, http.StatusBadRequest, "job_id is required")
		return
	}

	// An unknown platform name is refused before any build starts, so a typo
	// does not cost two implant builds and then report nothing usable.
	var platforms []sliver.OneLinerPlatform
	for _, p := range req.Platforms {
		candidate := sliver.OneLinerPlatform(strings.ToLower(strings.TrimSpace(p)))
		switch candidate {
		case sliver.OneLinerWindows, sliver.OneLinerLinux, sliver.OneLinerDarwin:
			platforms = append(platforms, candidate)
		default:
			writeErr(w, http.StatusBadRequest, "unsupported platform "+strconv.Quote(p))
			return
		}
	}

	results := c.OneLinerAll(req.OneLinerRequest, platforms)

	// Partial success is the normal failure shape here, so the response is always
	// 200 with per-platform errors: the frontend renders whatever succeeded and
	// shows why the rest did not. Failing the whole request would hide a working
	// command behind a non-2xx.
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
