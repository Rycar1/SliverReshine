package sliver

import (
	"context"
	"errors"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// Msf generates an MSF payload and executes it on the session.
func (c *Client) Msf(sessionID, payload, lhost string, lport uint32, encoder string, iterations int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.Msf(ctx, &clientpb.MSFReq{
		Payload:    payload,
		LHost:      lhost,
		LPort:      lport,
		Encoder:    encoder,
		Iterations: iterations,
		Request:    &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// MsfRemote injects an MSF payload into a remote process on the session.
func (c *Client) MsfRemote(sessionID, payload, lhost string, lport uint32, encoder string, iterations int32, pid uint32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	resp, err := c.RPC.MsfRemote(ctx, &clientpb.MSFRemoteReq{
		Payload:    payload,
		LHost:      lhost,
		LPort:      lport,
		Encoder:    encoder,
		Iterations: iterations,
		PID:        pid,
		Request:    &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// MsfStagerView is the JSON shape of a generated MSF stager.
type MsfStagerView struct {
	FileName string `json:"FileName"`
	DataB64  string `json:"DataB64"`
	Size     int    `json:"Size"`
}

// MsfStage generates an MSF stager (no session required).
// Note: the MsfStage RPC was removed from sliver-server; the new server only
// supports stage listeners built from saved profiles. This returns a clear
// error so the frontend surfaces it instead of failing silently.
func (c *Client) MsfStage(arch, format string, port uint32, host, osName, protocol string, badChars []string) (*MsfStagerView, error) {
	return nil, errors.New("MSF stager generation is not supported by the connected sliver-server version")
}
