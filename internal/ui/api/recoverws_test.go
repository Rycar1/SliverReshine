package api

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// recoverWS guards the goroutines that run after a WebSocket upgrade, which is
// exactly where net/http's own per-connection recover stops applying. The
// failure mode it prevents is not a bad response, it is a dead process: a panic
// escaping one of those goroutines takes the console down.
//
// The test drives a real panic on a real goroutine. If recoverWS stopped
// recovering, the panic would escape and the test binary would die here rather
// than report a failed assertion, which is the loudest possible failure.
func TestRecoverWSSurvivesAndLogsAPanic(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	done := make(chan struct{})
	go func() {
		// Registered first, so it runs after recoverWS has handled the panic.
		defer close(done)
		defer recoverWS(nil, "terminal")
		panic("boom")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the goroutine never finished; recoverWS did not stop the panic")
	}

	out := buf.String()
	if !strings.Contains(out, "PANIC in terminal websocket") {
		t.Errorf("log = %q, want a PANIC line naming the terminal websocket", out)
	}
	if !strings.Contains(out, "boom") {
		t.Errorf("log = %q, want the panic value in it", out)
	}
}
