package sliver

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

const (
	tcpProtocol = int32(6) // IPPROTO_TCP
)

// PortForwardView is the JSON shape of an active forward.
type PortForwardView struct {
	LocalAddr string `json:"LocalAddr"`
	LocalPort uint32 `json:"LocalPort"`
	Host      string `json:"Host"`
	Port      uint32 `json:"Port"`
	SessionID string `json:"SessionID"`

	// LastConnErr is the most recent per-connection failure, empty if none.
	//
	// A forward can be up while every connection through it fails, and the
	// difference matters: a refused target means the port is closed, a timeout
	// means it is filtered, and neither is visible from the forward's existence.
	// Reporting it here is what turns the list into something an operator can
	// diagnose from instead of a list of things that claim to be working.
	LastConnErr string `json:"LastConnErr,omitempty"`
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

	// lastConnErr is the most recent per-connection failure, empty if none.
	// It exists so the list view can explain a forward that is up while every
	// connection through it fails.
	lastConnErr string
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
		// Read the recorded error under the same lock that guards it. The
		// accept loop writes it from another goroutine.
		pf.mu.Lock()
		lastErr := pf.lastConnErr
		pf.mu.Unlock()

		out = append(out, PortForwardView{
			LocalAddr:   pf.LocalAddr,
			LocalPort:   pf.LocalPort,
			Host:        pf.Host,
			Port:        pf.Port,
			SessionID:   pf.SessionID,
			LastConnErr: lastErr,
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

// handleConn wires one accepted local connection to a new tunnel on the target.
//
// Every way this can fail is silent by nature: the failure has no HTTP response
// to travel back on, and the browser already sees an open TCP connection that
// simply delivers no data. That is why the failure paths record the reason on
// the forward. Otherwise "the tunnel is up but the target refused" and "the
// target is not answering at all" look identical from the console, and the
// operator is left reading a hung connection with no discriminating detail.
func (pf *PortForward) handleConn(conn net.Conn) {
	defer conn.Close()

	tunnel, err := pf.mgr.tm.CreateTunnel(pf.SessionID)
	if err != nil {
		pf.recordConnError(fmt.Errorf("create tunnel: %w", err))
		return
	}

	ctx, cancel := rpcCtx(rpcDefault)
	defer cancel()

	// The reply is kept because the server's Portfwd handler does not route
	// through GenericHandler. Unlike the shell and filesystem RPCs, it returns
	// the implant's reply verbatim, so a failed dial arrives as Response.Err on
	// an otherwise successful call rather than as a gRPC error. Checking only
	// `err` here meant a target that refused the connection still registered the
	// tunnel as open, and the browser waited on a socket nothing would write to.
	resp, err := pf.mgr.client.RPC.Portfwd(ctx, &sliverpb.PortfwdReq{
		Port:     pf.Port,
		Protocol: tcpProtocol,
		Host:     pf.Host,
		TunnelID: tunnel.ID,
		Request:  &commonpb.Request{SessionID: pf.SessionID},
	})
	if err != nil {
		tunnel.close()
		pf.recordConnError(err)
		return
	}
	if errMsg := resp.GetResponse().GetErr(); errMsg != "" {
		tunnel.close()
		pf.recordConnError(fmt.Errorf("target %s:%d: %s", pf.Host, pf.Port, errMsg))
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

// recordConnError notes the most recent per-connection failure on a forward.
//
// It is deliberately not an error return: the caller is an accept loop serving
// an arbitrary number of connections, and one refused connection must not stop
// the forward. Keeping the last reason is enough for an operator to tell a
// misconfigured target from a filtered one.
func (pf *PortForward) recordConnError(err error) {
	if err == nil {
		return
	}
	pf.mu.Lock()
	pf.lastConnErr = err.Error()
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
