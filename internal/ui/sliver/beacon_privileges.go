package sliver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/protobuf/proto"
)

// This file lets the credential harvest reach a beacon.
//
// It would not, before. escalateForMimikatz asked for the session's token
// integrity and gave up silently when that failed, and the failure was
// guaranteed on a beacon: GetPrivs resolves its target through
// core.Sessions.Get(SessionID), which only knows about interactive sessions. A
// beacon ID is not in that table, so the call returned InvalidSessionID every
// time. The operator then got mimikatz's own "LSA access was denied" with no
// mention of the step that had been skipped, which reads as "your token is not
// elevated" and sends them looking at the token.
//
// The fix is two things, and both matter:
//
//  1. Ask with the beacon's own ID and the async flag, which is how every other
//     beacon operation in this package works. Sliver queues the request and the
//     beacon answers on its next check-in.
//
//  2. When a step cannot be taken, say so. A silent "" is what turned a missing
//     feature into a misleading diagnosis.

// beaconRequest builds the async request envelope for a beacon.
//
// Async is what tells the server to queue this rather than look for a live
// session, and BeaconID is what it queues against. Sending only Async would
// leave the server guessing.
func beaconRequest(beaconID string, timeout time.Duration) *commonpb.Request {
	deadline := timeout - time.Second
	if deadline < 0 {
		deadline = timeout
	}
	return &commonpb.Request{
		BeaconID: beaconID,
		Async:    true,
		Timeout:  int64(deadline),
	}
}

// BeaconIntegrity reads a beacon's token integrity level.
//
// The correlation is by TaskID, and that is the whole correctness of this
// function. Three earlier ways of identifying "the answer to my question" were
// each wrong on a real beacon:
//
//	by Response on the task list   - the list RPC maps every row through
//	                                 models.BeaconTask.ToProtobuf(false), which
//	                                 omits Request and Response by design, so the
//	                                 field is empty on every poll.
//	by Description == "GetPrivs"   - the server sets Description to the protobuf
//	                                 message name, so it is "GetPrivsReq".
//	by "the newest completed task" - a heuristic, and one a second queued task or
//	                                 a second operator can satisfy.
//
// GetPrivs answers with Response.TaskID, set by the server's asyncGenericHandler,
// which is exactly the task this call queued. Using it removes the matching
// question rather than answering it: a previous run's task has a different ID,
// so a stale result is not something that can be picked up.
func (c *Client) BeaconIntegrity(beaconID string, wait time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	resp, err := c.RPC.GetPrivs(ctx, &sliverpb.GetPrivsReq{
		Request: beaconRequest(beaconID, wait),
	})
	if err != nil {
		return "", fmt.Errorf("queue GetPrivs for beacon: %w", err)
	}

	taskID := resp.GetResponse().GetTaskID()
	if taskID == "" {
		// No task to poll. Reporting a timeout here would misdescribe it: nothing
		// was queued, so waiting would be pure delay.
		return "", fmt.Errorf("the server queued GetPrivs for beacon %s but returned no task ID, "+
			"so there is nothing to wait for", beaconID)
	}

	content, err := c.waitForBeaconTask(ctx, taskID, wait)
	if err != nil {
		return "", err
	}
	// The answer is a marshalled sliverpb.GetPrivs, not a bare string. Reading
	// its ProcessIntegrity field is what makes the value usable: the raw bytes
	// never equal "High", so treating them as text silently reports unelevated
	// for a token that is elevated.
	raw, err := beaconTaskResponseBytes(content)
	if err != nil {
		return "", err
	}
	privs := &sliverpb.GetPrivs{}
	if err := proto.Unmarshal(raw, privs); err != nil {
		return "", fmt.Errorf("the beacon answered GetPrivs but its response could not be read: %w", err)
	}
	// The implant reports its own failures here. A task that completed with an
	// error still counts as COMPLETED server-side, so without this check the
	// caller would read an empty integrity and conclude "not elevated" for a
	// question the target answered with "I could not tell you".
	if errMsg := strings.TrimSpace(privs.GetResponse().GetErr()); errMsg != "" {
		return "", fmt.Errorf("the beacon could not read its token: %s", errMsg)
	}
	return strings.TrimSpace(privs.ProcessIntegrity), nil
}

