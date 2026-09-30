package sliver

import "testing"

// TestSplitHostPort pins the parsing that recovers a forward listener's target
// from its job description.
//
// The case that matters is IPv6. The server writes the target with
// net.JoinHostPort, so a v6 address arrives as "[::1]:4444"; splitting on the
// first colon would yield "[" and splitting on the last without stripping the
// brackets would yield "[::1]". Both are wrong in a way that only shows up on a
// dual-stack estate, which is exactly where a forward listener is most likely to
// be used.
func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		addr     string
		fallback uint32
		wantHost string
		wantPort uint32
	}{
		{"10.0.0.5:4444", 0, "10.0.0.5", 4444},
		{"127.0.0.1:4444", 0, "127.0.0.1", 4444},
		{"host.example.com:8888", 0, "host.example.com", 8888},
		// IPv6: bracketed, and the port is after the LAST colon.
		{"[::1]:4444", 0, "::1", 4444},
		{"[2001:db8::1]:9999", 0, "2001:db8::1", 9999},

		// No port at all: keep the whole string as the host and fall back to the
		// port the job reported separately, rather than dropping it.
		{"10.0.0.5", 4444, "10.0.0.5", 4444},
		// Port present but unparseable or out of range: same fallback.
		{"10.0.0.5:notaport", 4444, "10.0.0.5", 4444},
		{"10.0.0.5:0", 4444, "10.0.0.5", 4444},
		{"10.0.0.5:70000", 4444, "10.0.0.5", 4444},
	}

	for _, tc := range cases {
		gotHost, gotPort := splitHostPort(tc.addr, tc.fallback)
		if gotHost != tc.wantHost || gotPort != tc.wantPort {
			t.Errorf("splitHostPort(%q, %d) = (%q, %d), want (%q, %d)",
				tc.addr, tc.fallback, gotHost, gotPort, tc.wantHost, tc.wantPort)
		}
	}
}
