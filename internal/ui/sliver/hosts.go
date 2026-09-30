package sliver

import (
	"context"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// IOCView is the JSON shape of an indicator of compromise.
type IOCView struct {
	ID       string `json:"ID"`
	Path     string `json:"Path"`
	FileHash string `json:"FileHash"`
}

// HostView is the JSON shape of a host tracked by the server.
type HostView struct {
	Hostname  string    `json:"Hostname"`
	HostUUID  string    `json:"HostUUID"`
	OSVersion string    `json:"OSVersion"`
	IOCs      []IOCView `json:"IOCs"`
}

func iocToView(i *clientpb.IOC) IOCView {
	if i == nil {
		return IOCView{}
	}
	return IOCView{ID: i.ID, Path: i.Path, FileHash: i.FileHash}
}

func hostToView(h *clientpb.Host) HostView {
	v := HostView{
		Hostname:  h.Hostname,
		HostUUID:  h.HostUUID,
		OSVersion: h.OSVersion,
		IOCs:      []IOCView{},
	}
	for _, i := range h.IOCs {
		if i == nil {
			continue
		}
		v.IOCs = append(v.IOCs, iocToView(i))
	}
	return v
}

// Hosts lists all hosts tracked by the server.
func (c *Client) Hosts() ([]HostView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.RPC.Hosts(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]HostView, 0, len(resp.Hosts))
	for _, h := range resp.Hosts {
		if h == nil {
			continue
		}
		out = append(out, hostToView(h))
	}
	return out, nil
}

// Host fetches a single host by its UUID.
func (c *Client) Host(hostUUID string) (*HostView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := c.RPC.Host(ctx, &clientpb.Host{HostUUID: hostUUID})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, nil
	}
	v := hostToView(resp)
	return &v, nil
}

// HostRm removes a host and its IOCs from the server database.
func (c *Client) HostRm(hostUUID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.RPC.HostRm(ctx, &clientpb.Host{HostUUID: hostUUID})
	return err
}

// HostIOCRm removes a single IOC from the server database.
func (c *Client) HostIOCRm(iocID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := c.RPC.HostIOCRm(ctx, &clientpb.IOC{ID: iocID})
	return err
}
