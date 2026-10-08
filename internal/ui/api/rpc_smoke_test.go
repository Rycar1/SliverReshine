package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"sliverreshine/internal/ui/sliver"
)

// rpcConn is a grpc.ClientConnInterface that answers every call.
//
// The console's handlers are thin: they validate the request, build an RPC
// request and render the reply. Testing them one at a time means a hand-written
// stub method for every RPC, which is why most of them had no test at all and
// sat at 0% coverage. Answering the connection instead of the interface lets a
// single stub carry the whole surface: rpcpb.NewSliverRPCClient wraps this conn,
// so every generated method funnels through Invoke, and a zero-value reply is
// enough for a handler that only passes the reply through.
//
// It is deliberately not a mock: it records the methods that were reached so a
// test can assert the handler got as far as the RPC, and it can be told to fail
// every call to exercise the error path.
type rpcConn struct {
	mu       sync.Mutex
	methods  []string
	unaryErr error
}

func (c *rpcConn) Invoke(_ context.Context, method string, _, _ any, _ ...grpc.CallOption) error {
	c.mu.Lock()
	c.methods = append(c.methods, method)
	err := c.unaryErr
	c.mu.Unlock()
	return err
}

func (c *rpcConn) NewStream(_ context.Context, _ *grpc.StreamDesc, method string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	c.mu.Lock()
	c.methods = append(c.methods, method)
	err := c.unaryErr
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &rpcStream{}, nil
}

func (c *rpcConn) called() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.methods...)
}

// rpcStream ends immediately: RecvMsg reports EOF, so a handler that drains a
// server stream finishes instead of blocking the test.
type rpcStream struct{}

func (rpcStream) Header() (metadata.MD, error) { return nil, nil }
func (rpcStream) Trailer() metadata.MD         { return nil }
func (rpcStream) CloseSend() error             { return nil }
func (rpcStream) Context() context.Context     { return context.Background() }
func (rpcStream) SendMsg(any) error            { return nil }
func (rpcStream) RecvMsg(any) error            { return io.EOF }

// serverWithEmptyRPC is a connected console whose sliver-server answers every
// RPC with a zero value.
func serverWithEmptyRPC() (*Server, *rpcConn) {
	conn := &rpcConn{}
	s := New()
	s.SetClient(&sliver.Client{RPC: rpcpb.NewSliverRPCClient(conn)})
	return s, conn
}

// panicBody is the sentence the recover middleware writes when a handler
// panics. It is the only reliable way to tell a crash from a handler that chose
// a 5xx on purpose -- a failed screenshot with no display and a refused RPC both
// answer 5xx and are correct.
const panicBody = "hit an internal error"

