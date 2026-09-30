package c2

/*
	Sliver Implant Framework
	Copyright (C) 2019  Bishop Fox

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU General Public License for more details.

	You should have received a copy of the GNU General Public License
	along with this program.  If not, see <https://www.gnu.org/licenses/>.
*/

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/bishopfox/sliver/client/constants"
	consts "github.com/bishopfox/sliver/client/constants"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/server/certs"
	"github.com/bishopfox/sliver/server/core"
	"github.com/bishopfox/sliver/server/log"
)

// Bind sessions are the reverse of every other transport in this package.
//
// mTLS, WireGuard, HTTP, DNS and the TCP stager all have the implant dial out
// and the C2 accept; the implant is the TLS client and the server is the TLS
// server. A bind session is generated for the case where that is impossible —
// the target cannot reach the internet, or egress is filtered — but the operator
// has some path to a port on the target. So the implant listens and the C2
// dials in.
//
// That inverts the TLS roles: the implant becomes the TLS server and the C2
// becomes the client. No new key material is needed for it. Sliver already
// issues two independent CAs, and each side already holds what the swapped role
// requires:
//
//   - the implant presents its mtls-implant certificate, which this side already
//     verifies client certificates against (ClientCAs below);
//   - the implant verifies this side's mtls-server certificate, which is what
//     the implant's embedded CA certificate signs.
//
// getServerTLSConfig therefore works unchanged as a client configuration: its
// Certificates field is the mtls-server certificate the implant expects to see,
// and its RootCAs pool is the mtls-implant CA the implant's certificate chains
// to. ClientAuth/ClientCAs are server-side fields and are ignored by tls.Dial.
//
// Everything above the TLS layer is untouched. The implant still writes the
// "MUX/1" yamux preface and still speaks the same envelope framing, so a bind
// connection is handed to the same handleSliverConnection the accept path uses
// and registers a session the same way.

// bindLog is kept separate from mtlsLog so a bind session is distinguishable in
// the server log; the failure modes are different enough (a dial that never
// lands vs. a connection that never arrives) that sharing a logger makes both
// harder to read.
var bindLog = log.NamedLogger("c2", "bind")

const (
	// BindRetryInterval is how long the dialer waits between attempts.
	//
	// A bind listener is not a service that is up or down; the implant has to
	// reach the code path that opens it, and on a host that is still booting, or
	// where the implant was started by a scheduled task, that can take a while.
	// Retrying forever at a modest interval is the behaviour that matches: the
	// operator starts the dialer once and the session appears when the implant
	// is ready.
	BindRetryInterval = 5 * time.Second

	// BindDialTimeout bounds a single connection attempt.
	BindDialTimeout = 15 * time.Second
)

// BindListener is a running bind dialer. It owns a goroutine that keeps trying
// to reach the implant's listen port until a session is established, then keeps
// it alive by re-dialling whenever the session ends.
type BindListener struct {
	address string
	port    uint16

	mu     sync.Mutex
	stop   chan struct{}
	closed bool
}

// StartBindListener begins dialing address:port and registers each successful
// connection as a session.
//
// It returns as soon as the dialer is running, not when a session appears: the
// implant may not be listening yet, and blocking the RPC until it is would make
// the console look hung for an unbounded time. The session shows up in the
// session list when the implant accepts.
func StartBindListener(address string, port uint16) (*BindListener, error) {
	if address == "" {
		return nil, fmt.Errorf("bind listener requires an address to dial")
	}
	if port == 0 || port > 65535 {
		return nil, fmt.Errorf("bind listener requires a port between 1 and 65535")
	}

	// Ensure the certificate this side presents exists before the first dial, so
	// the failure mode is a clear error at start rather than a handshake
	// rejection on every attempt.
	host := defaultServerCert
	if _, _, err := certs.GetCertificate(certs.MtlsServerCA, certs.ECCKey, host); err != nil {
		certs.MtlsC2ServerGenerateECCCertificate(host)
	}

	bl := &BindListener{
		address: address,
		port:    port,
		stop:    make(chan struct{}),
	}
	go bl.loop()
	return bl, nil
}

// loop keeps a bind session up.
//
// handleSliverConnection blocks for the life of the session, so the loop is
// naturally "connect, serve, retry": when the session ends the call returns and
// the next attempt begins. The implant re-enters its own listen path on the
// same port at the same time, so the two sides converge without any handshake
// about who reconnects first.
func (bl *BindListener) loop() {
	failures := 0
	for {
		select {
		case <-bl.stop:
			return
		default:
		}

		conn, err := bl.dial()
		if err != nil {
			failures++
			// The first failure is reported loudly and the rest quietly. An
			// operator who just started a forward listener needs to know at once
			// why it is not connecting — wrong port, nothing listening, a
			// rejected certificate — but a target that is merely not up yet would
			// otherwise write the same line into the log every five seconds.
			if failures == 1 || failures%12 == 0 {
				bindLog.Infof("bind dial %s failed (%d attempts): %v", bl.Address(), failures, err)
			} else {
				bindLog.Debugf("bind dial %s failed: %v", bl.Address(), err)
			}
			if !bl.sleep(BindRetryInterval) {
				return
			}
			continue
		}
		failures = 0

		bindLog.Infof("bind session connected to %s:%d", bl.address, bl.port)
		handleSliverConnection(conn)

		bindLog.Infof("bind session to %s:%d ended, re-dialling", bl.address, bl.port)
		if !bl.sleep(BindRetryInterval) {
			return
		}
	}
}

