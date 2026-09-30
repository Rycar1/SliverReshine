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
