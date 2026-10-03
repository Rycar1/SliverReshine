//go:build !windows

package launch

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestTerminateProcessKillsTheWholeProcessGroup pins the difference between
// killing the daemon and killing everything the daemon started.
//
// The daemon leads its own process group (setSysProcAttr) and the workers it
// forks inherit that group. Process.Kill reaches the leader only, so the
// workers survive it and keep the gRPC port bound -- the launcher exits, the
// listener does not, and Ctrl-C looks like it did nothing. This spawns a leader
// with a child in the same group and asserts the group is empty afterwards.
func TestTerminateProcessKillsTheWholeProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & sleep 60")
	setSysProcAttr(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := cmd.Process.Pid

	// A live group answers signal 0; the child is reaped asynchronously, so
	// poll instead of assuming the kill has already landed everywhere.
	if err := syscall.Kill(-pid, 0); err != nil {
		t.Fatalf("group %d is not alive before the kill: %v", pid, err)
	}
	terminateProcess(cmd)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(-pid, 0); err != nil {
			break
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			t.Fatalf("process group %d still has members after terminateProcess", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = cmd.Wait()
}