// bindClientTLSConfig builds the configuration this side uses as a TLS *client*.
//
// It starts from getServerTLSConfig, which already carries the right key
// material: its Certificates field is the mtls-server certificate the implant
// verifies against the CA it embeds, and its RootCAs pool is the mtls-implant
// CA that the implant's certificate chains to. ClientAuth and ClientCAs are
// server-side fields and tls.Dial ignores them.
//
// Two things are then overridden:
//
//   - InsecureSkipVerify, because hostname verification has nothing to check
//     here. An implant's certificate is issued per *build*, not per address, so
//     it carries no SAN for whatever the operator dialled — an IP, a hostname,
//     or a pivot address. Left enabled, every dial fails with "cannot validate
//     certificate for 127.0.0.1 because it doesn't contain any IP SANs".
//
//   - VerifyPeerCertificate, which reimplements the check that does matter:
//     the chain must terminate at the mtls-implant CA. This mirrors
//     RootOnlyVerifyCertificate in the implant, which disables hostname
//     verification for exactly the same reason in the reverse direction.
//
// Peer identity is therefore established by the certificate chain rather than
// by the address — which is the stronger property, since the address is
// operator-supplied and the chain is not.
func bindClientTLSConfig() *tls.Config {
	cfg := getServerTLSConfig(defaultServerCert)
	if cfg == nil {
		return nil
	}

	caCert, _, err := certs.GetCertificateAuthorityPEM(certs.MtlsImplantCA)
	if err != nil {
		bindLog.Errorf("cannot load the mtls-implant CA: %v", err)
		return nil
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCert) {
		bindLog.Error("the mtls-implant CA certificate could not be parsed")
		return nil
	}

	cfg.InsecureSkipVerify = true
	cfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return fmt.Errorf("bind: peer presented no certificate")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("bind: parse peer certificate: %w", err)
		}
		// KeyUsages: Any, not the default ServerAuth. An implant certificate is
		// issued for client auth (GenerateECCCertificate is called with
		// isClient=true), and a bind session is precisely the case where that
		// same certificate is presented by the TLS *server*. Requiring ServerAuth
		// here rejects every genuine implant with "certificate specifies an
		// incompatible key usage". The property actually being asserted is that
		// the chain terminates at our mtls-implant CA, which is what Roots does.
		if _, err := cert.Verify(x509.VerifyOptions{
			Roots:     roots,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		}); err != nil {
			return fmt.Errorf("bind: peer certificate is not signed by the mtls-implant CA: %w", err)
		}
		return nil
	}
	return cfg
}

// dial opens one mutually-authenticated connection to the implant.
func (bl *BindListener) dial() (net.Conn, error) {
	cfg := bindClientTLSConfig()
	if cfg == nil {
		return nil, fmt.Errorf("cannot build the server TLS configuration")
	}

	// tls.Dial runs the handshake before returning, so a wrong certificate or a
	// peer that is not a bind implant fails here rather than being handed to
	// handleSliverConnection as a dead socket.
	dialer := &net.Dialer{Timeout: BindDialTimeout}
	conn, err := tls.DialWithDialer(dialer, "tcp",
		net.JoinHostPort(bl.address, strconv.Itoa(int(bl.port))), cfg)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// sleep waits for d, reporting false when the listener was stopped first.
func (bl *BindListener) sleep(d time.Duration) bool {
	select {
	case <-bl.stop:
		return false
	case <-time.After(d):
		return true
	}
}

// Stop ends the dialer. It does not kill a session that is already established:
// the session belongs to the operator once it is up, and closing it here would
// turn "stop retrying" into "disconnect me".
func (bl *BindListener) Stop() {
	if bl == nil {
		return
	}
	bl.mu.Lock()
	defer bl.mu.Unlock()
	if bl.closed {
		return
	}
	bl.closed = true
	close(bl.stop)
}

// Address reports what the dialer targets.
func (bl *BindListener) Address() string {
	if bl == nil {
		return ""
	}
	return net.JoinHostPort(bl.address, strconv.Itoa(int(bl.port)))
}

// BindStr is the job name a bind dialer is registered under.
//
// It is deliberately distinct from the transport names (mtls, http, ...) the
// reverse listeners use. A bind listener is a different kind of thing — nothing
// arrives at it, it goes out — and an operator reading the job list needs to
// see that at a glance rather than infer it from a port.
const BindStr = "bind"

// StartBindListenerJob starts a bind dialer and registers it as a job.
//
// The job exists so the dialer is visible and stoppable from the console.
// Unlike the reverse listeners there is no socket to close when it stops: the
// dialer's whole purpose is to keep trying, so stopping it means giving up on
// the target, not shutting a door.
func StartBindListenerJob(req *clientpb.DialBindReq) (*core.Job, error) {
	bl, err := StartBindListener(req.Host, uint16(req.Port))
	if err != nil {
		return nil, err // If we cannot start dialling there is no job to track
	}

	job := &core.Job{
		ID:          core.NextJobID(),
		Name:        BindStr,
		// The description is the bare target rather than a sentence. It is what
		// the console shows as the listener's address, and "bind listener (C2
		// dials 127.0.0.1:4444)" in an address column is noise — the job Name is
		// already "bind", which says the same thing once.
		Description: bl.Address(),
		Protocol:    constants.TCPListenerStr,
		Port:        uint16(req.Port),
		JobCtrl:     make(chan bool),
	}

	go func() {
		<-job.JobCtrl
		jobLog.Infof("Stopping bind listener (%d) ...", job.ID)
		bl.Stop()
		core.Jobs.Remove(job)
		core.EventBroker.Publish(core.Event{
			Job:       job,
			EventType: consts.JobStoppedEvent,
		})
	}()

	core.Jobs.Add(job)

	return job, nil
}