// waitForBeaconTask polls one task until it completes and returns its content.
//
// Polling a known ID, rather than scanning a list for something that looks
// right, is what makes another caller's task impossible to mistake for this one.
func (c *Client) waitForBeaconTask(ctx context.Context, taskID string, wait time.Duration) (*BeaconTaskView, error) {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("the beacon did not answer within %s", wait)
		case <-time.After(beaconPollInterval):
		}

		content, err := c.BeaconTaskContent(taskID)
		if err != nil {
			// A transient read failure is not the answer; the beacon may still be
			// working. Only the deadline is conclusive.
			continue
		}
		if !strings.EqualFold(content.State, "completed") {
			continue
		}
		// The caller decodes the payload and reads the implant's error from the
		// typed wrapper. Doing it here would mean parsing the bytes without
		// knowing which message they are, and a protobuf field number means
		// different things in different messages -- field 1 of GetPrivs is the
		// embedded Response, while field 1 of Response is the error string, so a
		// wrong guess silently reads the wrong field.
		return content, nil
	}
	return nil, fmt.Errorf("the beacon did not answer within %s", wait)
}

// beaconTaskResponseBytes returns a task's raw response payload.
//
// The view carries it base64-encoded because the console serves these as JSON,
// so the decode is the caller's step. Every consumer needs the decoded bytes:
// the response is a marshalled protobuf, and a protobuf cannot be parsed from
// its base64 text.
func beaconTaskResponseBytes(v *BeaconTaskView) ([]byte, error) {
	if v == nil || v.ResponseB64 == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(v.ResponseB64)
	if err != nil {
		return nil, fmt.Errorf("the beacon's response was not valid base64: %w", err)
	}
	return raw, nil
}

// beaconPollInterval is how often the async helpers re-read a task.
const beaconPollInterval = 2 * time.Second

// errBeaconElevationUnsupported explains why beacon-to-SYSTEM escalation is not
// attempted.
//
// This is a server-side limitation, not a gap in this client, and it is worth
// stating precisely because the symptom otherwise looks like a client bug.
// GetSystem reads its target with
//
//	session := core.Sessions.Get(req.Request.SessionID)
//
// unconditionally -- there is no async/beacon branch. GetPrivs, by contrast,
// routes through GenericHandler and only touches the session table when
// req.Request.Async is false. So a request carrying a BeaconID and no SessionID
// is answered with ErrInvalidSessionID ("Invalid session ID"), every time.
// asyncGenericHandler is never reached for GetSystem.
//
// Rather than spend the operator's wait on a call that cannot succeed, the
// harvest reports the situation and the way out. The message names the remedy,
// because "unsupported" on its own is not actionable.
var errBeaconElevationUnsupported = errors.New(
	"a beacon's token cannot be escalated to SYSTEM: the server's GetSystem accepts only an " +
		"interactive session and has no beacon path. Convert the beacon to a session (the " +
		"console's beacon view has that action) and run the harvest there, or run getsystem " +
		"on an existing session and harvest against it")

// BeaconElevateToSystem reports that beacon-side escalation is unavailable.
//
// It keeps its signature so callers do not have to special-case the beacon, and
// so the reason reaches the operator through the same channel as every other
// escalation failure. It does not attempt the RPC: a call that is certain to be
// refused, after a wait, teaches the operator less than the sentence below.
func (c *Client) BeaconElevateToSystem(_, _ string, _ time.Duration) (string, error) {
	return "", errBeaconElevationUnsupported
}

// errNoIntegritySource is returned when neither a session nor a beacon ID was
// supplied, which means the caller lost the target rather than that the target
// refused.
var errNoIntegritySource = errors.New("no session or beacon to read the token integrity from")
