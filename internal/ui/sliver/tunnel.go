package sliver

import (
	"bytes"
	"context"
	"io"
	"sync"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// TunnelIO bridges a Sliver RPC tunnel to io.Reader/io.Writer semantics.
type TunnelIO struct {
	ID        uint64
	SessionID string

	send chan []byte

	mu     sync.Mutex
	buf    bytes.Buffer
	cond   *sync.Cond
	closed bool

	closeOnce sync.Once
	closedCh  chan struct{}
}

// TunnelManager holds the single shared TunnelData stream and maps tunnel IDs
// to active tunnel I/O objects.
type TunnelManager struct {
	client *Client

	cancel context.CancelFunc

	mu       sync.RWMutex
	tunnels  map[uint64]*TunnelIO
	stream   rpcpb.SliverRPC_TunnelDataClient
	streamMu sync.Mutex
}

// NewTunnelManager starts the shared tunnel data loop for a client connection.
func NewTunnelManager(client *Client) (*TunnelManager, error) {
	tm := &TunnelManager{
		client:  client,
		tunnels: map[uint64]*TunnelIO{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := client.RPC.TunnelData(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	tm.stream = stream
	tm.cancel = cancel

	go tm.loop()
	return tm, nil
}

func (tm *TunnelManager) loop() {
	for {
		msg, err := tm.stream.Recv()
		if err != nil {
			if err != io.EOF {
				tm.closeAll()
			}
			return
		}
		if msg == nil {
			continue
		}
		tm.mu.RLock()
		tunnel := tm.tunnels[msg.TunnelID]
		tm.mu.RUnlock()
		if tunnel == nil {
			continue
		}
		if msg.Closed {
			tunnel.close()
			continue
		}
		tunnel.push(msg.Data)
	}
}

// send writes a TunnelData message to the shared stream. Only one writer at a time.
func (tm *TunnelManager) send(td *sliverpb.TunnelData) error {
	tm.streamMu.Lock()
	defer tm.streamMu.Unlock()
	return tm.stream.Send(td)
}

// CreateTunnel creates an RPC tunnel and binds it to a local TunnelIO.
func (tm *TunnelManager) CreateTunnel(sessionID string) (*TunnelIO, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t, err := tm.client.RPC.CreateTunnel(ctx, &sliverpb.Tunnel{SessionID: sessionID})
	if err != nil {
		return nil, err
	}
	tunnel := &TunnelIO{
		ID:        t.TunnelID,
		SessionID: sessionID,
		send:      make(chan []byte, 64),
		closedCh:  make(chan struct{}),
	}
	tunnel.cond = sync.NewCond(&tunnel.mu)
	tm.mu.Lock()
	tm.tunnels[tunnel.ID] = tunnel
	tm.mu.Unlock()

	// Bind goroutine forwards tunnel.send channel to the gRPC stream.
	go func() {
		for {
			select {
			case data := <-tunnel.send:
				if err := tm.send(&sliverpb.TunnelData{
					TunnelID:  tunnel.ID,
					SessionID: tunnel.SessionID,
					Data:      data,
				}); err != nil {
					tunnel.close()
					return
				}
			case <-tunnel.closedCh:
				return
			}
		}
	}()

	// Send empty message to bind client to the tunnel.
	tunnel.send <- []byte{}
	return tunnel, nil
}

// StartShell creates a tunnel and binds a shell to it for the given session.
// enablePTY is only valid on linux/darwin sessions.
func (tm *TunnelManager) StartShell(sessionID string, enablePTY bool) (*TunnelIO, error) {
	tunnel, err := tm.CreateTunnel(sessionID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = tm.client.RPC.Shell(ctx, &sliverpb.ShellReq{
		EnablePTY: enablePTY,
		TunnelID:  tunnel.ID,
		Request: &commonpb.Request{
			SessionID: sessionID,
		},
	})
	if err != nil {
		tunnel.close()
		return nil, err
	}
	return tunnel, nil
}

// Close closes the stream and all tunnels.
func (tm *TunnelManager) Close() {
	if tm.cancel != nil {
		tm.cancel()
	}
	tm.closeAll()
}

func (tm *TunnelManager) closeAll() {
	tm.mu.RLock()
	tunnels := make([]*TunnelIO, 0, len(tm.tunnels))
	for _, t := range tm.tunnels {
		tunnels = append(tunnels, t)
	}
	tm.mu.RUnlock()
	for _, t := range tunnels {
		t.close()
	}
}

// maxTunnelBuffer bounds the read buffer so a stalled consumer cannot grow it
// without limit. Output beyond the cap is dropped (preferred to blocking the
// shared tunnel stream for all tunnels).
const maxTunnelBuffer = 8 << 20

// push appends implant data to the read buffer and wakes blocked readers.
func (t *TunnelIO) push(data []byte) {
	if len(data) == 0 {
		return
	}
	t.mu.Lock()
	if t.buf.Len()+len(data) > maxTunnelBuffer {
		t.mu.Unlock()
		return
	}
	t.buf.Write(data)
	t.cond.Broadcast()
	t.mu.Unlock()
}

// Read implements io.Reader (implant -> local). Data is buffered so a single
// tunnel message larger than the caller's buffer is never dropped.
func (t *TunnelIO) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.buf.Len() == 0 {
		if t.closed {
			return 0, io.EOF
		}
		t.cond.Wait()
	}
	return t.buf.Read(p)
}

// Write implements io.Writer (local -> implant).
func (t *TunnelIO) Write(p []byte) (int, error) {
	select {
	case t.send <- p:
		return len(p), nil
	case <-t.closedCh:
		return 0, io.EOF
	}
}

func (t *TunnelIO) close() {
	t.closeOnce.Do(func() {
		close(t.closedCh)
		t.mu.Lock()
		t.closed = true
		t.cond.Broadcast()
		t.mu.Unlock()
	})
}
