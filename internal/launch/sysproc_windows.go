//go:build windows

package launch

import "os/exec"

// setSysProcAttr is a no-op on Windows; the child is killed explicitly on
// shutdown by Stop.
func setSysProcAttr(cmd *exec.Cmd) {}

// terminateProcess kills the daemon. Windows has no process group of the kind
// unix gets from Setpgid here, so the child is killed explicitly.
func terminateProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
