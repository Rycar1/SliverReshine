package api

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/websocket"
	"google.golang.org/grpc"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"

	"c2tool/internal/ui/sliver"
)

// ---------------------------------------------------------------------------
// harness: a scripted RPC surface plus a real WebSocket pair
// ---------------------------------------------------------------------------

// fakeTunnelStream stands in for the bidirectional TunnelData stream. Recv
// returns whatever the test feeds it; Send records what the console wrote.
type fakeTunnelStream struct {
	grpc.ClientStream

	recv chan *sliverpb.TunnelData
	sent chan *sliverpb.TunnelData
	once sync.Once
}

func newFakeTunnelStream() *fakeTunnelStream {
	return &fakeTunnelStream{
		recv: make(chan *sliverpb.TunnelData, 16),
		sent: make(chan *sliverpb.TunnelData, 64),
	}
}

func (s *fakeTunnelStream) Send(td *sliverpb.TunnelData) error {
	select {
	case s.sent <- td:
	default:
	}
	return nil
}

func (s *fakeTunnelStream) Recv() (*sliverpb.TunnelData, error) {
	td, ok := <-s.recv
	if !ok {
		return nil, io.EOF
	}
	return td, nil
}

// Close unblocks Recv, which is how the manager's loop goroutine exits.
func (s *fakeTunnelStream) Close() { s.once.Do(func() { close(s.recv) }) }

// terminalStub is a SliverRPCClient with just the tunnel surface scripted.
type terminalStub struct {
	rpcpb.SliverRPCClient

	stream *fakeTunnelStream

	mu       sync.Mutex
	resizes  []*sliverpb.ShellResizeReq
	shells   []*sliverpb.ShellReq
	sessions *clientpb.Sessions
	sessErr  error
}

func (s *terminalStub) TunnelData(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[sliverpb.TunnelData, sliverpb.TunnelData], error) {
	return s.stream, nil
}

func (s *terminalStub) CreateTunnel(context.Context, *sliverpb.Tunnel, ...grpc.CallOption) (*sliverpb.Tunnel, error) {
	return &sliverpb.Tunnel{TunnelID: 1}, nil
}

func (s *terminalStub) Shell(_ context.Context, in *sliverpb.ShellReq, _ ...grpc.CallOption) (*sliverpb.Shell, error) {
	s.mu.Lock()
	s.shells = append(s.shells, in)
	s.mu.Unlock()
	return &sliverpb.Shell{}, nil
}

func (s *terminalStub) ShellResize(_ context.Context, in *sliverpb.ShellResizeReq, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	s.mu.Lock()
	s.resizes = append(s.resizes, in)
	s.mu.Unlock()
	return &commonpb.Empty{}, nil
}

func (s *terminalStub) GetSessions(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.Sessions, error) {
	if s.sessErr != nil {
		return nil, s.sessErr
	}
	if s.sessions == nil {
		return &clientpb.Sessions{}, nil
	}
	return s.sessions, nil
}

func (s *terminalStub) resizesRecorded() []*sliverpb.ShellResizeReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*sliverpb.ShellResizeReq(nil), s.resizes...)
}

// newWSPair returns the two ends of one real WebSocket connection. The server
// end is handed to the test so the bridge functions can be driven directly.
func newWSPair(t *testing.T) (server, browser *websocket.Conn) {
	t.Helper()

	conns := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		ws.PayloadType = websocket.BinaryFrame
		conns <- ws
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	addr := strings.TrimPrefix(srv.URL, "http://")
	browser, err := websocket.Dial("ws://"+addr, "", "http://"+addr)
	if err != nil {
		t.Fatalf("websocket.Dial: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })

	select {
	case server = <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("the server side of the websocket pair never arrived")
	}
	return server, browser
}

// newTerminalBridge builds the objects runTerminal wires together: a tunnel
// manager over a scripted stream, one tunnel, and a WebSocket pair.
func newTerminalBridge(t *testing.T) (server, browser *websocket.Conn, tm *sliver.TunnelManager, tunnel *sliver.TunnelIO, stub *terminalStub) {
	t.Helper()

	stub = &terminalStub{stream: newFakeTunnelStream()}
	tm, err := sliver.NewTunnelManager(&sliver.Client{RPC: stub})
	if err != nil {
		t.Fatalf("NewTunnelManager: %v", err)
	}
	tunnel, err = tm.CreateTunnel("s1")
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}

	server, browser = newWSPair(t)
	t.Cleanup(func() {
		stub.stream.Close()
		tm.Close()
	})
	return server, browser, tm, tunnel, stub
}

// nextTunnelData waits for a TunnelData carrying want, skipping the empty
// binding message CreateTunnel sends.
func nextTunnelData(t *testing.T, ch <-chan *sliverpb.TunnelData, want string) *sliverpb.TunnelData {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case td := <-ch:
			if string(td.GetData()) == want {
				return td
			}
		case <-deadline:
			return nil
		}
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the condition never became true")
}

