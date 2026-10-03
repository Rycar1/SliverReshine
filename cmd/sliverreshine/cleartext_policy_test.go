package main

import (
	"strings"
	"testing"

	"sliverreshine/internal/config"
)

// The policy guard is the whole point of requireTLS: the pair below is the
// accepted one, and every other combination either refuses or is a no-op.
func TestCheckCleartextPolicy(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		requireTLS  bool
		cert        string
		key         string
		wantRefused bool
	}{
		{
			// The shipped default. Advisory warning, not a refusal -- the
			// console still starts so cross-machine access keeps working.
			name:        "wildcard without the flag only warns",
			addr:        "0.0.0.0:8080",
			wantRefused: false,
		},
		{
			name:        "wildcard with the flag is refused",
			addr:        "0.0.0.0:8080",
			requireTLS:  true,
			wantRefused: true,
		},
		{
			// A specific interface is as exposed as the wildcard: the console
			// is still reachable from the network.
			name:        "named interface with the flag is refused",
			addr:        "10.1.2.3:8080",
			requireTLS:  true,
			wantRefused: true,
		},
		{
			name:        "empty host is the wildcard, not loopback",
			addr:        ":8080",
			requireTLS:  true,
			wantRefused: true,
		},
		{
			// The recommended deployment: an SSH tunnel terminates the
			// encryption, and nothing leaves the host in the clear.
			name:        "loopback is allowed",
			addr:        "127.0.0.1:8080",
			requireTLS:  true,
			wantRefused: false,
		},
		{
			name:        "localhost is allowed",
			addr:        "localhost:8080",
			requireTLS:  true,
			wantRefused: false,
		},
		{
			name:        "ipv6 loopback is allowed",
			addr:        "[::1]:8080",
			requireTLS:  true,
			wantRefused: false,
		},
		{
			// With TLS on, there is no cleartext left to refuse.
			name:        "tls satisfies the flag on the wildcard",
			addr:        "0.0.0.0:8080",
			requireTLS:  true,
			cert:        "/etc/sliverreshine/console.crt",
			key:         "/etc/sliverreshine/console.key",
			wantRefused: false,
		},
		{
			// main already refuses a half-configured pair for its own reason; the
			// policy guard must not treat it as though TLS were present.
			name:        "half a tls pair is still cleartext",
			addr:        "0.0.0.0:8080",
			requireTLS:  true,
			cert:        "/etc/sliverreshine/console.crt",
			wantRefused: true,
		},
		{
			// The flag off must never refuse, whatever the address, or the
			// default deployment stops working.
			name:        "no flag never refuses",
			addr:        "0.0.0.0:8080",
			wantRefused: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Addr = tc.addr
			cfg.RequireTLS = tc.requireTLS
			cfg.TLSCert = tc.cert
			cfg.TLSKey = tc.key

			err := checkCleartextPolicy(cfg)
			if tc.wantRefused && err == nil {
				t.Fatalf("checkCleartextPolicy(%s, requireTLS=%v) = nil, want a refusal",
					tc.addr, tc.requireTLS)
			}
			if !tc.wantRefused && err != nil {
				t.Fatalf("checkCleartextPolicy(%s, requireTLS=%v) = %v, want nil",
					tc.addr, tc.requireTLS, err)
			}
		})
	}
}

// A refusal that does not say what to do is a dead end for the operator who
// just upgraded and set the flag. The message has to name both ways out.
func TestCleartextRefusalNamesBothWaysOut(t *testing.T) {
	cfg := config.Default()
	cfg.Addr = "0.0.0.0:8080"
	cfg.RequireTLS = true

	err := checkCleartextPolicy(cfg)
	if err == nil {
		t.Fatal("no refusal for a wildcard bind with requireTLS")
	}
	msg := err.Error()
	if !strings.Contains(msg, "0.0.0.0:8080") {
		t.Errorf("error %q does not name the offending address", msg)
	}
	if !strings.Contains(msg, "tlsCert") {
		t.Errorf("error %q does not offer the TLS fix", msg)
	}
	if !strings.Contains(msg, "127.0.0.1") {
		t.Errorf("error %q does not offer the loopback fix", msg)
	}
}

// isLoopbackHost is what the guard and the warning both rest on, so pin the
// wildcard case: an empty host is 0.0.0.0, and treating it as safe would silently
// disable both.
func TestIsLoopbackHost(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{"0.0.0.0", false},
		{"::", false},
		{"127.0.0.1", true},
		{"127.0.0.53", true},
		{"::1", true},
		{"localhost", true},
		{"10.0.0.1", false},
		{"192.168.1.10", false},
		{"console.example", false},
	}

	for _, tc := range tests {
		if got := isLoopbackHost(tc.host); got != tc.want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
