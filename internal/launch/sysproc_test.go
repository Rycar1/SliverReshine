package launch

import (
	"os/exec"
	"testing"
)

// TestTerminateProcessIgnoresAnUnstartedCommand covers the shutdown paths that
// run before (or without) a daemon: a nil command, and one that was built but
// never started. Both reach Stop on an error path -- a failed launch still
// defers the cleanup -- and both have to be no-ops rather than panics.
func TestTerminateProcessIgnoresAnUnstartedCommand(t *testing.T) {
	terminateProcess(nil)
	terminateProcess(&exec.Cmd{})
}
