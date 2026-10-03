package launch

import (
	"errors"
	"sync"
	"testing"
)

// TestDaemonExitStateIsRaceFree drives the publisher and both readers
// concurrently. Under -race (which CI runs) an unsynchronised field is reported
// here. The previous implementation read Cmd.ProcessState from the launcher
// goroutine while Wait wrote it from the reap goroutine -- a data race even
// though both sides only tested the value.
func TestDaemonExitStateIsRaceFree(t *testing.T) {
	s := &Server{}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			s.publishExit(errors.New("daemon exited 1"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			_ = s.daemonExited()
			_ = s.daemonExitReason()
		}
	}()
	wg.Wait()

	if !s.daemonExited() {
		t.Fatal("daemonExited must observe a published exit")
	}
	err := s.daemonExitReason()
	if err == nil || err.Error() != "daemon exited 1" {
		t.Fatalf("daemonExitReason = %v, want the published error", err)
	}
}

// A clean exit carries a nil error but must still count as exited:
// waitForPort uses daemonExited to stop waiting and daemonExitReason only to
// explain, so conflating the two would either hang the wait or invent a cause.
func TestDaemonExitStateDistinguishesCleanExits(t *testing.T) {
	s := &Server{}
	if s.daemonExited() {
		t.Fatal("a fresh server must not report an exit")
	}
	if s.daemonExitReason() != nil {
		t.Fatalf("a fresh server must have no exit reason, got %v", s.daemonExitReason())
	}

	s.publishExit(nil)
	if !s.daemonExited() {
		t.Fatal("a clean exit must still set exited")
	}
	if s.daemonExitReason() != nil {
		t.Fatalf("a clean exit must have a nil reason, got %v", s.daemonExitReason())
	}
}
