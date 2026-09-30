package sliver

import (
	"context"
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := c.RPC.Rename(ctx, &clientpb.RenameReq{
		SessionID: sessionID,
		Name:      name,
	})
	return err
}

// RenameBeacon renames a beacon.
func (c *Client) RenameBeacon(beaconID, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := c.RPC.Rename(ctx, &clientpb.RenameReq{
		BeaconID: beaconID,
		Name:     name,
	})
	return err
}

// RmBeacon removes a beacon from the server.
func (c *Client) RmBeacon(beaconID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := c.RPC.RmBeacon(ctx, &clientpb.Beacon{ID: beaconID})
	return err
}

// BeaconTasks lists the task queue of a beacon.
func (c *Client) BeaconTasks(beaconID string) ([]BeaconTaskView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.GetBeaconTaskContent(ctx, &clientpb.BeaconTask{ID: taskID})
	if err != nil {
		return nil, err
	}
	return beaconTaskToView(resp), nil
}

// ReconfigureSession changes the reconnect interval of a session (seconds).
func (c *Client) ReconfigureSession(sessionID string, reconnectSeconds int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := c.RPC.Reconfigure(ctx, &sliverpb.ReconfigureReq{
		ReconnectInterval: reconnectSeconds,
		Request:           &commonpb.Request{SessionID: sessionID},
	})
	return err
}

// OpenSessionFromBeacon instructs a beacon to open a new interactive session
// on its next check-in. Returns true when the request was queued asynchronously
// (beacon mode); the new session then appears in the session list on check-in.
func (c *Client) OpenSessionFromBeacon(beaconID string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.RPC.CloseSession(ctx, &sliverpb.CloseSession{
		Request: &commonpb.Request{SessionID: sessionID},
	})
	return err
}

// MonitorStart enables dead-session monitoring (watchtower) on the server.
func (c *Client) MonitorStart() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.RPC.MonitorStop(ctx, &commonpb.Empty{})
	return err
}
