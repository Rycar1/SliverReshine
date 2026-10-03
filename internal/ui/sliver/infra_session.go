package sliver

import (
	"errors"
	"net"
	"sort"
	"strconv"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// ---------------------------------------------------------------------------
// WASM extensions
//
// Sliver supports two extension flavours: BOF/COFF (already exposed) and WASM.
// WASM extensions are self-contained and target-independent, which makes them
// the more reusable of the two.
// ---------------------------------------------------------------------------

// WasmExtensions lists registered WASM extension names for a session.
func (c *Client) WasmExtensions(sessionID string) ([]string, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.ListWasmExtensions(ctx, &sliverpb.ListWasmExtensionsReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	names := resp.Names
	if names == nil {
		names = []string{}
	}
	sort.Strings(names)
	return names, nil
}

// RegisterWasmExtension installs a WASM extension into a session.
func (c *Client) RegisterWasmExtension(sessionID, name string, wasmGz []byte) error {
	if name == "" {
		return errors.New("extension name is required")
	}
	if len(wasmGz) == 0 {
		return errors.New("extension payload is empty")
	}
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.RegisterWasmExtension(ctx, &sliverpb.RegisterWasmExtensionReq{
		Name:    name,
		WasmGz:  wasmGz,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// ExecWasmExtension runs a registered WASM extension with arguments.
func (c *Client) ExecWasmExtension(sessionID, name string, args []string) (string, string, uint32, error) {
	ctx, cancel := c.rpcCtx(5 * opTimeout)
	defer cancel()
	req := &sliverpb.ExecWasmExtensionReq{
		Name:    name,
		Args:    args,
		Request: &commonpb.Request{SessionID: sessionID},
	}
	resp, err := c.RPC.ExecWasmExtension(ctx, req)
	if err != nil {
		return "", "", 0, err
	}
	if resp.GetResponse().GetErr() != "" {
		return "", "", 0, errors.New(resp.GetResponse().GetErr())
	}
	return string(resp.Stdout), string(resp.Stderr), resp.ExitCode, nil
}

// ---------------------------------------------------------------------------
// Reverse port forward listeners
//
// A reverse port forward listener binds on the *server* and forwards inbound
// connections down to the target. This is how an operator reaches a service
// that only listens on the target's loopback.
// ---------------------------------------------------------------------------

// RportFwdListenerView is the JSON shape of a reverse port forward listener.
//
// Sliver's RPC carries both endpoints as host:port strings. The numeric
// BindPort/ForwardPort fields on the wire are unreliable: the implant's start
// handler echoes req.ForwardPort into both, and its list handler omits them
// entirely (the TUI never reads them). The view therefore splits the address
// strings so the console shows the values that were actually requested.
type RportFwdListenerView struct {
	ID             uint32 `json:"ID"`
	BindAddress    string `json:"BindAddress"`
	BindPort       uint32 `json:"BindPort"`
	ForwardAddress string `json:"ForwardAddress"`
	ForwardPort    uint32 `json:"ForwardPort"`
}

// joinAddr builds the host:port form the implant expects. An empty host means
// "all interfaces on the target", matching the TUI's ":port" shorthand.
func joinAddr(host string, port uint32) string {
	if port == 0 {
		return host
	}
	return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}

// splitAddr is the inverse of joinAddr, tolerating a missing host.
func splitAddr(addr string) (string, uint32) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, 0
	}
	n, err := strconv.ParseUint(portStr, 10, 32)
	if err != nil {
		return host, 0
	}
	return host, uint32(n)
}

// StartRportFwdListener creates a reverse port forward listener. The bind side
// is opened by the implant on the target; connections arriving there are
// tunnelled back and dialled from the server, so the forward side must be
// reachable from the operator's machine rather than from the target.
func (c *Client) StartRportFwdListener(sessionID, bindAddr string, bindPort uint32, fwdAddr string, fwdPort uint32) (RportFwdListenerView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.StartRportFwdListener(ctx, &sliverpb.RportFwdStartListenerReq{
		BindAddress:    joinAddr(bindAddr, bindPort),
		ForwardAddress: joinAddr(fwdAddr, fwdPort),
		Request:        &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return RportFwdListenerView{}, err
	}
	if resp.GetResponse().GetErr() != "" {
		return RportFwdListenerView{}, errors.New(resp.GetResponse().GetErr())
	}
	return rportFwdToView(resp), nil
}

// StopRportFwdListener tears one down by ID.
func (c *Client) StopRportFwdListener(sessionID string, id uint32) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.StopRportFwdListener(ctx, &sliverpb.RportFwdStopListenerReq{
		ID:      id,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// RportFwdListeners lists the reverse port forwards on a session.
func (c *Client) RportFwdListeners(sessionID string) ([]RportFwdListenerView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetRportFwdListeners(ctx, &sliverpb.RportFwdListenersReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	out := make([]RportFwdListenerView, 0, len(resp.Listeners))
	for _, l := range resp.Listeners {
		out = append(out, rportFwdToView(l))
	}
	return out, nil
}

// rportFwdToView derives the display shape from the address strings, which are
// the only fields the implant populates reliably.
func rportFwdToView(l *sliverpb.RportFwdListener) RportFwdListenerView {
	if l == nil {
		return RportFwdListenerView{}
	}
	bindHost, bindPort := splitAddr(l.BindAddress)
	fwdHost, fwdPort := splitAddr(l.ForwardAddress)
	return RportFwdListenerView{
		ID:             l.ID,
		BindAddress:    bindHost,
		BindPort:       bindPort,
		ForwardAddress: fwdHost,
		ForwardPort:    fwdPort,
	}
}
