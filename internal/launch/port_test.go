package launch

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// The port check exists because the failure it prevents was invisible.
//
// Without it, a stray process on the gRPC port made the launcher report the
// server ready -- waitForPort dialled the address and reached the stranger --
// and the operator was left with a console that served its UI and said the
// server was unreachable. Nothing in that output pointed at a port conflict.
//
// These tests pin both halves: the check refuses a taken port, and it still
// allows the happy path.

func TestRequireFreePortAcceptsAnUnusedPort(t *testing.T) {
	// Bind and release to get a port the OS just handed out, so the test does
	// not race another process for a fixed number.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	s := &Server{}
	if err := s.requireFreePort("127.0.0.1", port); err != nil {
		t.Fatalf("a free port was refused: %v", err)
	}
}

func TestRequireFreePortRejectsATakenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	s := &Server{}
	err = s.requireFreePort("127.0.0.1", port)
	if err == nil {
		t.Fatal("a taken port was accepted; the daemon would fail to bind and the " +
			"console would report the server unreachable")
	}
}

// The message is the whole value of the check: it has to say what is wrong, what
// the consequence would have been, and what to do.
func TestRequireFreePortExplainsItself(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	s := &Server{}
	msg := s.requireFreePort("127.0.0.1", port).Error()

	for _, want := range []string{
		fmt.Sprintf("127.0.0.1:%d", port), // the address
		"already in use",                  // the problem
		"unreachable",                     // the symptom the operator would have seen
		"mpPort",                          // the fix
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not mention %q: %s", want, msg)
		}
	}
}
