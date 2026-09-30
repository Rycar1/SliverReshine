package sliver

import (
	"context"
	"errors"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// WGTCPForwarderView is the JSON shape of a WireGuard TCP forwarder.
type WGTCPForwarderView struct {
	ID         int32  `json:"ID"`
	LocalAddr  string `json:"LocalAddr"`
	RemoteAddr string `json:"RemoteAddr"`
}

// WGSocksServerView is the JSON shape of a WireGuard SOCKS5 server.
type WGSocksServerView struct {
	ID        int32  `json:"ID"`
	LocalAddr string `json:"LocalAddr"`
}

// WGClientConfigView is the JSON shape of a generated WireGuard client config.
type WGClientConfigView struct {
	ServerPubKey     string `json:"ServerPubKey"`
	ClientPrivateKey string `json:"ClientPrivateKey"`
	ClientPubKey     string `json:"ClientPubKey"`
	ClientIP         string `json:"ClientIP"`
}

func wgForwarderToView(f *sliverpb.WGTCPForwarder) WGTCPForwarderView {
	v := WGTCPForwarderView{}
	if f == nil {
		return v
	}
	v.ID = f.ID
	v.LocalAddr = f.LocalAddr
	v.RemoteAddr = f.RemoteAddr
	return v
}

func wgSocksToView(s *sliverpb.WGSocksServer) WGSocksServerView {
	v := WGSocksServerView{}
	if s == nil {
		return v
	}
	v.ID = s.ID
	v.LocalAddr = s.LocalAddr
	return v
}

// GenerateWGClientConfig generates a WireGuard client configuration on the server.
func (c *Client) GenerateWGClientConfig() (*WGClientConfigView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.RPC.GenerateWGClientConfig(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	return &WGClientConfigView{
		ServerPubKey:     resp.ServerPubKey,
		ClientPrivateKey: resp.ClientPrivateKey,
		ClientPubKey:     resp.ClientPubKey,
		ClientIP:         resp.ClientIP,
	}, nil
}

// GenerateUniqueIP generates a unique WireGuard client IP.
func (c *Client) GenerateUniqueIP() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.RPC.GenerateUniqueIP(ctx, &commonpb.Empty{})
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", nil
	}
	return resp.IP, nil
}

// WGForwarders lists the TCP forwarders of a session's WireGuard interface.
func (c *Client) WGForwarders(sessionID string) ([]WGTCPForwarderView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WGListForwarders(ctx, &sliverpb.WGTCPForwardersReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	out := make([]WGTCPForwarderView, 0, len(resp.Forwarders))
	for _, f := range resp.Forwarders {
		out = append(out, wgForwarderToView(f))
	}
	return out, nil
}

// WGStartPortForward starts a TCP port forward on a session's WireGuard interface.
func (c *Client) WGStartPortForward(sessionID string, localPort int32, remoteAddress string) (WGTCPForwarderView, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WGStartPortForward(ctx, &sliverpb.WGPortForwardStartReq{
		LocalPort:     localPort,
		RemoteAddress: remoteAddress,
		Request:       &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return WGTCPForwarderView{}, false, err
	}
	if resp.GetResponse().GetErr() != "" {
		return WGTCPForwarderView{}, false, errors.New(resp.GetResponse().GetErr())
	}
	return wgForwarderToView(resp.Forwarder), resp.GetResponse().GetAsync(), nil
}

// WGStopPortForward stops a TCP port forward on a session's WireGuard interface.
func (c *Client) WGStopPortForward(sessionID string, id int32) (WGTCPForwarderView, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WGStopPortForward(ctx, &sliverpb.WGPortForwardStopReq{
		ID:      id,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return WGTCPForwarderView{}, false, err
	}
	if resp.GetResponse().GetErr() != "" {
		return WGTCPForwarderView{}, false, errors.New(resp.GetResponse().GetErr())
	}
	return wgForwarderToView(resp.Forwarder), resp.GetResponse().GetAsync(), nil
}

// WGSocksServers lists the SOCKS5 servers of a session's WireGuard interface.
func (c *Client) WGSocksServers(sessionID string) ([]WGSocksServerView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WGListSocksServers(ctx, &sliverpb.WGSocksServersReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	out := make([]WGSocksServerView, 0, len(resp.Servers))
	for _, s := range resp.Servers {
		out = append(out, wgSocksToView(s))
	}
	return out, nil
}

// WGStartSocks starts a SOCKS5 server on a session's WireGuard interface.
func (c *Client) WGStartSocks(sessionID string, port int32) (WGSocksServerView, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WGStartSocks(ctx, &sliverpb.WGSocksStartReq{
		Port:    port,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return WGSocksServerView{}, false, err
	}
	if resp.GetResponse().GetErr() != "" {
		return WGSocksServerView{}, false, errors.New(resp.GetResponse().GetErr())
	}
	return wgSocksToView(resp.Server), resp.GetResponse().GetAsync(), nil
}

// WGStopSocks stops a SOCKS5 server on a session's WireGuard interface.
func (c *Client) WGStopSocks(sessionID string, id int32) (WGSocksServerView, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.WGStopSocks(ctx, &sliverpb.WGSocksStopReq{
		ID:      id,
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return WGSocksServerView{}, false, err
	}
	if resp.GetResponse().GetErr() != "" {
		return WGSocksServerView{}, false, errors.New(resp.GetResponse().GetErr())
	}
	return wgSocksToView(resp.Server), resp.GetResponse().GetAsync(), nil
}
