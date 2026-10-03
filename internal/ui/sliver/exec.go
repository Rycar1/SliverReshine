package sliver

import (
	"fmt"
	"strings"
	"time"

	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// Why every Windows process spawn goes through ExecuteWindows
//
// Sliver exposes two execute RPCs and on Windows they are not interchangeable.
// `Execute` carries an ExecuteReq, which has no window-related field at all.
// `ExecuteWindows` carries an ExecuteWindowsReq, whose HideWindow field is the
// one the implant's Windows handler copies onto syscall.SysProcAttr
// (implant/sliver/handlers/handlers_windows.go: executeWindowsHandler). A
// process started through the plain RPC therefore inherits cmd.exe's default,
// which is a visible console window.
//
// That is not cosmetic. The persistence inventory runs one probe per module --
// reg query, sc query, schtasks /query -- so opening that tab used to flash one
// console window per probe across the target's desktop, and a mimikatz run
// announced itself the same way. Every spawn in this package funnels through
// execOn, which is what makes the console quiet on a Windows target.
//
// Sliver's own client makes the same distinction: it calls ExecuteWindows
// whenever it wants a window hidden (client/command/exec/execute.go).

// errUnknownMessageType is what the server reports when the implant did not
// understand the message it was sent. It is the signal that a target predates
// the ExecuteWindows RPC, and the only failure worth retrying down the plain
// path: every other error describes the command, not the transport.
const errUnknownMessageType = "unknown message type"

// sessionOS answers "which operating system is this session running?" and
// remembers the answer.
//
// A session ID belongs to one running implant for the whole of its life, so the
// answer cannot go stale -- a replacement implant registers under a new ID. One
// Sessions call fills the cache for every session at once, which keeps the
// lookup off the hot path: the persistence inventory starts a dozen probes and
// must not pay an extra round trip per probe to decide which RPC to use.
//
// The mutex is held across the fetch so that a cold cache under concurrent
// spawns costs one Sessions call rather than one per caller.
//
// The cache is rebuilt from the session list on every miss rather than merged
// into. Merging left an entry behind for every session that had ever been seen,
// so a long-running console accumulated one map entry per implant that had ever
// checked in -- the list only grows, and nothing ever removed a dead session.
// Replacing makes the map exactly the live set, which is the property the
// comment above assumes when it says the answer cannot go stale.
func (c *Client) sessionOS(sessionID string) (string, bool) {
	c.osMu.Lock()
	defer c.osMu.Unlock()

	if osName, ok := c.osCache[sessionID]; ok {
		return osName, true
	}

	sessions, err := c.Sessions()
	if err != nil {
		return "", false
	}
	// Rebuilt, not merged: the map ends up holding exactly the live sessions.
	live := make(map[string]string, len(sessions))
	for _, s := range sessions {
		live[s.ID] = strings.ToLower(s.OS)
	}
	c.osCache = live
	osName, ok := c.osCache[sessionID]
	return osName, ok
}

// execOn runs a process on the session and returns its output.
//
// osName is the caller's existing knowledge of the target platform; an empty
// value asks for it to be resolved. Callers that already know it -- the
// persistence layer is handed a platform on every entry point -- pass it
// straight through so no lookup happens at all.
//
// Windows sessions are served by the ExecuteWindows RPC with HideWindow set.
// Everything else uses the plain Execute RPC, because MsgExecuteWindowsReq has
// no handler outside a Windows implant: sending it to a Linux target comes back
// as an unknown message type instead of running anything.
// execOn runs a process on the session and returns its output decoded to UTF-8.
//
// It is the entry point every caller uses. The work is split from execRawOn so
// the code-page probe can spawn a process without recursing back into decoding:
// decoding needs the code page, and discovering the code page needs a spawn.
func (c *Client) execOn(sessionID, osName, path string, args []string, op time.Duration) (*ExecResult, error) {
	res, err := c.execRawOn(sessionID, osName, path, args, op)
	if err != nil {
		return nil, err
	}
	// Windows reports command output in the console's OEM code page, not UTF-8.
	// Decoding here rather than at each caller means every path -- persistence,
	// file operations, anything added later -- gets readable text, instead of
	// each one having to remember.
	if strings.Contains(strings.ToLower(c.osNameFor(sessionID, osName)), platformWindows) {
		res = c.decodeExecResult(sessionID, res)
	}
	return res, nil
}

// osNameFor resolves the platform the same way execRawOn does, without spawning
// anything, so execOn can decide whether to decode.
func (c *Client) osNameFor(sessionID, osName string) string {
	if osName != "" {
		return osName
	}
	if resolved, ok := c.sessionOS(sessionID); ok {
		return resolved
	}
	// Unknown platform: decode anyway. On a POSIX target decodeWindowsOutput is a
	// no-op, because a valid UTF-8 stream is returned unchanged and the code-page
	// probe is only reached for bytes that are not valid UTF-8 -- which a POSIX
	// command producing Latin-1 text can do, and which is exactly when the decode
	// is wanted too.
	return platformWindows
}

// execRawOn spawns without decoding. Callers should prefer execOn.
func (c *Client) execRawOn(sessionID, osName, path string, args []string, op time.Duration) (*ExecResult, error) {
	known := osName != ""
	if !known {
		if resolved, ok := c.sessionOS(sessionID); ok {
			osName = resolved
			known = true
		}
	}

	// A target positively identified as non-Windows has no
	// MsgExecuteWindowsReq handler, so the plain RPC is the only one that can run
	// anything there. The test is "does the OS string mention windows", not
	// equality: sliver reports the platform in different shapes across versions
	// ("windows", "Windows", and platform/arch forms all appear), and treating
	// an unrecognised string as "not Windows" is exactly how the console window
	// comes back.
	if known && !strings.Contains(strings.ToLower(osName), platformWindows) {
		return c.execPlain(sessionID, path, args, op)
	}

	// Windows -- and the case where the platform could not be resolved at all --
	// go through the window-hiding RPC first.
	//
	// Resolving the platform costs a Sessions call, and that call can fail or
	// return a session whose OS field is empty. Falling back to the plain RPC on
	// an unresolved platform is the wrong default: it is exactly the case where
	// the target might be Windows, and a Windows target spawned that way puts a
	// console window on the desktop. ExecuteWindows is harmless on a non-Windows
	// implant -- it answers with an unknown message type and nothing runs -- so
	// trying it first costs one extra round trip only when the platform is
	// genuinely unknown, and never shows a window that could have been hidden.
	res, err := c.execWindows(sessionID, path, args, op)
	if err == nil {
		return res, nil
	}
	if !strings.Contains(err.Error(), errUnknownMessageType) {
		return nil, err
	}
	// An implant older than the ExecuteWindows RPC. Retrying through the plain
	// path keeps such a target usable, at the cost of the window this whole path
	// exists to suppress.
	return c.execPlain(sessionID, path, args, op)
}

// execWindows starts the process with HideWindow set, so no console window
// appears on the target's desktop.
func (c *Client) execWindows(sessionID, path string, args []string, op time.Duration) (*ExecResult, error) {
	ctx, cancel := rpcCtx(op)
	defer cancel()

	resp, err := c.RPC.ExecuteWindows(ctx, &sliverpb.ExecuteWindowsReq{
		Path:       path,
		Args:       args,
		Output:     true,
		HideWindow: true,
		Request:    requestFor(sessionID, op),
	})
	if err != nil {
		return nil, err
	}
	return execResultFrom(resp)
}

// execPlain is the platform-neutral spawn. It is correct everywhere, but on
// Windows it cannot suppress the console window -- see the note at the top of
// this file.
func (c *Client) execPlain(sessionID, path string, args []string, op time.Duration) (*ExecResult, error) {
	ctx, cancel := rpcCtx(op)
	defer cancel()

	resp, err := c.RPC.Execute(ctx, &sliverpb.ExecuteReq{
		Path:    path,
		Args:    args,
		Output:  true,
		Request: requestFor(sessionID, op),
	})
	if err != nil {
		return nil, err
	}
	return execResultFrom(resp)
}

// execResultFrom flattens an Execute reply. An error the implant reported is
// returned as a Go error so callers do not each have to inspect Response.
func execResultFrom(resp *sliverpb.Execute) (*ExecResult, error) {
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	return &ExecResult{
		Status: resp.Status,
		Stdout: string(resp.Stdout),
		Stderr: string(resp.Stderr),
		PID:    resp.Pid,
	}, nil
}
