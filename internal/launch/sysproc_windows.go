//go:build windows

package launch

import "os/exec"

// setSysProcAttr is a no-op on Windows; the child is killed explicitly on
// shutdown by Stop.
func setSysProcAttr(cmd *exec.Cmd) {}
