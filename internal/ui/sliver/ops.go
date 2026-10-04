package sliver

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

func encodeBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

// IfaceView is the JSON shape for a network interface.
type IfaceView struct {
	Index       int32    `json:"Index"`
	Name        string   `json:"Name"`
	MAC         string   `json:"MAC"`
	IPAddresses []string `json:"IPAddresses"`
}

// Ifconfig returns network interfaces of the session.
func (c *Client) Ifconfig(sessionID string) ([]IfaceView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Ifconfig(ctx, &sliverpb.IfconfigReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	out := make([]IfaceView, 0, len(resp.NetInterfaces))
	for _, ni := range resp.NetInterfaces {
		if ni == nil {
			continue
		}
		ips := ni.IPAddresses
		if ips == nil {
			ips = []string{}
		}
		out = append(out, IfaceView{
			Index:       ni.Index,
			Name:        ni.Name,
			MAC:         ni.MAC,
			IPAddresses: ips,
		})
	}
	return out, nil
}

// ProcessView is the JSON shape for a process.
type ProcessView struct {
	PID        int32    `json:"PID"`
	PPID       int32    `json:"PPID"`
	Executable string   `json:"Executable"`
	Owner      string   `json:"Owner"`
	SessionID  int32    `json:"SessionID"`
	CmdLine    []string `json:"CmdLine"`
}

// Ps lists processes running on the session.
func (c *Client) Ps(sessionID string) ([]ProcessView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Ps(ctx, &sliverpb.PsReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	out := make([]ProcessView, 0, len(resp.Processes))
	for _, p := range resp.Processes {
		if p == nil {
			continue
		}
		cmd := p.CmdLine
		if cmd == nil {
			cmd = []string{}
		}
		out = append(out, ProcessView{
			PID:        p.Pid,
			PPID:       p.Ppid,
			Executable: p.Executable,
			Owner:      p.Owner,
			SessionID:  p.SessionID,
			CmdLine:    cmd,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// KillProcess terminates a process on the session.
func (c *Client) KillProcess(sessionID string, pid int32, force bool) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Terminate(ctx, &sliverpb.TerminateReq{
		Pid:   pid,
		Force: force,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// SockEntryView is the JSON shape for a netstat entry.
type SockEntryView struct {
	Protocol    string `json:"Protocol"`
	LocalAddr   string `json:"LocalAddr"`
	LocalPort   uint32 `json:"LocalPort"`
	RemoteAddr  string `json:"RemoteAddr"`
	RemotePort  uint32 `json:"RemotePort"`
	State       string `json:"State"`
	UID         uint32 `json:"UID"`
	ProcessName string `json:"ProcessName"`
}

// Netstat lists open network connections on the session.
func (c *Client) Netstat(sessionID string) ([]SockEntryView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.Netstat(ctx, &sliverpb.NetstatReq{
		TCP: true,
		UDP: true,
		IP4: true,
		IP6: true,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	out := make([]SockEntryView, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		if e == nil {
			continue
		}
		entry := SockEntryView{
			Protocol: e.Protocol,
			State:    e.SkState,
			UID:      e.UID,
		}
		if e.LocalAddr != nil {
			entry.LocalAddr = e.LocalAddr.Ip
			entry.LocalPort = e.LocalAddr.Port
		}
		if e.RemoteAddr != nil {
			entry.RemoteAddr = e.RemoteAddr.Ip
			entry.RemotePort = e.RemoteAddr.Port
		}
		if e.Process != nil {
			entry.ProcessName = e.Process.Executable
		}
		out = append(out, entry)
	}
	return out, nil
}

// validUTF8 replaces bytes that are not valid UTF-8 with U+FFFD.
//
// An implant reads environment variables out of its own process block, where a
// "string" is whatever bytes happen to be there: a Windows variable in the OEM
// code page, or one set from a non-UTF-8 source, is not necessarily UTF-8.
// Protobuf requires string fields to be valid UTF-8, so the implant's marshaller
// fails the whole response with "string field contains invalid UTF-8" and the
// operator gets that error instead of their environment. The implant now
// sanitizes before sending; this is the console's half of the same guard, so an
// implant built before that fix still produces a readable list rather than a
// protobuf error.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}

// EnvView is the JSON shape for an environment variable.
type EnvView struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

// GetEnv lists environment variables of the session.
func (c *Client) GetEnv(sessionID string) ([]EnvView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetEnv(ctx, &sliverpb.EnvReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return nil, fmt.Errorf("%s", resp.Response.Err)
	}
	out := make([]EnvView, 0, len(resp.Variables))
	for _, v := range resp.Variables {
		if v == nil {
			continue
		}
		out = append(out, EnvView{Key: validUTF8(v.Key), Value: validUTF8(v.Value)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SetEnv sets an environment variable on the session.
func (c *Client) SetEnv(sessionID, key, value string) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.SetEnv(ctx, &sliverpb.SetEnvReq{
		Variable: &commonpb.EnvVar{Key: key, Value: value},
		Request:  &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// UnsetEnv removes an environment variable on the session.
func (c *Client) UnsetEnv(sessionID, key string) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.UnsetEnv(ctx, &sliverpb.UnsetEnvReq{
		Name:    key,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return fmt.Errorf("%s", resp.Response.Err)
	}
	return nil
}

// ExecResult is the JSON shape of an execute result.
type ExecResult struct {
	Status uint32 `json:"Status"`
	Stdout string `json:"Stdout"`
	Stderr string `json:"Stderr"`
	PID    uint32 `json:"PID"`
}

// Execute runs a binary on the session and captures output.
//
// The spawn goes through execOn, so a Windows target starts the process with
// its console window hidden. That is the point of the indirection: the plain
// Execute RPC has nowhere to carry the flag, and a visible console window on
// the target's desktop is what gives an operator away.
func (c *Client) Execute(sessionID, path string, args []string) (*ExecResult, error) {
	return c.execOn(sessionID, "", path, args, execDefaultTimeout)
}

// Screenshot takes a screenshot on the session and returns base64 PNG data.
func (c *Client) Screenshot(sessionID string) (string, error) {
	ctx, cancel := c.rpcCtx(rpcLong)
	defer cancel()
	resp, err := c.RPC.Screenshot(ctx, &sliverpb.ScreenshotReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return "", err
	}
	if resp.Response != nil && resp.Response.Err != "" {
		return "", fmt.Errorf("%s", resp.Response.Err)
	}
	// The Linux and Darwin implants implement Screenshot as a best-effort
	// X11/Quartz capture: on a headless target it returns no bytes at all and
	// the RPC still reports success. Returning that as a 200 with an empty
	// "Data" made the console show a blank image with no explanation -- the
	// operator could not tell "the target has no desktop" from "the console
	// lost the image". An empty capture is stated as such instead.
	if len(resp.Data) == 0 {
		return "", errors.New("the target returned an empty screenshot: it has no " +
			"active display (a headless host or a session with no graphical desktop), " +
			"so there is nothing to capture")
	}
	return encodeBase64(resp.Data), nil
}
