package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"sliverreshine/internal/ai"
	"sliverreshine/internal/ui/sliver"
)

// The event-stream transport for the model-backed runs.
//
// A collection or escalation run takes minutes and dozens of commands. Waiting
// for the final JSON means the operator watches a spinner with no way to tell a
// working run from a stuck one, so the runs publish their progress as it
// happens and this file carries it to the browser.
//
// The stream is Server-Sent Events rather than a WebSocket: the traffic is
// one-directional, it survives proxies that mangle upgrades, and it needs no
// new route in the CSRF exemption list -- the request is a POST with a JSON
// body like any other mutating call, only the response framing differs.

// wantsEventStream reports whether the caller asked for a stream.
//
// The check is on Accept rather than on a body field so a client that only
// wants the result -- a script, a test -- keeps the plain JSON response by
// saying nothing.
func wantsEventStream(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

// eventStream writes AIEvent values as SSE frames.
//
// It flushes after every frame. Without the flush the events would sit in the
// server's write buffer until enough of them accumulated, which is the same as
// not streaming at all for a run that emits one event per model turn.
type eventStream struct {
	w http.ResponseWriter
	f http.Flusher
}

// newEventStream takes over the response and returns the writer.
//
// It fails when the writer cannot be flushed, which means the deployment sits
// behind something that buffers whole responses. Reporting that is better than
// silently degrading to a stream that arrives all at once at the end.
func newEventStream(w http.ResponseWriter) (*eventStream, error) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("the response writer does not support streaming")
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// Tells a reverse proxy not to buffer the stream, which would undo the
	// flush below.
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	f.Flush()
	return &eventStream{w: w, f: f}, nil
}

// send writes one event. A write failure means the browser is gone; the run
// stops on its own because the request context is cancelled with it, so there
// is nothing useful to do here but stop writing.
func (s *eventStream) send(ev sliver.AIEvent) {
	blob, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if _, err := fmt.Fprintf(s.w, "data: %s\n\n", blob); err != nil {
		return
	}
	s.f.Flush()
}

// sendResult ends the stream with the run's final value.
func (s *eventStream) sendResult(result any) {
	s.send(sliver.AIEvent{Type: sliver.AIEventDone, Result: result})
}

// sendError ends the stream with a message instead of a result. The status is
// already 200 by the time this can happen, so the failure has to travel as an
// event rather than as an HTTP status.
func (s *eventStream) sendError(err error) {
	s.send(sliver.AIEvent{Type: sliver.AIEventDone, Text: err.Error()})
}

// handleAIPrivesc runs one escalation attempt over a session, streaming the
// model's reasoning and every command as they happen.
//
// The session ID comes from the path, so a body cannot point the run at a
// different target than the one the operator selected. Everything that can be
// answered with a status is validated before the stream starts, because once
// the first frame is written the status code is fixed.
//
// Read-only policy: this handler does not stamp the deployment policy onto the
// request. Escalation is not a read-only activity -- a run that cannot change
// state cannot escalate -- so the feature defaults to the full shell, and the
// caller opts into the allowlist by asking for it. The console's own
// enumeration step is its own tooling, not a model-proposed command.
func (s *Server) handleAIPrivesc(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req sliver.AIPrivescRequest
	if !decodeBody(w, r, &req) {
		return
	}
	req.SessionID = id

	provider := s.aiService().Provider()
	if provider == nil {
		writeErr(w, http.StatusNotImplemented, ai.ErrNotConfigured.Error())
		return
	}

	stream, err := newEventStream(w)
	if err != nil {
		writeErr(w, http.StatusNotImplemented, err.Error())
		return
	}
	req.Progress = stream.send

	result, runErr := c.AIPrivesc(r.Context(), provider, req)
	if runErr != nil {
		stream.sendError(runErr)
		return
	}
	stream.sendResult(result)
}
