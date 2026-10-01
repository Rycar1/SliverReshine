package launch

import (
	"errors"
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

// TestRequireFreePortNamesTheHolder is the regression test for the report that
// produced this feature.
//
// The first version of the check said only that *something* held the port, so
// the operator -- whose holder turned out to be wslrelay.exe, a WSL port relay
// that Sliver never mentions -- had to find out what by hand. The message has to
// carry the name, not just the fact of the conflict.
//
// The holder here is this very test binary, which is listening on the port, so
// the name is resolvable wherever the helper works. Where it is not resolvable
// (no ss/lsof, or a netstat that cannot be read) the check degrades to the
// unnamed message -- which is still correct, so the assertion accepts either
// shape and requires only that whichever one is produced be well-formed.
func TestRequireFreePortNamesTheHolder(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	s := &Server{}
	msg := s.requireFreePort("127.0.0.1", port).Error()

	holder := describeListener(port)
	if holder == "" {
		// Unresolvable on this machine: the fallback must still be the whole
		// message rather than a truncated or empty one.
		for _, want := range []string{"already in use", "unreachable", "mpPort"} {
			if !strings.Contains(msg, want) {
				t.Errorf("unnamed message is incomplete, missing %q: %s", want, msg)
			}
		}
		t.Logf("no holder resolvable on this platform; exercised the fallback: %s", msg)
		return
	}

	if !strings.Contains(msg, holder) {
		t.Errorf("the message omits the holder %q: %s", holder, msg)
	}
	if !strings.Contains(msg, "already in use by ") {
		t.Errorf("the message does not attribute the conflict to the holder: %s", msg)
	}
}

// TestPortInUseErrorShapes pins both renderings directly.
//
// requireFreePort's own message depends on whether the running machine can
// resolve a pid, which is exactly the environment-dependent part; going through
// portInUseError is how both shapes get asserted deterministically.
func TestPortInUseErrorShapes(t *testing.T) {
	cause := errors.New("listen tcp 127.0.0.1:31337: bind: Only one usage of each socket address")

	t.Run("fallback keeps the raw listen error", func(t *testing.T) {
		msg := portInUseError("127.0.0.1:31337", cause, "").Error()

		// The existing message is the fallback when the holder cannot be
		// determined, so it must survive intact -- including the underlying
		// listen error, which is then the only evidence left.
		for _, want := range []string{
			"127.0.0.1:31337", // the address
			"already in use",  // the problem
			"Only one usage",  // the raw cause, preserved
			"unreachable",     // the consequence
			"mpPort",          // the fix
			"Sliver daemon",   // who is affected
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("fallback message missing %q: %s", want, msg)
			}
		}
		if strings.Contains(msg, "by ") {
			t.Errorf("fallback message claims a holder it does not have: %s", msg)
		}
	})

	t.Run("named holder replaces the raw cause", func(t *testing.T) {
		msg := portInUseError("127.0.0.1:31337", cause, "wslrelay.exe (pid 15944)").Error()

		for _, want := range []string{
			"127.0.0.1:31337",
			"already in use by wslrelay.exe (pid 15944)",
			"unreachable",
			"mpPort",
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("named message missing %q: %s", want, msg)
			}
		}
		// The raw error is dropped only in the named form: naming the holder is
		// the more useful evidence, and keeping both made the message a wall of
		// text on the line the operator actually reads.
		if strings.Contains(msg, "Only one usage") {
			t.Errorf("named message should not also dump the listen error: %s", msg)
		}
	})
}

// describeListener must not invent a holder. A port nothing is listening on has
// to come back as "", which is what routes requireFreePort to the fallback text.
func TestDescribeListenerOnAnUnboundPortIsEmpty(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// Release it: nothing holds the port, so there is nothing to name.
	ln.Close()

	if got := describeListener(port); got != "" {
		t.Errorf("describeListener(%d) = %q, want \"\" for a port nobody holds", port, got)
	}
}

// listeningPID must never panic or hang on nonsense input; it returns 0, which
// describeListener turns into the fallback message.
func TestListeningPIDRejectsImpossiblePorts(t *testing.T) {
	for _, port := range []int{0, -1} {
		if got := listeningPID(port); got != 0 {
			t.Errorf("listeningPID(%d) = %d, want 0", port, got)
		}
	}
}
