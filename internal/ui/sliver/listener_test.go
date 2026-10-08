package sliver

import (
	"context"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"
)

// StartListener built a DNS request without the port, so every DNS listener
// bound the server's own default (53) no matter what the operator asked for --
// while the API answered {"success":true}.
//
// A listener on the wrong port does not fail; it simply never receives
// anything. That is the worst way for this to go wrong: the operator sees a
// successful start, the payload runs, and nothing ever connects, with no error
// anywhere to explain it.
//
// The callback test found it: the job reported Port: 0 while the request said
// 9053, and netstat showed UDP 53.

type listenerStub struct {
	rpcpb.SliverRPCClient

	dnsReq  *clientpb.DNSListenerReq
	mtlsReq *clientpb.MTLSListenerReq
	wgReq   *clientpb.WGListenerReq
	httpReq *clientpb.HTTPListenerReq

	getJobs func() (*clientpb.Jobs, error)
}

func (s *listenerStub) GetJobs(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Jobs, error) {
	if s.getJobs == nil {
		return &clientpb.Jobs{}, nil
	}
	return s.getJobs()
}

func (s *listenerStub) StartDNSListener(_ context.Context, in *clientpb.DNSListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	s.dnsReq = in
	return &clientpb.ListenerJob{JobID: 1, Type: "dns"}, nil
}

func (s *listenerStub) StartMTLSListener(_ context.Context, in *clientpb.MTLSListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	s.mtlsReq = in
	return &clientpb.ListenerJob{JobID: 2, Type: "mtls"}, nil
}

func (s *listenerStub) StartWGListener(_ context.Context, in *clientpb.WGListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	s.wgReq = in
	return &clientpb.ListenerJob{JobID: 3, Type: "wg"}, nil
}

func (s *listenerStub) StartHTTPListener(_ context.Context, in *clientpb.HTTPListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	s.httpReq = in
	return &clientpb.ListenerJob{JobID: 4, Type: "http"}, nil
}

// The regression: the port must reach the server.
func TestStartListenerPassesTheDNSPort(t *testing.T) {
	isolateListenerSites(t)
	stub := &listenerStub{}
	c := &Client{RPC: stub}

	if _, err := c.StartListener("dns", "c2.example.com", 9053, false, "", "", ""); err != nil {
		t.Fatalf("StartListener: %v", err)
	}
	if stub.dnsReq == nil {
		t.Fatal("StartDNSListener was never called")
	}
	if stub.dnsReq.GetPort() != 9053 {
		t.Errorf("Port = %d, want 9053: without it the server binds its own default "+
			"and the listener silently never receives anything", stub.dnsReq.GetPort())
	}
	if stub.dnsReq.GetHost() == "" {
		t.Error("Host was not set, so the listener would bind every interface")
	}
	if len(stub.dnsReq.GetDomains()) != 1 || stub.dnsReq.GetDomains()[0] != "c2.example.com" {
		t.Errorf("Domains = %v, want the configured domain", stub.dnsReq.GetDomains())
	}
}

// The other transports already carried their port; this keeps them that way.
func TestStartListenerPassesPortsForEveryTransport(t *testing.T) {
	cases := []struct {
		kind string
		port uint32
		get  func(*listenerStub) uint32
	}{
		{"mtls", 9001, func(s *listenerStub) uint32 { return s.mtlsReq.GetPort() }},
		{"http", 9002, func(s *listenerStub) uint32 { return s.httpReq.GetPort() }},
		{"https", 9003, func(s *listenerStub) uint32 { return s.httpReq.GetPort() }},
		{"wireguard", 9004, func(s *listenerStub) uint32 { return s.wgReq.GetPort() }},
		{"dns", 9053, func(s *listenerStub) uint32 { return s.dnsReq.GetPort() }},
	}
	for _, tc := range cases {
		stub := &listenerStub{}
		c := &Client{RPC: stub}
		if _, err := c.StartListener(tc.kind, "127.0.0.1", tc.port, false, "", "", ""); err != nil {
			t.Errorf("%s: StartListener: %v", tc.kind, err)
			continue
		}
		if got := tc.get(stub); got != tc.port {
			t.Errorf("%s: port = %d, want %d", tc.kind, got, tc.port)
		}
	}
}

// A second start on the same port is what a double click, or a retry after a
// slow response, produces. It used to fail at the bind with an error that named
// neither the listener holding the port nor the fact that the request was
// already satisfied; now the running listener is handed back instead.
func TestStartListenerReusesAListenerAlreadyOnThePort(t *testing.T) {
	isolateListenerSites(t)
	stub := &listenerStub{getJobs: func() (*clientpb.Jobs, error) {
		return &clientpb.Jobs{Active: []*clientpb.Job{
			{ID: 7, Name: "http", Protocol: "tcp", Port: 8080},
		}}, nil
	}}
	c := &Client{RPC: stub}
	id, err := c.StartListener("http", "0.0.0.0", 8080, false, "webdelivery", "c2.example.com", "")
	if err != nil {
		t.Fatalf("StartListener: %v", err)
	}
	if id != 7 {
		t.Errorf("job id = %d, want the running listener 7", id)
	}
	if stub.httpReq != nil {
		t.Error("a second HTTP listener was started on a port that is already bound")
	}
}

// A listener of a different kind on the same port is not the one that was
// asked for, so it must not be handed back. Both report "tcp", which is why the
// match is on the job name and not the protocol.
func TestStartListenerDoesNotReuseADifferentKind(t *testing.T) {
	isolateListenerSites(t)
	stub := &listenerStub{getJobs: func() (*clientpb.Jobs, error) {
		return &clientpb.Jobs{Active: []*clientpb.Job{
			{ID: 7, Name: "mtls", Protocol: "tcp", Port: 8080},
		}}, nil
	}}
	c := &Client{RPC: stub}
	if _, err := c.StartListener("http", "0.0.0.0", 8080, false, "webdelivery", "", ""); err != nil {
		t.Fatalf("StartListener: %v", err)
	}
	if stub.httpReq == nil {
		t.Error("the HTTP listener was skipped because an unrelated mTLS listener shares the port")
	}
}

// An unknown transport is a caller error and must be reported as one, not
// silently turned into a default listener.
func TestStartListenerRejectsAnUnknownTransport(t *testing.T) {
	c := &Client{RPC: &listenerStub{}}
	if _, err := c.StartListener("carrier-pigeon", "127.0.0.1", 1, false, "", "", ""); err == nil {
		t.Error("an unknown transport was accepted")
	}
}

var _ = commonpb.Empty{}
