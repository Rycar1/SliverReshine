package sliver

import (
	"context"
	"errors"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// StartService creates and starts a Windows service via the session.
func (c *Client) StartService(sessionID, serviceName, description, binPath, hostname, arguments string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.RPC.StartService(ctx, &sliverpb.StartServiceReq{
		ServiceName:        serviceName,
		ServiceDescription: description,
		BinPath:            binPath,
		Hostname:           hostname,
		Arguments:          arguments,
		Request:            &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// StopService stops a Windows service via the session.
func (c *Client) StopService(sessionID, serviceName, hostname string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.RPC.StopService(ctx, &sliverpb.StopServiceReq{
		ServiceInfo: &sliverpb.ServiceInfoReq{
			ServiceName: serviceName,
			Hostname:    hostname,
		},
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

// RemoveService deletes a Windows service via the session.
func (c *Client) RemoveService(sessionID, serviceName, hostname string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.RPC.RemoveService(ctx, &sliverpb.RemoveServiceReq{
		ServiceInfo: &sliverpb.ServiceInfoReq{
			ServiceName: serviceName,
			Hostname:    hostname,
		},
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

// SSHCommandResult is the JSON shape of an SSH command result.
type SSHCommandResult struct {
	StdOut string `json:"StdOut"`
	StdErr string `json:"StdErr"`
}

// RunSSHCommand runs a command over SSH from the session to another host.
func (c *Client) RunSSHCommand(sessionID, username, hostname string, port uint32, command, password string, privKey []byte) (*SSHCommandResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.RunSSHCommand(ctx, &sliverpb.SSHCommandReq{
		Username: username,
		Hostname: hostname,
		Port:     port,
		Command:  command,
		Password: password,
		PrivKey:  privKey,
		Request:  &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	return &SSHCommandResult{StdOut: resp.StdOut, StdErr: resp.StdErr}, nil
}

// RegisterExtension registers an extension on the session.
func (c *Client) RegisterExtension(sessionID, name, os, init string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.RPC.RegisterExtension(ctx, &sliverpb.RegisterExtensionReq{
		Name:    name,
		Data:    data,
		OS:      os,
		Init:    init,
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

// ListExtensions lists extensions registered on the session.
func (c *Client) ListExtensions(sessionID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := c.RPC.ListExtensions(ctx, &sliverpb.ListExtensionsReq{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	return resp.Names, nil
}

// CallExtensionResult is the JSON shape of an extension call result.
type CallExtensionResult struct {
	Output      string `json:"Output"`
	ServerStore bool   `json:"ServerStore"`
}

// CallExtension calls an exported function of a registered extension.
func (c *Client) CallExtension(sessionID, name, export string, serverStore bool, args []byte) (*CallExtensionResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	resp, err := c.RPC.CallExtension(ctx, &sliverpb.CallExtensionReq{
		Name:        name,
		Export:      export,
		ServerStore: serverStore,
		Args:        args,
		Request:     &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	return &CallExtensionResult{Output: string(resp.Output), ServerStore: resp.ServerStore}, nil
}
