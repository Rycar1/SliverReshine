package sliver

import (
	"context"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// PruneBeacons removes beacons that have not checked in for the given number
// of days past their next scheduled check-in. Mirrors the official client's
// `beacons prune` command. Returns the number of beacons removed.
func (c *Client) PruneBeacons(days int) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	beacons, err := c.RPC.GetBeacons(ctx, &commonpb.Empty{})
	if err != nil {
		return 0, err
	}
	pruneDuration := time.Duration(days) * 24 * time.Hour
	pruned := 0
	for _, beacon := range beacons.Beacons {
		if beacon == nil {
			continue
		}
		nextCheckin := time.Unix(beacon.NextCheckin, 0)
		if time.Now().Before(nextCheckin) {
			continue
		}
		if pruneDuration > time.Since(nextCheckin) {
			continue
		}
		if _, err := c.RPC.RmBeacon(ctx, &clientpb.Beacon{ID: beacon.ID}); err == nil {
			pruned++
		}
	}
	return pruned, nil
}

// PruneSessions kills all sessions flagged as dead. Returns the number pruned.
func (c *Client) PruneSessions() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sessions, err := c.RPC.GetSessions(ctx, &commonpb.Empty{})
	if err != nil {
		return 0, err
	}
	pruned := 0
	for _, session := range sessions.Sessions {
		if session == nil || !session.IsDead {
			continue
		}
		if c.KillSession(session.ID) == nil {
			pruned++
		}
	}
	return pruned, nil
}
