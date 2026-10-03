package main

import (
	"net"
	"strconv"
	"testing"
)

// consoleFallbackAddr is the pure half of the console fallback: it must change
// the port and nothing else. The wildcard case is the one that matters, because
// the console defaults to 0.0.0.0 and the fallback must never quietly narrow
// that to loopback.
func TestConsoleFallbackAddrKeepsTheHost(t *testing.T) {
	cases := []struct {
		addr string
		port int
		want string
	}{
		{"0.0.0.0:8080", 51234, "0.0.0.0:51234"},
		{"127.0.0.1:8080", 51234, "127.0.0.1:51234"},
		{"[::]:8080", 51234, "[::]:51234"},
	}
	for _, tc := range cases {
		got, ok := consoleFallbackAddr(tc.addr, tc.port)
		if !ok {
			t.Errorf("consoleFallbackAddr(%q) reported no fallback", tc.addr)
			continue
		}
		if got != tc.want {
			t.Errorf("consoleFallbackAddr(%q, %d) = %q, want %q", tc.addr, tc.port, got, tc.want)
		}
	}
}

func TestConsoleFallbackAddrRejectsAnUnsplitAddress(t *testing.T) {
	if _, ok := consoleFallbackAddr("not-an-address", 8080); ok {
		t.Error("consoleFallbackAddr accepted an address it cannot rewrite")
	}
}

// A free port is used exactly as asked: the fallback must not fire when it is
// not needed, or the console would move on every start.
func TestListenConsoleUsesAFreeAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	got, err := listenConsole(addr)
	if err != nil {
		t.Fatalf("listenConsole(%s): %v", addr, err)
	}
	defer got.Close()
	if got.Addr().String() != addr {
		t.Errorf("listenConsole(%s) bound %s, want the address it was given", addr, got.Addr())
	}
}

// The regression: a taken console port used to be fatal. It must now bind a
// different port on the same host, so the console stays reachable and the
// banner names the port that is really serving.
func TestListenConsoleFallsBackWhenThePortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	takenPort := taken.Addr().(*net.TCPAddr).Port

	got, err := listenConsole(net.JoinHostPort("127.0.0.1", strconv.Itoa(takenPort)))
	if err != nil {
		t.Fatalf("listenConsole on a taken port: %v", err)
	}
	defer got.Close()

	host, portStr, err := net.SplitHostPort(got.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Errorf("fallback changed the host to %q, want 127.0.0.1", host)
	}
	if portStr == strconv.Itoa(takenPort) {
		t.Errorf("fallback stayed on the taken port %d", takenPort)
	}
}
