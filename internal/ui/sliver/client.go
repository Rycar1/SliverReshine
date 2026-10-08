package sliver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
)

// maxReceiveMessageSize caps a single gRPC reply the console will accept.

// Sliver's server sends up to `server/transport` ServerMaxMessageSize (2 GB)
// and Sliver's own client is configured for 2 GB - 1. Matching that here means
// a reply the server is willing to produce is never rejected by the console.
// Process dumps are the RPC that reaches this: a full-memory minidump is
// proportional to the target's committed memory and routinely exceeds the
// 128 MB that used to be configured here.
const maxReceiveMessageSize = (2 * 1024 * 1024 * 1024) - 1

// Client wraps a Sliver gRPC connection.
type Client struct {
	conn    *grpc.ClientConn
	RPC     rpcpb.SliverRPCClient
	Profile string

	// root is the console-wide client this one is a view of, or nil when this
	// is that client. A view is created per HTTP request by
	// WithRequestContext; it shares the connection and every piece of cached
	// state with the root and differs only in reqCtx, so a request never gets
	// its own port-forward manager or listener-to-website map.
	root *Client

	// reqCtx is the HTTP request this view is serving, if any. It bounds every
	// gRPC call the view makes (see rpcCtx) and cancels them when the browser
	// goes away. Nil on the console-wide client, which serves no single request.
	reqCtx context.Context

	pfMu  sync.Mutex
	pfMgr *PortForwardManager

	sMu  sync.Mutex
	sMgr *SocksManager

	// osCache memoises each session's operating system, keyed by session ID.
	// It decides which execute RPC a process spawn uses, so it is read on every
	// spawn; see exec.go for why the two RPCs are not interchangeable.
	osMu    sync.Mutex
	osCache map[string]string

	// sites and hosts are the two per-listener facts Sliver does not report: the
	// website an HTTP listener serves, and the address it is reachable at. Both
	// are recorded when this console starts a listener and persisted beside the
	// profiles. See listener_sites.go and jobmap.go.
	//
	// mapsMu guards their lazy creation only; each map has its own lock for the
	// values themselves.
	mapsMu sync.Mutex
	sites  *persistedJobMap
	hosts  *persistedJobMap

	// stageCache memoises stages that were already built and published,
	// keyed by the fingerprint in oneliner.go. It is what makes re-opening
	// the one-liner dialog a map lookup instead of two implant builds.
	// Entries are small and keyed by listener, so nothing evicts them.
	stageMu    sync.Mutex
	stageCache map[string]OneLinerResult
}

// WithRequestContext returns a view of the client bound to ctx, the context of
// the HTTP request being served.
//
// The view shares the connection, the managers and every cache with the client
// it came from -- it is the same console, seen from one request -- so nothing
// request-shaped leaks into console-wide state. What it adds is inheritance:
// the gRPC calls it makes are children of ctx, so a browser that disconnects
// cancels the work instead of leaving it to run out its own budget, and a call
// is capped at whatever is left of the request's budget rather than at the
// method's full timeout.
func (c *Client) WithRequestContext(ctx context.Context) *Client {
	if c == nil || ctx == nil {
		return c
	}
	return &Client{
		conn:    c.conn,
		RPC:     c.RPC,
		Profile: c.Profile,
		root:    c.rootClient(),
		reqCtx:  ctx,
	}
}

// rootClient returns the console-wide client this one is a view of.
func (c *Client) rootClient() *Client {
	if c.root != nil {
		return c.root
	}
	return c
}

// PortForwards lazily creates and returns the port-forward manager.
func (c *Client) PortForwards() (*PortForwardManager, error) {
	if c.root != nil {
		return c.root.PortForwards()
	}
	c.pfMu.Lock()
	defer c.pfMu.Unlock()
	if c.pfMgr == nil {
		mgr, err := NewPortForwardManager(c)
		if err != nil {
			return nil, err
		}
		c.pfMgr = mgr
	}
	return c.pfMgr, nil
}

// ExistingPortForwards returns the port-forward manager only if one has already
// been created, and never brings one into existence.
//
// Reading the list of forwards is not a reason to open a tunnel manager: the
// constructor dials gRPC, so a caller that only wants to render what is already
// running would pay a round trip and change the state it is reporting. The
// topology view polls, which is exactly that case.
func (c *Client) ExistingPortForwards() *PortForwardManager {
	if c.root != nil {
		return c.root.ExistingPortForwards()
	}
	c.pfMu.Lock()
	defer c.pfMu.Unlock()
	return c.pfMgr
}

// ExistingSocks returns the SOCKS manager only if it already exists. Same reason
// as ExistingPortForwards: a read must not create what it is reporting.
func (c *Client) ExistingSocks() *SocksManager {
	if c.root != nil {
		return c.root.ExistingSocks()
	}
	c.sMu.Lock()
	defer c.sMu.Unlock()
	return c.sMgr
}

// Socks lazily creates and returns the SOCKS5 proxy manager.
func (c *Client) Socks() *SocksManager {
	if c.root != nil {
		return c.root.Socks()
	}
	c.sMu.Lock()
	defer c.sMu.Unlock()
	if c.sMgr == nil {
		c.sMgr = NewSocksManager(c)
	}
	return c.sMgr
}