// TestRoutesServeAgainstAnEmptyRPCSurface walks the session-scoped and global
// routes with a well-formed-but-minimal request and asserts that none of them
// panics.
//
// A panic is the failure this catches: the recover middleware turns one into a
// 500, so a handler that dereferences a reply the server did not populate -- or
// that assumes a field the request left empty -- shows up here rather than as a
// crash in production. This is not hypothetical: the first run found two, a
// Regenerate that read resp.File.Name after guarding only resp.File.Data, and a
// SOCKS start that read the nil proxy before checking the error.
//
// Any other status is accepted. A 4xx means the handler validated the request,
// and a handler that answers 5xx deliberately is a decision, not a defect.
func TestRoutesServeAgainstAnEmptyRPCSurface(t *testing.T) {
	cases := []struct {
		method string
		path   string
		body   string
	}{
		// Session: filesystem
		{"GET", "/api/sessions/s-1/fs", ""},
		{"GET", "/api/sessions/s-1/fs/pwd", ""},
		{"POST", "/api/sessions/s-1/fs/cd", "{}"},
		{"GET", "/api/sessions/s-1/fs/cat", ""},
		{"GET", "/api/sessions/s-1/fs/download", ""},
		{"POST", "/api/sessions/s-1/fs/mkdir", "{}"},
		{"DELETE", "/api/sessions/s-1/fs", ""},
		{"POST", "/api/sessions/s-1/fs/mv", "{}"},
		{"POST", "/api/sessions/s-1/fs/chmod", "{}"},
		{"POST", "/api/sessions/s-1/fs/chown", "{}"},
		{"POST", "/api/sessions/s-1/fs/chtimes", "{}"},
		{"POST", "/api/sessions/s-1/fs/grep", "{}"},
		{"GET", "/api/sessions/s-1/memfiles", ""},
		{"POST", "/api/sessions/s-1/memfiles", "{}"},
		{"DELETE", "/api/sessions/s-1/memfiles", ""},
		// Session: process and network
		{"GET", "/api/sessions/s-1/ifconfig", ""},
		{"GET", "/api/sessions/s-1/ps", ""},
		{"POST", "/api/sessions/s-1/ps/kill", "{}"},
		{"GET", "/api/sessions/s-1/netstat", ""},
		{"GET", "/api/sessions/s-1/screenshot", ""},
		{"POST", "/api/sessions/s-1/ping", "{}"},
		{"GET", "/api/sessions/s-1/env", ""},
		{"POST", "/api/sessions/s-1/env", "{}"},
		{"DELETE", "/api/sessions/s-1/env/PATH", ""},
		{"POST", "/api/sessions/s-1/exec", "{}"},
		{"POST", "/api/sessions/s-1/exec", "{\"path\":\"/bin/sh\",\"args\":[]}"},
		{"POST", "/api/sessions/s-1/exec-assembly", "{}"},
		{"POST", "/api/sessions/s-1/sideload", "{}"},
		{"POST", "/api/sessions/s-1/spawn-dll", "{}"},
		{"POST", "/api/sessions/s-1/migrate", "{}"},
		{"POST", "/api/sessions/s-1/process-dump", "{}"},
		{"POST", "/api/sessions/s-1/exec-shellcode", "{}"},
		{"POST", "/api/sessions/s-1/psexec", "{}"},
		// Session: privileges
		{"POST", "/api/sessions/s-1/impersonate", "{}"},
		{"POST", "/api/sessions/s-1/make-token", "{}"},
		{"POST", "/api/sessions/s-1/rev-to-self", "{}"},
		{"POST", "/api/sessions/s-1/getsystem", "{}"},
		{"GET", "/api/sessions/s-1/privs", ""},
		{"GET", "/api/sessions/s-1/token-owner", ""},
		{"POST", "/api/sessions/s-1/execute-token", "{}"},
		{"POST", "/api/sessions/s-1/runas", "{}"},
		// Session: registry
		{"GET", "/api/sessions/s-1/reg/subkeys", ""},
		{"GET", "/api/sessions/s-1/reg/values", ""},
		{"GET", "/api/sessions/s-1/reg/read", ""},
		{"POST", "/api/sessions/s-1/reg/write", "{}"},
		{"POST", "/api/sessions/s-1/reg/create-key", "{}"},
		{"POST", "/api/sessions/s-1/reg/delete-key", "{}"},
		{"POST", "/api/sessions/s-1/reg/hive", "{}"},
		// Session: services, extensions, msf, persistence
		{"POST", "/api/sessions/s-1/services", "{}"},
		{"POST", "/api/sessions/s-1/services/detail", "{}"},
		{"POST", "/api/sessions/s-1/services/start-by-name", "{}"},
		{"POST", "/api/sessions/s-1/services/stop", "{}"},
		{"POST", "/api/sessions/s-1/services/remove", "{}"},
		{"POST", "/api/sessions/s-1/ssh", "{}"},
		{"GET", "/api/sessions/s-1/extensions", ""},
		{"POST", "/api/sessions/s-1/extensions/register", "{}"},
		{"POST", "/api/sessions/s-1/extensions/call", "{}"},
		{"GET", "/api/sessions/s-1/wasm", ""},
		{"POST", "/api/sessions/s-1/wasm/register", "{}"},
		{"POST", "/api/sessions/s-1/wasm/exec", "{}"},
		{"POST", "/api/sessions/s-1/msf", "{}"},
		{"POST", "/api/sessions/s-1/msf/remote", "{}"},
		{"POST", "/api/sessions/s-1/backdoor", "{}"},
		{"POST", "/api/sessions/s-1/dll-hijack", "{}"},
		{"GET", "/api/sessions/s-1/persistence", ""},
		{"POST", "/api/sessions/s-1/persistence/install", "{}"},
		{"POST", "/api/sessions/s-1/persistence/remove", "{}"},
		{"POST", "/api/sessions/s-1/mimikatz", "{}"},
		// Session: pivots, tunnels, port forwarding, wireguard
		{"GET", "/api/sessions/s-1/pivots/listeners", ""},
		{"POST", "/api/sessions/s-1/pivots/listeners", "{}"},
		{"DELETE", "/api/sessions/s-1/pivots/listeners/1", ""},
		{"POST", "/api/sessions/s-1/tunnel", "{}"},
		{"DELETE", "/api/tunnels", ""},
		{"GET", "/api/sessions/s-1/rportfwd", ""},
		{"POST", "/api/sessions/s-1/rportfwd", "{}"},
		{"DELETE", "/api/sessions/s-1/rportfwd/1", ""},
		{"GET", "/api/portfwd", ""},
		{"POST", "/api/portfwd", "{}"},
		{"DELETE", "/api/portfwd/1", ""},
		{"GET", "/api/sessions/s-1/wg/forwarders", ""},
		{"POST", "/api/sessions/s-1/wg/forwarders", "{}"},
		{"DELETE", "/api/sessions/s-1/wg/forwarders/1", ""},
		{"GET", "/api/sessions/s-1/wg/socks", ""},
		{"POST", "/api/sessions/s-1/wg/socks", "{}"},
		{"DELETE", "/api/sessions/s-1/wg/socks/1", ""},
		// Session: aliases, ai
		{"POST", "/api/sessions/s-1/aliases/whoami/run", "{}"},
		{"POST", "/api/sessions/s-1/ai-collect", "{}"},
		// Beacons
		{"GET", "/api/beacons", ""},
		{"GET", "/api/beacons/b-1", ""},
		{"POST", "/api/beacons/b-1/rename", "{}"},
		{"DELETE", "/api/beacons/b-1", ""},
		{"GET", "/api/beacons/b-1/tasks", ""},
		{"GET", "/api/beacons/b-1/tasks/1", ""},
		{"POST", "/api/beacons/b-1/open-session", "{}"},
		{"POST", "/api/sessions/s-1/rename", "{}"},
		{"POST", "/api/monitor/start", "{}"},
		{"POST", "/api/monitor/stop", "{}"},
		{"GET", "/api/monitor/providers", ""},
		{"POST", "/api/monitor/providers", "{}"},
		{"DELETE", "/api/monitor/providers", ""},
		// Implants, builds, profiles, listeners
		{"GET", "/api/implant-profiles", ""},
		{"POST", "/api/implant-profiles", "{}"},
		{"DELETE", "/api/implant-profiles/p", ""},
		{"DELETE", "/api/implant-builds/b", ""},
		{"POST", "/api/regenerate", "{}"},
		{"GET", "/api/operators", ""},
		{"GET", "/api/compiler", ""},
		{"GET", "/api/builders", ""},
		{"POST", "/api/generate", "{}"},
		{"POST", "/api/listeners", "{}"},
		{"DELETE", "/api/listeners/1", ""},
		{"GET", "/api/listeners/bind", ""},
		{"POST", "/api/listeners/bind", "{}"},
		{"DELETE", "/api/listeners/bind/1", ""},
		{"GET", "/api/jobs", ""},
		{"GET", "/api/events", ""},
		{"GET", "/api/socks", ""},
		{"POST", "/api/socks", "{}"},
		{"DELETE", "/api/socks/1", ""},
		{"POST", "/api/msf/stage", "{}"},
		{"POST", "/api/shellcode/rdi", "{}"},
		{"POST", "/api/mimikatz/parse", "{}"},
		// Loot, creds, hosts, websites
		{"GET", "/api/loot", ""},
		{"POST", "/api/loot", "{}"},
		{"POST", "/api/loot/l-1/rename", "{}"},
		{"GET", "/api/loot/l-1", ""},
		{"DELETE", "/api/loot/l-1", ""},
		{"GET", "/api/creds", ""},
		{"POST", "/api/creds", "{}"},
		{"PUT", "/api/creds", "{}"},
		{"DELETE", "/api/creds", ""},
		{"GET", "/api/creds/filter", ""},
		{"POST", "/api/creds/sniff", "{}"},
		{"GET", "/api/creds/c-1", ""},
		{"GET", "/api/hosts", ""},
		{"GET", "/api/hosts/h-1", ""},
		{"DELETE", "/api/hosts/h-1", ""},
		{"DELETE", "/api/hosts/h-1/iocs/1", ""},
		{"GET", "/api/websites", ""},
		{"GET", "/api/websites/site-1", ""},
		{"POST", "/api/websites/site-1/content", "{}"},
		{"PUT", "/api/websites/site-1/content", "{}"},
		{"DELETE", "/api/websites/site-1/content", ""},
		{"DELETE", "/api/websites/site-1", ""},
		{"GET", "/api/canaries", ""},
		// C2 config surface
		{"GET", "/api/c2profiles", ""},
		{"POST", "/api/c2profiles", "{}"},
		{"GET", "/api/c2profiles/p", ""},
		{"GET", "/api/traffic-encoders", ""},
		{"POST", "/api/traffic-encoders", "{}"},
		{"DELETE", "/api/traffic-encoders/e", ""},
		{"GET", "/api/shellcode-encoders", ""},
		{"POST", "/api/shellcode-encoders", "{}"},
		{"GET", "/api/certificates/ca", ""},
		{"GET", "/api/certificates", ""},
		{"GET", "/api/wg/config", ""},
		{"GET", "/api/wg/ip", ""},
		{"GET", "/api/aliases", ""},
		{"POST", "/api/aliases", "{}"},
		{"DELETE", "/api/aliases/a", ""},
		// Topology, modules, misc
		{"GET", "/api/pivots/graph", ""},
		{"GET", "/api/topology", ""},
		{"GET", "/api/persistence/modules", ""},
		{"GET", "/api/mimikatz/modules", ""},
		{"GET", "/api/rpc/methods", ""},
		{"GET", "/api/ai/status", ""},
		{"POST", "/api/ai/read-only/check", "{}"},
		{"GET", "/api/overview", ""},
		{"GET", "/api/webdelivery/formats", ""},
		{"POST", "/api/webdelivery", "{}"},
		{"GET", "/api/oneliner/targets", ""},
		{"POST", "/api/oneliner", "{}"},
		{"POST", "/api/oneliner/all", "{}"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			s, _ := serverWithEmptyRPC()
			var r *http.Request
			if tc.body == "" {
				r = httptest.NewRequest(tc.method, tc.path, nil)
			} else {
				r = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			}
			rec := recorderFor(s, r)
			if strings.Contains(rec.Body.String(), panicBody) {
				t.Fatalf("%s %s panicked: %s", tc.method, tc.path, rec.Body.String())
			}
		})
	}
}
