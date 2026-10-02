package api

import (
	"net/http"

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
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.OneLinerRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := c.OneLiner(req)
	if err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleOneLinerTargets reports which listeners can serve a stage, so the UI can
// offer only those.
//
// It exists rather than letting the frontend filter on job name because "can this
// listener serve a file" is a property of the transport, and spelling that out in
// one place keeps the frontend from re-deriving it and getting it wrong -- an
// mTLS listener offered as a delivery target produces a command that cannot work.
func (s *Server) handleOneLinerTargets(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
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