// ProfileConfig mirrors the sliver-client JSON profile format found in
// ~/.sliver-client/configs/<name>.json
type ProfileConfig struct {
	Operator      string `json:"operator"`
	LHost         string `json:"lhost"`
	LPort         int    `json:"lport"`
	Token         string `json:"token"`
	CACertificate string `json:"ca_certificate"`
	Certificate   string `json:"certificate"`
	PrivateKey    string `json:"private_key"`
}

// ConfigPaths returns the default sliver-client config directory locations.
func ConfigPaths() []string {
	var out []string
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".sliver-client", "configs"))
	}
	if env := os.Getenv("SLIVER_CLIENT_CONFIGS"); env != "" {
		for _, p := range filepath.SplitList(env) {
			if p != "" {
				out = append(out, p)
			}
		}
	}
	out = append(out, "/root/.sliver-client/configs")
	return out
}

// ListProfiles scans the sliver-client config directories for saved profiles.
func ListProfiles() []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range ConfigPaths() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if filepath.Ext(name) != ".json" {
				continue
			}
			name = name[:len(name)-len(".json")]
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// LoadProfile reads a profile JSON from the sliver-client config dirs.
func LoadProfile(name string) (*ProfileConfig, error) {
	// The name arrives as a URL path segment, and Go's ServeMux hands back the
	// percent-decoded value -- so "%2F" is a literal separator by the time it
	// gets here and filepath.Join would happily walk out of the config directory.
	// Checked once, before the loop, so every directory in ConfigPaths() is
	// covered by the same rule.
	if err := validateArtifactName(name); err != nil {
		return nil, fmt.Errorf("invalid profile name: %w", err)
	}

	for _, dir := range ConfigPaths() {
		p := filepath.Join(dir, name+".json")
		if data, err := os.ReadFile(p); err == nil {
			var cfg ProfileConfig
			if err := json.Unmarshal(data, &cfg); err != nil {
				return nil, fmt.Errorf("parse profile %s: %w", name, err)
			}
			return &cfg, nil
		}
	}
	return nil, fmt.Errorf("profile %q not found in %v", name, ConfigPaths())
}

// ParseProfile parses a sliver-client profile JSON document (the same format
// found in ~/.sliver-client/configs/<name>.json) into a ProfileConfig. The
// raw bytes come from a config file loaded by the UI, not from disk.
func ParseProfile(data []byte) (*ProfileConfig, error) {
	var cfg ProfileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return &cfg, nil
}

// tokenAuth attaches the operator bearer token to every gRPC request.
type tokenAuth struct {
	token string
}

func (t tokenAuth) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	return map[string]string{"Authorization": "Bearer " + t.token}, nil
}

func (tokenAuth) RequireTransportSecurity() bool { return true }

// Connect establishes a mTLS gRPC connection to sliver-server. Like the
// official sliver-client, hostname validation is skipped but the server
// certificate chain is still verified against the profile CA, and the
// operator token is attached to every request.
func Connect(cfg *ProfileConfig) (*Client, error) {
	if cfg.CACertificate == "" {
		return nil, fmt.Errorf("profile is missing CA certificate")
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM([]byte(cfg.CACertificate)) {
		return nil, fmt.Errorf("invalid CA certificate in profile")
	}
	cert, err := tls.X509KeyPair([]byte(cfg.Certificate), []byte(cfg.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("invalid client certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		RootCAs:            caPool,
		Certificates:       []tls.Certificate{cert},
		InsecureSkipVerify: true, // hostname check is done by hand; chain verified against CA
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return rootOnlyVerify(cfg.CACertificate, rawCerts)
		},
	}
	creds := credentials.NewTLS(tlsConfig)

	ctx, cancel := dialCtx(rpcQuick)
	defer cancel()

	addr := fmt.Sprintf("%s:%d", cfg.LHost, cfg.LPort)
	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(tokenAuth{token: cfg.Token}),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxReceiveMessageSize)),
	)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:    conn,
		RPC:     rpcpb.NewSliverRPCClient(conn),
		Profile: cfg.Operator,
	}, nil
}

// rootOnlyVerify validates the server certificate chain against the profile
// CA, skipping hostname matching (mirrors the official sliver-client).
func rootOnlyVerify(caCertificate string, rawCerts [][]byte) error {
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(caCertificate)) {
		return fmt.Errorf("failed to parse root certificate")
	}
	if len(rawCerts) == 0 {
		return fmt.Errorf("no server certificate presented")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("failed to parse server certificate: %w", err)
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		return err
	}
	return nil
}

// Close terminates the gRPC connection.
func (c *Client) Close() {
	if c.root != nil {
		c.root.Close()
		return
	}
	c.pfMu.Lock()
	if c.pfMgr != nil {
		c.pfMgr.Close()
		c.pfMgr = nil
	}
	c.pfMu.Unlock()
	c.sMu.Lock()
	if c.sMgr != nil {
		c.sMgr.Close()
		c.sMgr = nil
	}
	c.sMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// Version queries the sliver-server version.
func (c *Client) Version() (string, error) {
	ctx, cancel := c.rpcCtx(rpcProbe)
	defer cancel()
	ver, err := c.RPC.GetVersion(ctx, &commonpb.Empty{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d (commit %s)", ver.Major, ver.Minor, ver.Patch, ver.Commit), nil
}
