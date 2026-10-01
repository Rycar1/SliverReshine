//go:build !windows

package launch

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// listeningPID returns the PID of the process listening on port, or 0 when it
// cannot be determined.
//
// Best-effort, and knowingly so: this only feeds an error message. A missing
// tool, a permission denial (both ss and lsof hide the pid of another user's
// socket unless run as root) or an unfamiliar output format all degrade to 0,
// and the caller then prints the conflict without naming the holder rather than
// losing the message.
//
// ss is tried first because it is present on every mainstream Linux (including
// the minimal images and WSL this binary also runs in), where lsof is a
// separate package that is frequently not installed.
func listeningPID(port int) int {
	if port <= 0 {
		return 0
	}
	if pid := ssListeningPID(port); pid != 0 {
		return pid
	}
	// lsof prints the PID directly, so it needs no parsing, but it is the
	// optional one of the two.
	if pid := firstPID("lsof", "-ti", "tcp:"+strconv.Itoa(port), "-sTCP:LISTEN"); pid != 0 {
		return pid
	}
	return firstPID("lsof", "-ti", "tcp:"+strconv.Itoa(port))
}

// firstPID runs name with args and returns the first positive integer in its
// output, or 0 when the command fails or prints none.
func firstPID(name string, args ...string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return 0
	}
	for _, field := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(field); err == nil && pid > 0 {
			return pid
		}
	}
	return 0
}

// ssPidPattern matches the users:(("name",pid=1234,fd=5)) column of `ss -p`.
var ssPidPattern = regexp.MustCompile(`pid=(\d+)`)

// ssListeningPID parses `ss -lptn` for the socket bound to port.
//
// A listener line looks like:
//
//	LISTEN 0  5  127.0.0.1:45999  0.0.0.0:*  users:(("python3",pid=2373,fd=3))
//
// The local address is normally column 4, but the column count varies between
// iproute2 versions and when the socket is not the only address family, so this
// scans for any field ending in ":port" instead of indexing. That is safe for a
// *listening* socket specifically: its peer is always 0.0.0.0:* or [::]:* or
// *:*, so no field other than the local address can end in the port.
func ssListeningPID(port int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ss", "-lptn").Output()
	if err != nil {
		return 0
	}

	suffix := ":" + strconv.Itoa(port)
	for _, line := range strings.Split(string(out), "\n") {
		bound := false
		for _, field := range strings.Fields(line) {
			if strings.HasSuffix(field, suffix) {
				bound = true
				break
			}
		}
		if !bound {
			continue
		}
		// -p prints the owning process only for sockets the caller may
		// inspect; without it the row still matches but yields no pid, and the
		// loop keeps looking rather than giving up on the first hit.
		if m := ssPidPattern.FindStringSubmatch(line); m != nil {
			if pid, err := strconv.Atoi(m[1]); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

// processName returns the executable path of pid, or "" when it cannot be read.
//
// This is the platform half of describeListener. It cannot use the standard
// library: os.Process exposes no image-name accessor (os.Executable reports the
// *current* process only).
//
// procfs answers the question without spawning anything, so it is tried first;
// `ps` covers macOS and the BSDs, which have no /proc. Reading another user's
// /proc/<pid>/exe is denied without privilege, which is why the caller degrades
// to a bare pid instead of failing.
func processName(pid int) string {
	if pid <= 0 {
		return ""
	}
	if exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe"); err == nil && exe != "" {
		// A binary replaced on disk while it is still running reads back with
		// this kernel-appended suffix, which is not part of the name.
		return strings.TrimSuffix(exe, " (deleted)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
