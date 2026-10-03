package sliver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// ---------------------------------------------------------------------------
// HTTP C2 profiles
//
// A profile redefines what the implant's HTTP traffic looks like on the wire
// (URIs, headers, cookies, body transforms). Editing profiles from the console
// is what lets an operator reshape C2 traffic per engagement instead of being
// stuck with the defaults baked into the implant.
// ---------------------------------------------------------------------------

// C2ProfileView is a lightweight summary of a stored HTTP C2 profile.
type C2ProfileView struct {
	ID        string   `json:"ID"`
	Name      string   `json:"Name"`
	Created   int64    `json:"Created"`
	ServerURI []string `json:"ServerURI,omitempty"`
	UserAgent string   `json:"UserAgent,omitempty"`
}

// HTTPC2Profiles lists stored profiles.
func (c *Client) HTTPC2Profiles() ([]C2ProfileView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.GetHTTPC2Profiles(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]C2ProfileView, 0, len(resp.Configs))
	for _, p := range resp.Configs {
		v := C2ProfileView{ID: p.ID, Name: p.Name, Created: p.Created}
		if p.ImplantConfig != nil {
			v.UserAgent = p.ImplantConfig.UserAgent
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// HTTPC2Profile fetches one profile in full.
func (c *Client) HTTPC2Profile(name string) (*clientpb.HTTPC2Config, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.GetHTTPC2ProfileByName(ctx, &clientpb.C2ProfileReq{Name: name})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// SaveHTTPC2Profile stores a profile, optionally overwriting an existing one.
//
// The two nested messages are checked here, not just the outer one. Sliver's
// SaveHTTPC2Profile validates the config with CheckHTTPC2ConfigErrors, whose very
// first statement is `len(config.ServerConfig.Cookies)` -- an unconditional
// dereference. A request that supplies a profile without ServerConfig or
// ImplantConfig therefore panics inside the server, and the server is a child
// process (internal/launch starts it with exec.Command) that recovers only to
// call os.Exit(99). One malformed request from an authenticated operator takes
// down every session, beacon and listener on the engagement.
//
// The console is the only place that can prevent this: the check has to happen
// before the RPC is sent.
func (c *Client) SaveHTTPC2Profile(cfg *clientpb.HTTPC2Config, overwrite bool) error {
	if cfg == nil {
		return errors.New("no profile supplied")
	}
	if cfg.ServerConfig == nil {
		return errors.New("profile has no serverConfig: the server dereferences it before validating")
	}
	if cfg.ImplantConfig == nil {
		return errors.New("profile has no implantConfig: the server dereferences it before validating")
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	_, err := c.RPC.SaveHTTPC2Profile(ctx, &clientpb.HTTPC2ConfigReq{
		Overwrite: overwrite,
		C2Config:  cfg,
	})
	return err
}

// ---------------------------------------------------------------------------
// Traffic encoders
//
// Traffic encoders are WASM modules that transform every C2 message. They are
// the general mechanism behind custom encryption and protocol mimicry.
// ---------------------------------------------------------------------------

// TrafficEncoderView summarises a registered encoder.
type TrafficEncoderView struct {
	ID     uint64 `json:"ID"`
	TestID string `json:"TestID"`
}

// TrafficEncoders lists registered traffic encoders.
func (c *Client) TrafficEncoders() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.TrafficEncoderMap(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(resp.Encoders))
	for name := range resp.Encoders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// TrafficEncodersWithID lists encoders together with their IDs.
func (c *Client) TrafficEncodersWithID() (map[string]uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.TrafficEncoderMap(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]uint64, len(resp.Encoders))
	for name, e := range resp.Encoders {
		out[name] = e.GetID()
	}
	return out, nil
}

// TrafficEncoderAdd uploads a WASM encoder and runs the server's conformance
// tests against it. The returned report is what tells the operator whether the
// encoder is safe to use on a live implant.
func (c *Client) TrafficEncoderAdd(name string, wasm []byte, skipTests bool) (*TrafficEncoderReport, error) {
	if name == "" {
		return nil, errors.New("encoder name is required")
	}
	if len(wasm) == 0 {
		return nil, errors.New("encoder wasm is empty")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*opTimeout)
	defer cancel()
	resp, err := c.RPC.TrafficEncoderAdd(ctx, &clientpb.TrafficEncoder{
		Wasm:      &commonpb.File{Name: name, Data: wasm},
		SkipTests: skipTests,
	})
	if err != nil {
		return nil, err
	}
	rep := &TrafficEncoderReport{
		TotalTests:    int32(resp.TotalTests),
		TotalDuration: resp.TotalDuration,
		EncoderID:     resp.Encoder.GetID(),
	}
	for _, t := range resp.Tests {
		rep.Tests = append(rep.Tests, TrafficEncoderTestView{
			Name:      t.Name,
			Completed: t.Completed,
			Success:   t.Success,
			Duration:  t.Duration,
			Err:       t.Err,
		})
	}
	return rep, nil
}

// TrafficEncoderTestView is one conformance test result.
type TrafficEncoderTestView struct {
	Name      string `json:"Name"`
	Completed bool   `json:"Completed"`
	Success   bool   `json:"Success"`
	Duration  int64  `json:"Duration"`
	Err       string `json:"Err,omitempty"`
}

// TrafficEncoderReport is the JSON shape of the encoder test run.
type TrafficEncoderReport struct {
	EncoderID     uint64                   `json:"EncoderID"`
	TotalTests    int32                    `json:"TotalTests"`
	TotalDuration int64                    `json:"TotalDuration"`
	Tests         []TrafficEncoderTestView `json:"Tests"`
}

// TrafficEncoderRm removes a registered encoder.
func (c *Client) TrafficEncoderRm(name string) error {
	ids, err := c.TrafficEncodersWithID()
	if err != nil {
		return err
	}
	id, ok := ids[name]
	if !ok {
		return fmt.Errorf("no traffic encoder named %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	_, err = c.RPC.TrafficEncoderRm(ctx, &clientpb.TrafficEncoder{ID: id})
	return err
}

// ---------------------------------------------------------------------------
// Shellcode encoders
//
// Encoders rewrite shellcode so it survives a given loader's constraints —
// escaping bad characters and applying an encoding chain the stager can decode.
// ---------------------------------------------------------------------------

// ShellcodeEncoderView describes an available encoder chain.
type ShellcodeEncoderView struct {
	Arch     string   `json:"Arch"`
	Encoders []string `json:"Encoders"`
}

// ShellcodeEncoders lists encoder chains grouped by architecture.
func (c *Client) ShellcodeEncoders() ([]ShellcodeEncoderView, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.ShellcodeEncoderMap(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	out := make([]ShellcodeEncoderView, 0, len(resp.Encoders))
	for arch, m := range resp.Encoders {
		v := ShellcodeEncoderView{Arch: arch}
		for name := range m.Encoders {
			v.Encoders = append(v.Encoders, name)
		}
		sort.Strings(v.Encoders)
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Arch < out[j].Arch })
	return out, nil
}

// ShellcodeEncode runs an encoder over raw shellcode. Returns base64-ready bytes.
func (c *Client) ShellcodeEncode(encoder, arch string, data []byte, iterations uint32, badChars []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("no shellcode supplied")
	}
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	enc, err := shellcodeEncoderFromString(encoder)
	if err != nil {
		return nil, err
	}

	resp, err := c.RPC.ShellcodeEncoder(ctx, &clientpb.ShellcodeEncodeReq{
		Encoder:      enc,
		Architecture: arch,
		Iterations:   iterations,
		BadChars:     badChars,
		Data:         data,
	})
	if err != nil {
		return nil, err
	}
	if resp.GetResponse().GetErr() != "" {
		return nil, errors.New(resp.GetResponse().GetErr())
	}
	return resp.Data, nil
}

// ShellcodeEncoder is one encoder the server offers.
//
// The advertised Names are lowercase ("xor", "shikata_ga_nai"); the protobuf
// enum values are uppercase ("XOR", "SHIKATA_GA_NAI"). These are two different
// name spaces and they used to be confused -- see shellcodeEncoderFromString.
type ShellcodeEncoder struct {
	Enum  string
	Names []string
}

// knownShellcodeEncoders maps each protobuf enum onto the names the server
// advertises for it.
//
// It is a hardcoded table because the authoritative mapping is the server's own
// shellcodeEncoderEnums, which is unexported and unreachable from here. The
// console cannot import it, so it is mirrored -- with a test that fails if the
// two ever disagree about a name the console accepts.
var knownShellcodeEncoders = []ShellcodeEncoder{
	{Enum: "SHIKATA_GA_NAI", Names: []string{"shikata_ga_nai"}},
	{Enum: "XOR", Names: []string{"xor"}},
	{Enum: "XOR_DYNAMIC", Names: []string{"xor_dynamic"}},
}

// shellcodeEncoderFromString resolves an operator-supplied encoder name to the
// enum the RPC expects.
//
// This used to look the name up in clientpb.ShellcodeEncoder_value, whose keys
// are the ENUM names. Every lowercase name the console itself advertises -- and
// therefore every name the UI offers and its placeholder suggests -- missed that
// map and fell through to the zero value, ShellcodeEncoder_NONE. The server's
// response to NONE is to return the input unchanged:
//
//	if req.Encoder == clientpb.ShellcodeEncoder_NONE {
//	    resp.Data = req.Data
//	    return resp, nil
//	}
//
// So the operator got HTTP 200, a success toast, and their own unencoded bytes
// back, then handed raw shellcode to a loader. Nothing reported a failure.
//
// Both spellings are accepted here, and an unrecognised name is an error rather
// than a silent NONE. Falling back is exactly what made the old behaviour
// invisible.
func shellcodeEncoderFromString(s string) (clientpb.ShellcodeEncoder, error) {
	name := strings.TrimSpace(s)
	if name == "" {
		return 0, errors.New("no encoder named")
	}

	for _, e := range knownShellcodeEncoders {
		if strings.EqualFold(name, e.Enum) {
			return clientpb.ShellcodeEncoder(clientpb.ShellcodeEncoder_value[e.Enum]), nil
		}
		for _, alias := range e.Names {
			if strings.EqualFold(name, alias) {
				return clientpb.ShellcodeEncoder(clientpb.ShellcodeEncoder_value[e.Enum]), nil
			}
		}
	}

	valid := make([]string, 0, len(knownShellcodeEncoders))
	for _, e := range knownShellcodeEncoders {
		valid = append(valid, e.Names[0])
	}
	sort.Strings(valid)
	return 0, fmt.Errorf("unknown encoder %q; the server offers %s",
		s, strings.Join(valid, ", "))
}

// ---------------------------------------------------------------------------
// WASM extensions
//
// Sliver supports two extension flavours: BOF/COFF (already exposed) and WASM.
// WASM extensions are self-contained and target-independent, which makes them
// the more reusable of the two.
// ---------------------------------------------------------------------------

// WasmExtensions lists registered WASM extension names for a session.
func (c *Client) WasmExtensions(sessionID string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	defer cancel()
	resp, err := c.RPC.CreateTunnel(ctx, &sliverpb.Tunnel{SessionID: sessionID})
	if err != nil {
		return 0, err
	}
	return resp.TunnelID, nil
}

// CloseTunnel tears a tunnel down.
func (c *Client) CloseTunnel(tunnelID uint64, sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*opTimeout)
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
