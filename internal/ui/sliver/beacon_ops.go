package sliver

import (
	"errors"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// BeaconTaskView is the JSON shape of a beacon task.
type BeaconTaskView struct {
	ID          string `json:"ID"`
	BeaconID    string `json:"BeaconID"`
	CreatedAt   int64  `json:"CreatedAt"`
	State       string `json:"State"`
	SentAt      int64  `json:"SentAt"`
	CompletedAt int64  `json:"CompletedAt"`
	Description string `json:"Description"`
	ResponseB64 string `json:"ResponseB64,omitempty"`
}

func beaconTaskToView(t *clientpb.BeaconTask) *BeaconTaskView {
	if t == nil {
		return nil
	}
	return &BeaconTaskView{
		ID:          t.ID,
		BeaconID:    t.BeaconID,
		CreatedAt:   t.CreatedAt,
		State:       t.State,
		SentAt:      t.SentAt,
		CompletedAt: t.CompletedAt,
		Description: t.Description,
		ResponseB64: encodeBase64(t.Response),
	}
}

// RenameSession renames an interactive session.
func (c *Client) RenameSession(sessionID, name string) error {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err := c.RPC.Rename(ctx, &clientpb.RenameReq{
		SessionID: sessionID,
		Name:      name,
	})
	return err
}

// RenameBeacon renames a beacon.
func (c *Client) RenameBeacon(beaconID, name string) error {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err := c.RPC.Rename(ctx, &clientpb.RenameReq{
		BeaconID: beaconID,
		Name:     name,
	})
	return err
}

// RmBeacon removes a beacon from the server.
func (c *Client) RmBeacon(beaconID string) error {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	_, err := c.RPC.RmBeacon(ctx, &clientpb.Beacon{ID: beaconID})
	return err
}

// BeaconTasks lists the task queue of a beacon.
func (c *Client) BeaconTasks(beaconID string) ([]BeaconTaskView, error) {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.GetBeaconTasks(ctx, &clientpb.Beacon{ID: beaconID})
	if err != nil {
		return nil, err
	}
	out := make([]BeaconTaskView, 0, len(resp.Tasks))
	for _, t := range resp.Tasks {
		if t == nil {
			continue
		}
		if v := beaconTaskToView(t); v != nil {
			out = append(out, *v)
		}
	}
	return out, nil
}

// BeaconTaskContent fetches the full content of a single beacon task.
func (c *Client) BeaconTaskContent(taskID string) (*BeaconTaskView, error) {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.GetBeaconTaskContent(ctx, &clientpb.BeaconTask{ID: taskID})
	if err != nil {
		return nil, err
	}
	return beaconTaskToView(resp), nil
}

// ReconfigureSession changes the reconnect interval of a session.
//
// The argument is in SECONDS, because that is what the form collects, and it is
// converted to the nanoseconds the wire field holds. The proto field is a
// time.Duration and the implant does time.Duration(interval) then time.Sleep, so
// passing the raw seconds made the interval 1e9 times too short: a request for
// 60 s gave the implant 60 ns, turning its reconnect loop into a hot loop while
// the console reported "reconnect every 60s". The official client converts too
// (int64(time.Duration) from time.ParseDuration).
//
// The error is checked rather than returned blind, and the RPC's own Response.Err
// is surfaced, so a rejected reconfiguration is not reported as success.
func (c *Client) ReconfigureSession(sessionID string, reconnectSeconds int64) error {
	if reconnectSeconds < 0 {
		return errors.New("reconnect interval cannot be negative")
	}
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.Reconfigure(ctx, &sliverpb.ReconfigureReq{
		ReconnectInterval: reconnectSeconds * int64(time.Second),
		Request:           &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// OpenSessionFromBeacon instructs a beacon to open a new interactive session
// on its next check-in. Returns true when the request was queued asynchronously
// (beacon mode); the new session then appears in the session list on check-in.
func (c *Client) OpenSessionFromBeacon(beaconID string) (bool, error) {
	ctx, cancel := c.rpcCtx(rpcDefault)
	defer cancel()
	resp, err := c.RPC.OpenSession(ctx, &sliverpb.OpenSession{
		C2S:     []string{},
		Delay:   0,
		Request: &commonpb.Request{BeaconID: beaconID},
	})
	if err != nil {
		return false, err
	}
	return resp.GetResponse().GetAsync(), nil
}

// CloseSession closes an interactive session without killing the remote process.
func (c *Client) CloseSession(sessionID string) error {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	_, err := c.RPC.CloseSession(ctx, &sliverpb.CloseSession{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	return err
}

// MonitorStart enables dead-session monitoring (watchtower) on the server.
func (c *Client) MonitorStart() error {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	resp, err := c.RPC.MonitorStart(ctx, &commonpb.Empty{})
	if err != nil {
		return err
	}
	if resp.GetErr() != "" {
		return errors.New(resp.GetErr())
	}
	return nil
}

// MonitorStop disables dead-session monitoring on the server.
func (c *Client) MonitorStop() error {
	ctx, cancel := c.rpcCtx(rpcQuick)
	defer cancel()
	_, err := c.RPC.MonitorStop(ctx, &commonpb.Empty{})
	return err
}
