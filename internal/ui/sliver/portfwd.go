package sliver

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

const (
	tcpProtocol = int32(6) // IPPROTO_TCP
)

// PortForwardView is the JSON shape of an active port forward.
type PortForwardView struct {
	LocalAddr string `json:"LocalAddr"`
	LocalPort uint32 `json:"LocalPort"`
	Host      string `json:"Host"`
	Port      uint32 `json:"Port"`
	SessionID string `json:"SessionID"`
}

// PortForward represents a local listener forwarding to a remote target through a session.
type PortForward struct {
	LocalAddr string
	LocalPort uint32
	Host      string
	Port      uint32
	SessionID string

	mgr *PortForwardManager

	listener net.Listener

	mu       sync.Mutex
	tunnels  []*TunnelIO
	done     chan struct{}
	closeOne sync.Once
}

// PortForwardManager manages active port forwards for a client connection.
type PortForwardManager struct {
	client   *Client
	tm       *TunnelManager
	mu       sync.Mutex
	forwards map[uint32]*PortForward
}

// NewPortForwardManager creates a manager bound to the given client.
func NewPortForwardManager(client *Client) (*PortForwardManager, error) {
	tm, err := NewTunnelManager(client)
	if err != nil {
		return nil, err
	}
	return &PortForwardManager{
		client:   client,
		tm:       tm,
		forwards: map[uint32]*PortForward{},
	}, nil
}

// List returns all active port forwards.
func (pfm *PortForwardManager) List() []PortForwardView {
	pfm.mu.Lock()
	defer pfm.mu.Unlock()
	out := make([]PortForwardView, 0, len(pfm.forwards))
	for _, pf := range pfm.forwards {
		out = append(out, PortForwardView{
			LocalAddr: pf.LocalAddr,
			LocalPort: pf.LocalPort,
			Host:      pf.Host,
			Port:      pf.Port,
			SessionID: pf.SessionID,
		})
	}
	return out
}

// Forward starts a local listener and forwards connections to host:port through sessionID.
func (pfm *PortForwardManager) Forward(sessionID, bindAddr string, bindPort, remotePort uint32, remoteHost string) (*PortForward, error) {
	if remoteHost == "" {
		remoteHost = "127.0.0.1"
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

	pf := &PortForward{
		LocalAddr: bindAddr,
		LocalPort: localPort,
		Host:      remoteHost,
		Port:      remotePort,
		SessionID: sessionID,
		mgr:       pfm,
		listener:  listener,
		tunnels:   []*TunnelIO{},
		done:      make(chan struct{}),
	}

	pfm.mu.Lock()
	if _, exists := pfm.forwards[localPort]; exists {
		pfm.mu.Unlock()
		listener.Close()
		return nil, &forwardExistsError{Port: localPort}
	}
	pfm.forwards[localPort] = pf
	pfm.mu.Unlock()

	go pf.acceptLoop()
	return pf, nil
}

// Stop stops a forward by local port.
func (pfm *PortForwardManager) Stop(localPort uint32) error {
	pfm.mu.Lock()
	pf, ok := pfm.forwards[localPort]
	delete(pfm.forwards, localPort)
	pfm.mu.Unlock()
	if !ok {
		return &forwardNotFoundError{Port: localPort}
	}
	pf.close()
	return nil
}

// Close stops all forwards and the shared tunnel stream.
func (pfm *PortForwardManager) Close() {
	pfm.mu.Lock()
	forwards := make([]*PortForward, 0, len(pfm.forwards))
	for _, pf := range pfm.forwards {
		forwards = append(forwards, pf)
	}
	pfm.forwards = map[uint32]*PortForward{}
	pfm.mu.Unlock()
	for _, pf := range forwards {
		pf.close()
	}
	pfm.tm.Close()
}

func (pf *PortForward) acceptLoop() {
	for {
		conn, err := pf.listener.Accept()
		if err != nil {
			return
		}
		go pf.handleConn(conn)
	}
}

func (pf *PortForward) handleConn(conn net.Conn) {
	defer conn.Close()

	tunnel, err := pf.mgr.tm.CreateTunnel(pf.SessionID)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = pf.mgr.client.RPC.Portfwd(ctx, &sliverpb.PortfwdReq{
		Port:      pf.Port,
		Protocol:  tcpProtocol,
		Host:      pf.Host,
		TunnelID:  tunnel.ID,
		Request:   &commonpb.Request{SessionID: pf.SessionID},
	})
	if err != nil {
		tunnel.close()
		return
	}

	pf.mu.Lock()
	pf.tunnels = append(pf.tunnels, tunnel)
	pf.mu.Unlock()

	go func() {
		_, _ = io.Copy(tunnel, conn)
		tunnel.close()
	}()
	_, _ = io.Copy(conn, tunnel)

	pf.mu.Lock()
	for i, t := range pf.tunnels {
		if t == tunnel {
			pf.tunnels = append(pf.tunnels[:i], pf.tunnels[i+1:]...)
			break
		}
	}
	pf.mu.Unlock()
}

func (pf *PortForward) close() {
	pf.closeOne.Do(func() {
		if pf.listener != nil {
			_ = pf.listener.Close()
		}
		close(pf.done)
		pf.mu.Lock()
		for _, t := range pf.tunnels {
			t.close()
		}
		pf.tunnels = nil
		pf.mu.Unlock()
	})
}

type forwardExistsError struct {
	Port uint32
}

func (e *forwardExistsError) Error() string {
	return "a forward already exists on local port " + itoa(e.Port)
}

type forwardNotFoundError struct {
	Port uint32
}

func (e *forwardNotFoundError) Error() string {
	return "no forward on local port " + itoa(e.Port)
}

func itoa(v uint32) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
