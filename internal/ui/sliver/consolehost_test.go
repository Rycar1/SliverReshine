package sliver

import "testing"

// ConsoleHostFromHeader turns the address in the operator's own URL into a
// candidate callback address.
//
// The cases that matter are the ones that must *not* produce a value. A Host
// header is attacker-controlled, and it ends up inside a delivery command, so a
// header that is not a bare host has to be dropped rather than passed through. A
// loopback or wildcard header is dropped for a different reason: it is not an
// address a target can dial, so reporting it would replace one dead one-liner
// with another that looks fine.
func TestConsoleHostFromHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"empty header", "", ""},
		{"bare hostname", "c2.example.com", "c2.example.com"},
		{"hostname with port", "c2.example.com:8443", "c2.example.com"},
		{"public IPv4 with port", "203.0.113.7:8080", "203.0.113.7"},
		{"bare public IPv4", "203.0.113.7", "203.0.113.7"},
		{"bracketed IPv6 with port", "[2001:db8::1]:8443", "2001:db8::1"},
		{"bare IPv6 literal", "2001:db8::1", "2001:db8::1"},
		{"surrounding whitespace", "  c2.example.com:443  ", "c2.example.com"},

		{"wildcard IPv4", "0.0.0.0:8080", ""},
		{"wildcard IPv6", "[::]:8080", ""},
		{"loopback IPv4", "127.0.0.1:8080", ""},
		{"loopback IPv6", "[::1]:8443", ""},
		{"loopback name", "localhost:8080", ""},
		{"loopback name no port", "localhost", ""},

		{"shell metacharacter", "evil; id", ""},
		{"argument injection", "10.0.0.5 -o /tmp/x", ""},
		{"powerShell injection", "a'; Write-Output PWNED; '", ""},
		{"embedded newline", "good.example.com\nbad", ""},
		{"leading dot", ".example.com", ""},
		{"empty host with port", ":8080", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConsoleHostFromHeader(tc.header); got != tc.want {
				t.Errorf("ConsoleHostFromHeader(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

// An accepted value must survive the same validator operator input goes through,
// so it cannot introduce anything the templates do not expect.
func TestConsoleHostFromHeaderOutputIsValidated(t *testing.T) {
	got := ConsoleHostFromHeader("c2.example.com:8443")
	if got == "" {
		t.Fatal("a plain public hostname was dropped")
	}
	if err := validateHost(got); err != nil {
		t.Errorf("accepted value %q does not pass validateHost: %v", got, err)
	}
}
