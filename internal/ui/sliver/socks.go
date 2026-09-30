package sliver

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// SocksProxyView is the JSON shape of an active SOCKS5 proxy.
type SocksProxyView struct {
	ID        uint64 `json:"ID"`
	SessionID string `json:"SessionID"`
	BindAddr  string `json:"BindAddr"`
	BindPort  uint32 `json:"BindPort"`
	Username  string `json:"Username"`
	Password  string `json:"Password"`
}

// SocksProxy is a local TCP listener tunnelling raw bytes through a session.
// The implant side runs the actual SOCKS5 server, so this side only relays.
type SocksProxy struct {
	ID        uint64
	SessionID string
	BindAddr  string
	BindPort  uint32
	Username  string
	Password  string

	mgr      *SocksManager
	listener net.Listener
	stream   rpcpb.SliverRPC_SocksProxyClient
	cancel   context.CancelFunc

	mu        sync.Mutex
	conns     map[uint64]net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

// SocksManager manages active SOCKS5 proxies for a client connection.
type SocksManager struct {
	client   *Client
	mu       sync.Mutex
	proxies  map[uint64]*SocksProxy
	nextID   atomic.Uint64
}

// NewSocksManager creates an empty manager bound to the given client.
func NewSocksManager(client *Client) *SocksManager {
	return &SocksManager{
		client:  client,
		proxies: map[uint64]*SocksProxy{},
	}
}

// List returns all active SOCKS5 proxies.
func (sm *SocksManager) List() []SocksProxyView {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	out := make([]SocksProxyView, 0, len(sm.proxies))
	for _, p := range sm.proxies {
		out = append(out, SocksProxyView{
			ID:        p.ID,
			SessionID: p.SessionID,
			BindAddr:  p.BindAddr,
			BindPort:  p.BindPort,
			Username:  p.Username,
			Password:  p.Password,
		})
	}
	return out
}

// Start opens a SOCKS5 proxy listener bound to bindAddr:bindPort.
func (sm *SocksManager) Start(sessionID, bindAddr string, bindPort uint32, username, password string) (*SocksProxy, error) {
	if sessionID == "" {
		return nil, &socksError{msg: "session id is required"}
	}
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(bindAddr, itoa(bindPort)))
	if err != nil {
		return nil, err
	}
	localPort := bindPort
	if localPort == 0 {
		localPort = uint32(listener.Addr().(*net.TCPAddr).Port)
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := sm.client.RPC.SocksProxy(ctx)
	if err != nil {
		cancel()
		listener.Close()
		return nil, err
	}

	p := &SocksProxy{
		ID:        sm.nextID.Add(1),
		SessionID: sessionID,
		BindAddr:  bindAddr,
		BindPort:  localPort,
		Username:  username,
		Password:  password,
		mgr:       sm,
		listener:  listener,
		stream:    stream,
		cancel:    cancel,
		conns:     map[uint64]net.Conn{},
		done:      make(chan struct{}),
	}

	sm.mu.Lock()
	sm.proxies[p.ID] = p
	sm.mu.Unlock()

	go p.recvLoop()
	go p.acceptLoop()
	return p, nil
}

// Stop stops a proxy by ID.
func (sm *SocksManager) Stop(id uint64) error {
	sm.mu.Lock()
	p, ok := sm.proxies[id]
	delete(sm.proxies, id)
	sm.mu.Unlock()
	if !ok {
		return &socksError{msg: "no SOCKS proxy with that ID"}
	}
	p.close()
	return nil
}

// Close stops all proxies.
func (sm *SocksManager) Close() {
	sm.mu.Lock()
	proxies := make([]*SocksProxy, 0, len(sm.proxies))
	for _, p := range sm.proxies {
		proxies = append(proxies, p)
	}
	sm.proxies = map[uint64]*SocksProxy{}
	sm.mu.Unlock()
	for _, p := range proxies {
		p.close()
	}
}

func (p *SocksProxy) acceptLoop() {
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		go p.handleConn(conn)
	}
}

func (p *SocksProxy) handleConn(conn net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	socks, err := p.mgr.client.RPC.CreateSocks(ctx, &sliverpb.Socks{SessionID: p.SessionID})
	if err != nil {
		conn.Close()
		return
	}

	p.mu.Lock()
	p.conns[socks.TunnelID] = conn
	p.mu.Unlock()

	frame := &sliverpb.SocksData{
		TunnelID: socks.TunnelID,
		Request:  &commonpb.Request{SessionID: p.SessionID},
	}
	if p.Username != "" {
		frame.Username = p.Username
		frame.Password = p.Password
	}

	defer func() {
		p.mu.Lock()
		delete(p.conns, socks.TunnelID)
		p.mu.Unlock()
		_ = p.stream.Send(&sliverpb.SocksData{
			TunnelID:  socks.TunnelID,
			CloseConn: true,
			Request:   &commonpb.Request{SessionID: p.SessionID},
		})
		conn.Close()
	}()

	buf := make([]byte, 8192)
	var seq uint64
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			frame.Data = buf[:n]
			frame.Sequence = seq
			seq++
			if serr := p.stream.Send(frame); serr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// recvLoop drains the SocksProxy stream and writes data back to local conns.
func (p *SocksProxy) recvLoop() {
	for {
		msg, err := p.stream.Recv()
		if err != nil {
			p.close()
			return
		}
		if msg == nil {
			continue
		}
		p.mu.Lock()
		conn := p.conns[msg.TunnelID]
		p.mu.Unlock()
		if conn == nil {
			continue
		}
		if msg.CloseConn {
			conn.Close()
			continue
		}
		if len(msg.Data) > 0 {
			if _, werr := conn.Write(msg.Data); werr != nil {
				conn.Close()
			}
		}
	}
}

func (p *SocksProxy) close() {
	p.closeOnce.Do(func() {
		if p.listener != nil {
			_ = p.listener.Close()
		}
		if p.stream != nil {
			_ = p.stream.CloseSend()
		}
		if p.cancel != nil {
			p.cancel()
		}
		close(p.done)
		p.mu.Lock()
		for _, c := range p.conns {
			c.Close()
		}
		p.conns = map[uint64]net.Conn{}
		p.mu.Unlock()
	})
}

type socksError struct {
	msg string
}

func (e *socksError) Error() string { return e.msg }
