package api

import "testing"

// hostOrConsoleAddress is the seam that fixes the dead one-liner: a listener
// bound to 0.0.0.0 has no usable callback address of its own, so an empty host
// field falls back to the address the operator reached the console on.
//
// The two properties that matter are that an explicit host is never overridden
// (the operator knows the target's network better than the console does) and
// that a header which says nothing about target reachability -- loopback,
// wildcard, or a forged non-host -- yields "" so the caller's own fallback runs.
func TestHostOrConsoleAddress(t *testing.T) {
	cases := []struct {
		name       string
		reqHost    string
		headerHost string
		want       string
	}{
		{"explicit host wins", "10.0.0.5", "c2.example.com:8443", "10.0.0.5"},
		{"explicit host wins over empty header", "10.0.0.5", "", "10.0.0.5"},
		{"blank host falls back to the header", "", "c2.example.com:8443", "c2.example.com"},
		{"whitespace host falls back to the header", "   ", "c2.example.com:8443", "c2.example.com"},
		{"loopback header is dropped", "", "127.0.0.1:8080", ""},
		{"wildcard header is dropped", "", "0.0.0.0:8080", ""},
		{"localhost header is dropped", "", "localhost:8080", ""},
		{"forged header is dropped", "", "evil; id", ""},
		{"both empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hostOrConsoleAddress(tc.reqHost, tc.headerHost); got != tc.want {
				t.Errorf("hostOrConsoleAddress(%q, %q) = %q, want %q",
					tc.reqHost, tc.headerHost, got, tc.want)
			}
		})
	}
}
