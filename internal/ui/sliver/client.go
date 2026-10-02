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
	"time"

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

	pfMu  sync.Mutex
	pfMgr *PortForwardManager

	sMu  sync.Mutex
	sMgr *SocksManager

	// osCache memoises each session's operating system, keyed by session ID.
	// It decides which execute RPC a process spawn uses, so it is read on every
	// spawn; see exec.go for why the two RPCs are not interchangeable.
	osMu    sync.Mutex
	osCache map[string]string

	// listenerSites records which website each HTTP listener this console started
	// serves, keyed by job ID.
	//
	// It exists because Sliver does not tell us: a Job carries a name, a port and
	// a free-text description, and no field for the website it was bound to. A
	// stage published to one website is invisible to a listener serving another,
	// so the delivery URL 404s while everything reports success -- the listener
	// is up, the content is published, and the fetch fails.
	//
	// Only listeners started here are recorded. One started elsewhere (a previous
	// run, the server CLI) has no entry, and the one-liner says so rather than
	// guessing a name that would silently not match.
	lsMu  sync.Mutex
	lsMap map[uint32]string
}

// rememberListenerSite records which website a listener serves.
func (c *Client) rememberListenerSite(jobID uint32, website string) {
	c.lsMu.Lock()
	defer c.lsMu.Unlock()
	if c.lsMap == nil {
		c.lsMap = map[uint32]string{}
	}
	c.lsMap[jobID] = website
}

// listenerSite returns the website a listener serves and whether it is known.
func (c *Client) listenerSite(jobID uint32) (string, bool) {
	c.lsMu.Lock()
	defer c.lsMu.Unlock()
	site, ok := c.lsMap[jobID]
	return site, ok
}

// PortForwards lazily creates and returns the port-forward manager.
func (c *Client) PortForwards() (*PortForwardManager, error) {
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
	c.pfMu.Lock()
	defer c.pfMu.Unlock()
	return c.pfMgr
}

// ExistingSocks returns the SOCKS manager only if it already exists. Same reason
// as ExistingPortForwards: a read must not create what it is reporting.
func (c *Client) ExistingSocks() *SocksManager {
	c.sMu.Lock()
	defer c.sMu.Unlock()
	return c.sMgr
}

// Socks lazily creates and returns the SOCKS5 proxy manager.
func (c *Client) Socks() *SocksManager {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ver, err := c.RPC.GetVersion(ctx, &commonpb.Empty{})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%d.%d (commit %s)", ver.Major, ver.Minor, ver.Patch, ver.Commit), nil
}
