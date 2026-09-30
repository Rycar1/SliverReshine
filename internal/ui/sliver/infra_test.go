package sliver

import (
	"testing"

	"github.com/bishopfox/sliver/protobuf/sliverpb"
)

// Sliver's rportfwd RPC carries endpoints as host:port strings, but the numeric
// BindPort/ForwardPort fields on the wire are unreliable: the implant's start
// handler writes req.ForwardPort into both, and its list handler omits them.
// These tests pin the join/split behaviour that keeps the console honest.
func TestJoinAddr(t *testing.T) {
	cases := []struct {
		host string
		port uint32
		want string
	}{
		{"0.0.0.0", 9900, "0.0.0.0:9900"},
		{"127.0.0.1", 9100, "127.0.0.1:9100"},
		{"", 9900, ":9900"},
		// A zero port means the caller already supplied a full address.
		{"example.com:443", 0, "example.com:443"},
		{"", 0, ""},
	}
	for _, c := range cases {
		if got := joinAddr(c.host, c.port); got != c.want {
			t.Errorf("joinAddr(%q, %d) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}

func TestSplitAddr(t *testing.T) {
	cases := []struct {
		addr     string
		wantHost string
		wantPort uint32
	}{
		{"0.0.0.0:9900", "0.0.0.0", 9900},
		{"127.0.0.1:9100", "127.0.0.1", 9100},
		// The TUI's ":port" shorthand binds all interfaces.
		{":9900", "", 9900},
		{"[::1]:8080", "::1", 8080},
		// No port at all: keep the value so it is still visible in the UI.
		{"example.com", "example.com", 0},
		{"", "", 0},
	}
	for _, c := range cases {
		host, port := splitAddr(c.addr)
		if host != c.wantHost || port != c.wantPort {
			t.Errorf("splitAddr(%q) = (%q, %d), want (%q, %d)",
				c.addr, host, port, c.wantHost, c.wantPort)
		}
	}
}

// A start response reports BindPort == ForwardPort (an upstream quirk). The
// view must recover the real values by parsing the address strings instead of
// trusting those fields.
func TestRportFwdToViewIgnoresBogusPortFields(t *testing.T) {
	resp := &sliverpb.RportFwdListener{
		ID:             2,
		BindAddress:    "0.0.0.0:9900",
		ForwardAddress: "127.0.0.1:9100",
		BindPort:       9100, // bogus: implant echoes req.ForwardPort here
		ForwardPort:    9100,
	}
	v := rportFwdToView(resp)
	if v.BindAddress != "0.0.0.0" || v.BindPort != 9900 {
		t.Errorf("bind side = %s:%d, want 0.0.0.0:9900", v.BindAddress, v.BindPort)
	}
	if v.ForwardAddress != "127.0.0.1" || v.ForwardPort != 9100 {
		t.Errorf("forward side = %s:%d, want 127.0.0.1:9100", v.ForwardAddress, v.ForwardPort)
	}
	if v.ID != 2 {
		t.Errorf("ID = %d, want 2", v.ID)
	}
}

// The list handler populates only the address strings, so a round trip must
// still surface usable ports.
func TestRportFwdToViewFromListShape(t *testing.T) {
	resp := &sliverpb.RportFwdListener{
		ID:             7,
		BindAddress:    "0.0.0.0:9900",
		ForwardAddress: "127.0.0.1:9100",
		// BindPort / ForwardPort deliberately left at zero.
	}
	v := rportFwdToView(resp)
	if v.BindPort != 9900 {
		t.Errorf("BindPort = %d, want 9900", v.BindPort)
	}
	if v.ForwardPort != 9100 {
		t.Errorf("ForwardPort = %d, want 9100", v.ForwardPort)
	}
}

func TestRportFwdToViewNil(t *testing.T) {
	if v := rportFwdToView(nil); v != (RportFwdListenerView{}) {
		t.Errorf("rportFwdToView(nil) = %+v, want zero value", v)
	}
}
