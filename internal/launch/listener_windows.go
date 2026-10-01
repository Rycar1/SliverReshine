//go:build windows

package launch

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// listeningPID returns the PID of the process listening on port, or 0 when it
// cannot be determined.
//
// Windows keeps the TCP connection table behind GetExtendedTcpTable, which the
// standard library does not wrap. Rather than take a dependency on
// golang.org/x/sys/windows for one diagnostic string -- x/sys is only an
// indirect requirement of this module, and importing it would make it direct --
// this parses `netstat -ano`, which ships with every supported Windows and
// prints the owning PID in its last column:
//
//	TCP    127.0.0.1:31337    0.0.0.0:0    LISTENING    15944
//
// Best-effort by design: this only feeds an error message, so a missing
// netstat, a timeout or an unexpected format all degrade to 0 and the caller
// prints the conflict without naming the holder.
func listeningPID(port int) int {
	if port <= 0 {
		return 0
	}

	// A hard timeout because this runs on the startup path: a hung child here
	// would be a hang the operator cannot see.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "netstat", "-ano")
	// netstat is a console program. Without this a black window flashes over
	// the console UI every time the gRPC port is taken -- which is exactly the
	// moment the operator is looking at the screen.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return parseNetstatListeningPID(out, port)
}

// parseNetstatListeningPID extracts the owning PID for a local port from
// `netstat -ano` output.
//
// The parse deliberately keys off the local-address column and the numeric last
// column instead of the state word. `netstat` localises that word (LISTENING /
// ABHÖREN / 侦听), so a state comparison would silently stop matching on a
// non-English install and the message would lose the holder for those users.
//
// Rows are ranked rather than filtered: a row in the LISTENING state wins, and
// any other row whose *local* address carries the port is kept as a fallback so
// a localised state string still yields a PID. Foreign-address matches are
// never considered, so a client connected *to* the port is not mistaken for
// its owner.
func parseNetstatListeningPID(out []byte, port int) int {
	suffix := ":" + strconv.Itoa(port)
	fallback := 0

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimRight(line, "\r"))
		// TCP rows are proto/local/foreign/state/pid; UDP rows have no state
		// and no listener, and the header line has neither.
		if len(fields) < 4 {
			continue
		}
		if !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		// fields[1] is the local address, e.g. 127.0.0.1:31337 or [::]:31337.
		if !strings.HasSuffix(fields[1], suffix) {
			continue
		}
		pid, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil || pid <= 0 {
			continue
		}
		if strings.EqualFold(fields[3], "LISTENING") {
			return pid
		}
		if fallback == 0 {
			fallback = pid
		}
	}
	return fallback
}

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
)

// processQueryLimitedInformation is the least privilege that still answers
// "what is this process called?".
const processQueryLimitedInformation = 0x1000

// processName returns the full image path of pid, or "" when it cannot be read.
//
// This is the platform half of describeListener. It cannot use the standard
// library: os.Process exposes no image-name accessor (os.Executable reports the
// *current* process only), so naming another process needs OpenProcess plus
// QueryFullProcessImageName. PROCESS_QUERY_LIMITED_INFORMATION is requested
// rather than PROCESS_QUERY_INFORMATION because it is granted for more
// processes, so a holder running as another user is still named in the common
// case instead of degrading to a bare pid.
func processName(pid int) string {
	if pid <= 0 {
		return ""
	}

	handle, _, _ := procOpenProcess.Call(processQueryLimitedInformation, 0, uintptr(pid))
	if handle == 0 {
		return ""
	}
	defer syscall.CloseHandle(syscall.Handle(handle))

	// 32768 UTF-16 units, not MAX_PATH: QueryFullProcessImageName accepts long
	// paths and truncates silently when the buffer is too small.
	buf := make([]uint16, 32768)
	size := uint32(len(buf))
	ok, _, _ := procQueryFullProcessImageNameW.Call(
		handle, 0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:size])
}
