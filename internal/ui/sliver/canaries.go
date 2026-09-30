package sliver

import (
	"context"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// CanaryView is the JSON shape of a DNS canary.
type CanaryView struct {
	ImplantName    string `json:"ImplantName"`
	Domain         string `json:"Domain"`
	Triggered      bool   `json:"Triggered"`
	FirstTriggered string `json:"FirstTriggered,omitempty"`
	LatestTrigger  string `json:"LatestTrigger,omitempty"`
	Count          uint32 `json:"Count"`
}

// Canaries lists the DNS canaries tracked by the server.
func (c *Client) Canaries() ([]CanaryView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := c.RPC.Canaries(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]CanaryView, 0, len(resp.Canaries))
	for _, can := range resp.Canaries {
		if can == nil {
			continue
		}
		out = append(out, CanaryView{
			ImplantName:    can.ImplantName,
			Domain:         can.Domain,
			Triggered:      can.Triggered,
			FirstTriggered: can.FirstTriggered,
			LatestTrigger:  can.LatestTrigger,
			Count:          can.Count,
		})
	}
	return out, nil
}
