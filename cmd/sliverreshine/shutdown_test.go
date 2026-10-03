package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"
)

// TestBeginShutdownOnFirstSignalLeavesTheNextPressForTheForcePath pins the split
// between the two Ctrl-C presses.
//
// The reader that starts the shutdown must consume exactly one press. When it
// consumed none -- two channels registered for the same signal, both fed by the
// first press -- the force path took the first press and a single Ctrl-C exited
// immediately, with the grace period unreachable.
func TestBeginShutdownOnFirstSignalLeavesTheNextPressForTheForcePath(t *testing.T) {
	sig := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go beginShutdownOnFirstSignal(sig, cancel)

	sig <- os.Interrupt
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the first signal did not start the shutdown")
	}

	select {
	case got := <-sig:
		t.Fatalf("the first press was left behind for the force path (got %v)", got)
	default:
	}

	sig <- syscall.SIGTERM
	select {
	case got := <-sig:
		if got != syscall.SIGTERM {
			t.Errorf("the next press read back as %v, want %v", got, syscall.SIGTERM)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second press was dropped instead of buffered")
	}
}

// TestRapidDoubleCtrlCStillTakesTheFastPath covers the case the buffer exists
// for: both presses land before the first is handled. The shutdown reader takes
// one and the force path still finds the other, so a double press during a slow
// step is not silently swallowed.
func TestRapidDoubleCtrlCStillTakesTheFastPath(t *testing.T) {
	sig := make(chan os.Signal, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go beginShutdownOnFirstSignal(sig, cancel)

	sig <- os.Interrupt
	sig <- os.Interrupt

	returned := make(chan os.Signal, 1)
	go func() { returned <- waitForForcedQuit(ctx, sig) }()
	select {
	case got := <-returned:
		if got != os.Interrupt {
			t.Errorf("waitForForcedQuit returned %v, want %v", got, os.Interrupt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the second of two rapid presses was dropped")
	}
}

// TestWaitForForcedQuitNeedsASecondSignal covers the other half: a shutdown in
// progress must not end on its own, or the ceiling below would be the only way
// out and the operator's second Ctrl-C would mean nothing.
func TestWaitForForcedQuitNeedsASecondSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	returned := make(chan os.Signal, 1)
	go func() { returned <- waitForForcedQuit(ctx, sig) }()

	cancel()
	select {
	case got := <-returned:
		t.Fatalf("waitForForcedQuit returned %v without a second signal", got)
	case <-time.After(50 * time.Millisecond):
	}

	sig <- syscall.SIGTERM
	select {
	case got := <-returned:
		if got != syscall.SIGTERM {
			t.Errorf("waitForForcedQuit returned %v, want %v", got, syscall.SIGTERM)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a second signal did not end the shutdown")
	}
}

// TestShutdownConsoleIsBoundedAndDropsInFlightRequests covers the two ways
// http.Server.Shutdown alone fails to release the console.
//
// A request that never returns holds the connection past the grace period, and
// Shutdown reports the deadline without touching it. Without the Close backstop
// the port stays bound and the process looks stuck on "shutting down ...".
func TestShutdownConsoleIsBoundedAndDropsInFlightRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "late")
	})}
	go func() { _ = srv.Serve(ln) }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: console\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler never ran")
	}

	begin := time.Now()
	shutdownConsole(srv)
	if elapsed := time.Since(begin); elapsed > consoleShutdownGrace+2*time.Second {
		t.Fatalf("shutdownConsole took %s; the grace period is not a ceiling", elapsed)
	}

	// The handler is still blocked, so the only way the client sees the
	// connection end is if the server dropped it.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Error("the in-flight connection survived shutdown; the console port would stay bound")
	}
	close(release)
}
