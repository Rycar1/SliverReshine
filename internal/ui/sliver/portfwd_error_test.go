package sliver

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// Port forwarding has two error channels and the console used to read only one.
//
// The gRPC error covers the transport. A target that refuses the connection is
// not a transport failure: the implant dials, fails, and answers with a normal
// reply whose Response.Err says so. The server's Portfwd handler does not route
// through GenericHandler -- unlike the shell and filesystem RPCs -- so nothing
// converts that field into a gRPC status. It reaches the console intact and is
// only visible if the console looks.
//
// It did not look. handleConn discarded the reply with `_`, so a refused target
// took the same path as a working one: the tunnel was registered as open and the
// browser was left holding a TCP connection nothing would ever write to. The
// operator saw a port forward that reported success and delivered nothing, with
// no error anywhere to explain it.
//
// These tests pin the reply-inspection down from both sides, so a regression to
// `_, err = ...` fails here rather than in the field.

// fakeTunnelStream satisfies the shared tunnel stream. Only Send is reached by
// CreateTunnel's bind goroutine; the embedded interface leaves every other
// method panicking so an unexpected call fails loudly instead of silently.
type fakeTunnelStream struct {
	rpcpb.SliverRPC_TunnelDataClient

	mu   sync.Mutex
	sent []*sliverpb.TunnelData
}

func (f *fakeTunnelStream) Send(msg *sliverpb.TunnelData) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, msg)
	return nil
}

// portfwdStub drives the two RPCs handleConn touches.
type portfwdStub struct {
	rpcStub

	portfwd func(*sliverpb.PortfwdReq) (*sliverpb.Portfwd, error)

	mu         sync.Mutex
	portfwdGot []*sliverpb.PortfwdReq
}

func (s *portfwdStub) CreateTunnel(_ context.Context, _ *sliverpb.Tunnel, _ ...grpc.CallOption) (*sliverpb.Tunnel, error) {
	return &sliverpb.Tunnel{TunnelID: 7, SessionID: "s-1"}, nil
}

func (s *portfwdStub) Portfwd(_ context.Context, in *sliverpb.PortfwdReq, _ ...grpc.CallOption) (*sliverpb.Portfwd, error) {
	s.mu.Lock()
	s.portfwdGot = append(s.portfwdGot, in)
	s.mu.Unlock()
	if s.portfwd == nil {
		return nil, errors.New("Portfwd is not expected in this test")
	}
	return s.portfwd(in)
}

func (s *portfwdStub) portfwdCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.portfwdGot)
}

// newPortForwardForTest builds a forward wired to a stub, bypassing Forward so
// no local listener is opened. The listener is not what these tests exercise.
func newPortForwardForTest(stub *portfwdStub) *PortForward {
	client := &Client{RPC: stub}
	tm := &TunnelManager{
		client:  client,
		tunnels: map[uint64]*TunnelIO{},
		stream:  &fakeTunnelStream{},
	}
	pfm := &PortForwardManager{
		client:   client,
		tm:       tm,
		forwards: map[uint32]*PortForward{},
	}
	return &PortForward{
		LocalAddr: "127.0.0.1",
		LocalPort: 9001,
		Host:      "127.0.0.1",
		Port:      445,
		SessionID: "s-1",
		mgr:       pfm,
		done:      make(chan struct{}),
	}
}

// TestPortForwardRefusedTargetIsReported is the regression test for the dropped
// reply. A refused target must be recorded, not treated as a working forward.
func TestPortForwardRefusedTargetIsReported(t *testing.T) {
	const refusal = "dial tcp 127.0.0.1:445: connect: connection refused"
	stub := &portfwdStub{
		portfwd: func(*sliverpb.PortfwdReq) (*sliverpb.Portfwd, error) {
			// This is exactly what the implant sends: a successful call whose
			// reply carries the failure.
			return &sliverpb.Portfwd{
				Response: &commonpb.Response{Err: refusal},
			}, nil
		},
	}
	pf := newPortForwardForTest(stub)

	local, peer := net.Pipe()
	defer peer.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pf.handleConn(local)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		// Without the fix the code registers the tunnel and blocks in
		// io.Copy(conn, tunnel), which is the hang the operator saw.
		t.Fatal("handleConn did not return after the target refused the connection")
	}

	pf.mu.Lock()
	gotErr := pf.lastConnErr
	tunnels := len(pf.tunnels)
	pf.mu.Unlock()

	if gotErr == "" {
		t.Fatal("a refused target recorded no error; the reply's Response.Err was dropped")
	}
	if !strings.Contains(gotErr, refusal) {
		t.Errorf("recorded error %q does not carry the implant's message %q", gotErr, refusal)
	}
	if tunnels != 0 {
		t.Errorf("registered %d tunnel(s) for a connection that was never established", tunnels)
	}
	if stub.portfwdCalls() != 1 {
		t.Errorf("Portfwd called %d time(s), want 1", stub.portfwdCalls())
	}
}