// ---------------------------------------------------------------------------
// writeWS
// ---------------------------------------------------------------------------

func TestWriteWSFramesAMessage(t *testing.T) {
	server, browser, _, _, _ := newTerminalBridge(t)

	if err := writeWS(server, wsMsgData, []byte("hello")); err != nil {
		t.Fatalf("writeWS: %v", err)
	}
	_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))

	msgType, payload, err := nextFrame(newWSFrameReader(browser))
	if err != nil {
		t.Fatalf("nextFrame: %v", err)
	}
	if msgType != wsMsgData || string(payload) != "hello" {
		t.Errorf("got (0x%02x, %q), want (0x%02x, %q)", msgType, payload, wsMsgData, "hello")
	}
}

// ---------------------------------------------------------------------------
// terminalTargetProfile
// ---------------------------------------------------------------------------

func TestTerminalTargetProfileReadsTheSessionOS(t *testing.T) {
	cases := []struct {
		name        string
		sessions    []*clientpb.Session
		sessErr     error
		wantPTY     bool
		wantWindows bool
	}{
		{"linux gets a PTY", []*clientpb.Session{{ID: "s1", OS: "linux"}}, nil, true, false},
		{"darwin gets a PTY", []*clientpb.Session{{ID: "s1", OS: "darwin"}}, nil, true, false},
		{"windows gets the keystroke rewrite", []*clientpb.Session{{ID: "s1", OS: "windows"}}, nil, false, true},
		{"an unknown OS gets neither", []*clientpb.Session{{ID: "s1", OS: "freebsd"}}, nil, false, false},
		{"another session is ignored", []*clientpb.Session{{ID: "other", OS: "linux"}}, nil, false, false},
		{"a listing failure is conservative", nil, errors.New("rpc down"), false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &terminalStub{sessions: &clientpb.Sessions{Sessions: tc.sessions}, sessErr: tc.sessErr}
			pty, win := terminalTargetProfile(&sliver.Client{RPC: stub}, "s1")
			if pty != tc.wantPTY || win != tc.wantWindows {
				t.Errorf("terminalTargetProfile = (%v, %v), want (%v, %v)", pty, win, tc.wantPTY, tc.wantWindows)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// applyResize
// ---------------------------------------------------------------------------

func TestApplyResizeForwardsTheFrame(t *testing.T) {
	_, _, tm, tunnel, stub := newTerminalBridge(t)

	applyResize(tm, "s1", tunnel.ID, []byte(`{"cols":100,"rows":40}`))

	got := stub.resizesRecorded()
	if len(got) != 1 {
		t.Fatalf("resizes recorded = %d, want 1", len(got))
	}
	if got[0].GetRows() != 40 || got[0].GetCols() != 100 {
		t.Errorf("resize = rows %d, cols %d, want 40/100", got[0].GetRows(), got[0].GetCols())
	}
	if got[0].GetTunnelID() != tunnel.ID {
		t.Errorf("resize tunnel = %d, want %d", got[0].GetTunnelID(), tunnel.ID)
	}
	if got[0].GetRequest().GetSessionID() != "s1" {
		t.Errorf("resize session = %q, want %q", got[0].GetRequest().GetSessionID(), "s1")
	}
}

// A resize frame is attacker-reachable, so out-of-range values must be clamped
// to the wire type rather than wrapping, and nonsense must be dropped.
func TestApplyResizeClampsAndDropsBadFrames(t *testing.T) {
	_, _, tm, tunnel, stub := newTerminalBridge(t)

	applyResize(tm, "s1", tunnel.ID, []byte(`{"cols":70000,"rows":70000}`))
	applyResize(tm, "s1", tunnel.ID, []byte(`{"cols":0,"rows":40}`))
	applyResize(tm, "s1", tunnel.ID, []byte(`{"cols":-5,"rows":40}`))
	applyResize(tm, "s1", tunnel.ID, []byte(`{"cols":100}`))
	applyResize(tm, "s1", tunnel.ID, []byte(`not json`))

	got := stub.resizesRecorded()
	if len(got) != 1 {
		t.Fatalf("resizes recorded = %d, want only the clamped frame: %v", len(got), got)
	}
	if got[0].GetRows() != 0xffff || got[0].GetCols() != 0xffff {
		t.Errorf("clamped resize = rows %d, cols %d, want 65535/65535", got[0].GetRows(), got[0].GetCols())
	}
}

// ---------------------------------------------------------------------------
// pumpWSToTunnel
// ---------------------------------------------------------------------------

func TestPumpWSToTunnelForwardsKeystrokesResizesAndClose(t *testing.T) {
	server, browser, tm, tunnel, stub := newTerminalBridge(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		pumpWSToTunnel(server, tm, tunnel, "s1", nil, false)
	}()

	if _, err := browser.Write(buildFrame(wsMsgData, []byte("whoami\n"))); err != nil {
		t.Fatalf("write data frame: %v", err)
	}
	if nextTunnelData(t, stub.stream.sent, "whoami\n") == nil {
		t.Fatal("the keystrokes never reached the tunnel")
	}

	if _, err := browser.Write(buildFrame(wsMsgResize, []byte(`{"cols":120,"rows":50}`))); err != nil {
		t.Fatalf("write resize frame: %v", err)
	}
	waitFor(t, func() bool { return len(stub.resizesRecorded()) == 1 })
	if got := stub.resizesRecorded()[0]; got.GetRows() != 50 || got.GetCols() != 120 {
		t.Errorf("resize = rows %d, cols %d, want 50/120", got.GetRows(), got.GetCols())
	}

	if _, err := browser.Write(buildFrame(wsMsgClose, nil)); err != nil {
		t.Fatalf("write close frame: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pumpWSToTunnel did not return after a close frame")
	}
	if nextTunnelData(t, stub.stream.sent, "exit\n") == nil {
		t.Error("a close frame did not send \"exit\" to the shell")
	}
}

// A Windows session running over a pipe needs DEL turned into BS and a bare CR
// expanded, or the command line is corrupted on the target.
func TestPumpWSToTunnelRewritesKeystrokesForWindows(t *testing.T) {
	server, browser, tm, tunnel, stub := newTerminalBridge(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		pumpWSToTunnel(server, tm, tunnel, "s1", nil, true)
	}()
	t.Cleanup(func() {
		_ = browser.Close()
		<-done
	})

	payload := append([]byte("dir\r"), 0x7f)
	if _, err := browser.Write(buildFrame(wsMsgData, payload)); err != nil {
		t.Fatalf("write data frame: %v", err)
	}
	if nextTunnelData(t, stub.stream.sent, "dir\r\n\x08") == nil {
		t.Fatal("the Windows keystroke rewrite did not reach the tunnel")
	}
}

// ---------------------------------------------------------------------------
// forwardTunnelToWS
// ---------------------------------------------------------------------------

func TestForwardTunnelToWSStreamsOutputThenCloses(t *testing.T) {
	server, browser, _, tunnel, stub := newTerminalBridge(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		forwardTunnelToWS(server, tunnel, nil)
	}()

	_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))

	stub.stream.recv <- &sliverpb.TunnelData{TunnelID: tunnel.ID, SessionID: "s1", Data: []byte("out")}
	msgType, payload, err := nextFrame(newWSFrameReader(browser))
	if err != nil {
		t.Fatalf("nextFrame: %v", err)
	}
	if msgType != wsMsgData || string(payload) != "out" {
		t.Errorf("got (0x%02x, %q), want (0x%02x, %q)", msgType, payload, wsMsgData, "out")
	}

	stub.stream.Close()
	msgType, payload, err = nextFrame(newWSFrameReader(browser))
	if err != nil {
		t.Fatalf("nextFrame after the tunnel ended: %v", err)
	}
	if msgType != wsMsgClose || len(payload) != 0 {
		t.Errorf("got (0x%02x, %q), want an empty close frame", msgType, payload)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwardTunnelToWS did not return when the tunnel ended")
	}
}
