//go:build !windows

package launch

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr puts the daemon in its own process group so signals aimed at
// the launcher do not propagate to the listener.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateProcess kills the daemon and everything it forked.
//
// The daemon is started with Setpgid, so it leads a process group of its own
// and Process.Kill only reaches the leader. Sliver's daemon forks build and
// compile workers; killing the leader alone left them holding the gRPC port,
// which is the state an operator describes as "it will not close" -- the
// launcher is gone, the listener is not.
//
// Signalling the whole group with a negative pid takes the children too. ESRCH
// is expected when the daemon already exited, so the group error falls back to
// killing the single process rather than being reported.
func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