// TestPortForwardDialFailureClosesConnection proves the local side is released.
// A browser waiting on a connection that can never carry data is the symptom
// this whole path exists to avoid.
func TestPortForwardDialFailureClosesConnection(t *testing.T) {
	stub := &portfwdStub{
		portfwd: func(*sliverpb.PortfwdReq) (*sliverpb.Portfwd, error) {
			return &sliverpb.Portfwd{
				Response: &commonpb.Response{Err: "dial tcp: i/o timeout"},
			}, nil
		},
	}
	pf := newPortForwardForTest(stub)

	local, peer := net.Pipe()
	defer peer.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pf.handleConn(local)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConn blocked after the implant reported a dial failure")
	}

	// handleConn defers conn.Close(), so the peer must be released rather than
	// left hanging. Reading from a goroutine keeps a regression from stalling
	// the suite: a connection that stays open shows up as a timeout here, and
	// an already-closed pipe shows up as an immediate error.
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, err := peer.Read(buf)
		readDone <- err
	}()

	select {
	case err := <-readDone:
		// A closed pipe returns an error immediately, which is the expected
		// outcome. A successful read would mean the connection is still usable.
		if err == nil {
			t.Fatal("peer read succeeded; the connection was not closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("connection stayed open: the local end was never released")
	}
}

// TestPortForwardSuccessfulReplyIsNotReportedAsFailure guards the other
// direction. The check must key on Response.Err, not on the mere presence of a
// reply, or every working forward would be flagged.
func TestPortForwardSuccessfulReplyIsNotReportedAsFailure(t *testing.T) {
	released := make(chan struct{})
	stub := &portfwdStub{
		portfwd: func(*sliverpb.PortfwdReq) (*sliverpb.Portfwd, error) {
			// Hold the call so the test can observe the state between the reply
			// and the first copy, which is where the tunnel is registered.
			<-released
			return &sliverpb.Portfwd{
				Port:     445,
				Host:     "127.0.0.1",
				Protocol: tcpProtocol,
				TunnelID: 7,
				Response: &commonpb.Response{},
			}, nil
		},
	}
	pf := newPortForwardForTest(stub)

	local, peer := net.Pipe()
	defer peer.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pf.handleConn(local)
	}()

	// Wait until the RPC is in flight, then let it answer.
	deadline := time.Now().Add(2 * time.Second)
	for stub.portfwdCalls() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("Portfwd was never called")
		}
		time.Sleep(time.Millisecond)
	}
	close(released)

	// The tunnel is registered once the reply is accepted.
	deadline = time.Now().Add(2 * time.Second)
	for {
		pf.mu.Lock()
		n := len(pf.tunnels)
		gotErr := pf.lastConnErr
		pf.mu.Unlock()
		if n > 0 {
			break
		}
		if gotErr != "" {
			t.Fatalf("a working forward recorded error %q", gotErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("a successful reply did not register a tunnel")
		}
		time.Sleep(time.Millisecond)
	}

	// Unblock handleConn so it does not outlive the test.
	pf.close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConn did not return after the forward was closed")
	}
}

// TestPortForwardTransportErrorIsRecorded covers the channel that was always
// handled, so the two paths stay distinguishable in the recorded reason.
func TestPortForwardTransportErrorIsRecorded(t *testing.T) {
	stub := &portfwdStub{
		portfwd: func(*sliverpb.PortfwdReq) (*sliverpb.Portfwd, error) {
			return nil, errors.New("rpc error: code = Unavailable desc = connection closed")
		},
	}
	pf := newPortForwardForTest(stub)

	local, peer := net.Pipe()
	defer peer.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		pf.handleConn(local)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleConn blocked after a transport error")
	}

	pf.mu.Lock()
	gotErr := pf.lastConnErr
	pf.mu.Unlock()

	if gotErr == "" {
		t.Fatal("a transport error recorded no reason")
	}
}
