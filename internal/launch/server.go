// Package launch provisions and supervises the embedded Sliver server, then
// generates an operator profile so the web console can attach over mTLS.
//
// The whole point of this package is to remove the manual steps the upstream
// project expects an operator to perform in the sliver-server TUI:
//
//  1. first-run asset unpack (Go toolchain + Zig + garble, extracted from the
//     server binary itself into the state directory)
//  2. `multiplayer` listener start (the gRPC surface the web console speaks)
//  3. `new-operator --permissions all` (profile generation)
//
// Each step here is idempotent, so restarts are cheap.
package launch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Options configures the embedded server.
type Options struct {
	// StateDir is SLIVER_ROOT_DIR: servers, certs, loot and the unpacked Go
	// toolchain live here. Created on first run.
	StateDir string
	// ConfigDir is where the generated operator profile is written. It is also
	// exported as SLIVER_CLIENT_CONFIGS for the web console.
	ConfigDir string
	// Operator is the operator name inside the generated profile.
	Operator string
	// MultiplayerHost / MultiplayerPort expose the gRPC surface to the console.
	MultiplayerHost string
	MultiplayerPort int
	// LogPath receives combined server stdout/stderr.
	LogPath string
}

// Server is a running sliver-server process.
type Server struct {
	opts Options
	cmd  *exec.Cmd
	bin  string

	// mu guards the exit state below. cmd.Wait() writes Cmd.ProcessState, and
	// the launcher reads it from another goroutine to report "the daemon exited
	// early" -- an unsynchronised read/write pair on the same field, which is a
	// data race even though both sides only test it.
	//
	// The Wait goroutine publishes here instead, so no reader touches
	// ProcessState at all.
	mu      sync.Mutex
	exited  bool
	exitErr error
	// ProfilePath is the generated sliver-client profile the console loads.
	ProfilePath string
	// Version is the version string read back from the generated profile
	// directory when available.
	Version string
}

const (
	// profileName is the operator profile filename (minus .json).
	profileName = "sliverreshine"

	// versionFile mirrors sliver's own asset version marker. Bumping it forces
	// a re-unpack.
	versionFile = "version"
)

// Start provisions, launches and waits for the embedded Sliver server.
//
// The returned Server owns the child process; call Stop to terminate it.
func Start(ctx context.Context, opts Options) (*Server, error) {
	opts = normalizeOptions(opts)

	if err := os.MkdirAll(opts.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	if err := os.MkdirAll(opts.ConfigDir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}

	s := &Server{opts: opts}

	bin, err := MaterializeServer()
	if err != nil {
		return nil, err
	}
	s.bin = bin

	if err := exportClientConfigs(opts.ConfigDir); err != nil {
		return nil, err
	}

	if err := s.unpack(ctx); err != nil {
		return nil, err
	}
	port, moved, err := s.pickFreePort()
	if err != nil {
		return nil, err
	}
	if moved {
		// A busy gRPC port used to be fatal, and the failure it produced was
		// invisible: the console started, served its UI and reported the server
		// unreachable. Moving to a free port keeps the launch working, and the
		// profile is regenerated below against the port actually bound.
		log.Printf("[launch] gRPC port %d is in use; switched to %d", s.opts.MultiplayerPort, port)
		s.opts.MultiplayerPort = port
	}
	if err := s.spawn(ctx); err != nil {
		return nil, err
	}
	if err := s.waitForPort(ctx); err != nil {
		s.Stop()
		return nil, err
	}
	if err := s.generateProfile(ctx); err != nil {
		s.Stop()
		return nil, err
	}
	s.ProfilePath = filepath.Join(opts.ConfigDir, profileName+".json")
	if _, err := os.Stat(s.ProfilePath); err != nil {
		s.Stop()
		return nil, fmt.Errorf("operator profile was not written to %s: %w", s.ProfilePath, err)
	}

	log.Printf("[launch] sliver server ready (gRPC %s:%d)", s.opts.MultiplayerHost, s.opts.MultiplayerPort)
	return s, nil
}

// normalizeOptions fills in the option values the launcher derives when the
// caller left them unset.
func normalizeOptions(opts Options) Options {
	if opts.Operator == "" {
		opts.Operator = "operator"
	}
	if opts.MultiplayerHost == "" {
		opts.MultiplayerHost = "127.0.0.1"
	}
	if opts.MultiplayerPort == 0 {
		opts.MultiplayerPort = 31337
	}
	return opts
}

// exportClientConfigs points the console at dir through SLIVER_CLIENT_CONFIGS,
// unless the operator has already set that variable themselves.
//
// The console discovers profiles through this variable; setting it here keeps
// the console and the launcher in agreement without any UI step. The profile
// directory is exported so the console can find it without a UI step, but only
// when the operator has not set it themselves. Overwriting an explicit value
// silently repointed the profile list at a different directory, and the symptom
// -- saved profiles missing from the console -- points at the profiles rather
// than at this line.
func exportClientConfigs(dir string) error {
	existing := os.Getenv("SLIVER_CLIENT_CONFIGS")
	if existing != "" {
		log.Printf("[launch] SLIVER_CLIENT_CONFIGS is already set to %q; leaving it alone", existing)
		return nil
	}
	if err := os.Setenv("SLIVER_CLIENT_CONFIGS", dir); err != nil {
		return fmt.Errorf("export SLIVER_CLIENT_CONFIGS: %w", err)
	}
	return nil
}

// env returns the child environment with the Sliver state directory and a
// writable Go build temp directory pinned.
//
// GOTMPDIR matters: the embedded Go toolchain resolves its scratch directory
// from the environment, and without an explicit value it can land somewhere
// unwritable (on a service-account install it tried C:\Windows\go-build* and
// died with "Access is denied", failing every implant build). Pointing it at
// our own state directory makes builds independent of the ambient environment.
func (s *Server) env() []string {
	tmp := filepath.Join(s.opts.StateDir, "tmp")
	// The error is reported rather than dropped. If this fails, GOTMPDIR points
	// at a directory that does not exist, and every implant build fails with a
	// compiler error naming neither the directory nor the permission problem --
	// which is exactly the failure this variable exists to prevent. env() cannot
	// return an error without changing every caller, so it says so instead of
	// letting the operator diagnose it from a bare compiler message.
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		log.Printf("[launch] WARNING: cannot create the build temp directory %s (%v); "+
			"implant builds will fail until this is fixed", tmp, err)
	}

	env := os.Environ()
	env = append(env,
		"SLIVER_ROOT_DIR="+s.opts.StateDir,
		"GOTMPDIR="+tmp,
		"TMPDIR="+tmp,
		"TMP="+tmp,
		"TEMP="+tmp,
	)

	// LOCALAPPDATA is set for the same reason as the temp variables, and its
	// absence is what broke obfuscated builds.
	//
	// garble -- the tool Sliver runs when the operator ticks "obfuscate" -- asks
	// Go for its build cache location, and Go answers from %LocalAppData% on
	// Windows. When the daemon is started by a launcher that scrubs the
	// environment, garble dies with
	//
	//	%LocalAppData% is not defined
	//
	// and exits 1, which reaches the console as a bare "exit status 1" naming
	// neither garble nor the variable. A non-obfuscated build never touches that
	// path, which is why only the obfuscated variant failed.
	//
	// The value is only filled in when it is missing, so an operator's real
	// profile directory is left alone.
	if os.Getenv("LOCALAPPDATA") == "" {
		env = append(env, "LOCALAPPDATA="+tmp)
	}
	// Garble also consults the user profile for its own cache on some paths.
	if os.Getenv("USERPROFILE") == "" {
		env = append(env, "USERPROFILE="+s.opts.StateDir)
	}
	if os.Getenv("HOME") == "" {
		env = append(env, "HOME="+s.opts.StateDir)
	}
	if os.Getenv("APPDATA") == "" {
		env = append(env, "APPDATA="+tmp)
	}
	return env
}

// run executes the embedded server binary and returns its combined output.
// Every invocation is given a hard timeout because the non-daemon subcommands
// are expected to exit on their own.
func (s *Server) run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Env = s.env()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s timed out after %s", strings.Join(args, " "), timeout)
	}
	return out, err
}

// toolchainStaleReason reports why the unpacked toolchain cannot be reused, or
// "" when it is current.
//
// Three separate things have to hold, and the original check only asked whether
// two directories existed:
//
//  1. go/bin is present. Without it nothing can be compiled at all.
//  2. the version marker matches the server's own commit. Sliver writes
//     ver.GitCommit into that file and re-unpacks when it differs; an install
//     left over from an older build has a toolchain of a different generation,
//     which surfaces much later as a compiler that cannot build the implant.
//  3. the toolchain is internally consistent -- the `go` binary reports the same
//     version as the compiler in pkg/tool. This is the cheap half of what `go
//     build` itself checks, and checking it here turns "version mismatch" at
//     build time into a one-time re-unpack.
//
// A missing marker is stale, not current. That case is what actually bit: an
// install whose toolchain predates the marker file has no marker, and treating
// "no marker" as fine reuses a toolchain of unknown vintage.
func (s *Server) toolchainStaleReason() string {
	goRoot := filepath.Join(s.opts.StateDir, "go")
	// Both binaries are required, not just the directory. Testing the directory
	// alone treats a tree that stopped after go.zip as complete, and the marker
	// below then pins that state permanently: the next start sees a current
	// marker and skips the unpack that would have repaired it. Checking
	// completeness here means a broken tree re-unpacks on the next start even
	// when its marker says it is current.
	for _, tool := range []string{"go", "garble"} {
		if runtime.GOOS == "windows" {
			tool += ".exe"
		}
		if _, err := os.Stat(filepath.Join(goRoot, "bin", tool)); err != nil {
			return fmt.Sprintf("toolchain incomplete: %s is missing", tool)
		}
	}

	marker := filepath.Join(s.opts.StateDir, versionFile)
	raw, err := os.ReadFile(marker)
	if err != nil {
		return fmt.Sprintf("no version marker at %s", marker)
	}
	onDisk := strings.TrimSpace(string(raw))
	if onDisk == "" {
		return "empty version marker"
	}

	if reason := toolchainVersionMismatch(goRoot); reason != "" {
		return reason
	}
	return ""
}

// toolchainVersionMismatch compares the `go` binary's version against the
// compiler that ships beside it.
//
// The comparison is between two files in the same tree, so a difference means
// the tree was assembled from two different Go releases -- a partially replaced
// unpack, a restored backup, or an install reused across builds. Running `go`
// from that tree fails with "compile: version ... does not match go tool
// version ...", and the message names neither the directory nor the fix.
//
// A tree that cannot be read is not reported as broken: the caller is about to
// run `go` anyway, and refusing on a read error would turn a permissions quirk
// into a re-unpack loop.
func toolchainVersionMismatch(goRoot string) string {
	goBin := filepath.Join(goRoot, "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	goVersionFile := filepath.Join(goRoot, "VERSION")
	// Kept deliberately narrow: this runs on the startup path, and a hung
	// subprocess here would be a hang the operator cannot see.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, goBin, "version").Output()
	if err != nil {
		return ""
	}
	reported := strings.TrimSpace(string(out))
	if !strings.HasPrefix(reported, "go version ") {
		return ""
	}
	reported = strings.Fields(reported)[2]

	raw, err := os.ReadFile(goVersionFile)
	if err != nil {
		return ""
	}
	// VERSION holds "go1.25.6\ntime 2025-08-08T19:33:32Z\n".
	expected := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	if expected == "" || expected == reported {
		return ""
	}
	return fmt.Sprintf("the unpacked toolchain is inconsistent: go is %s but VERSION says %s", reported, expected)
}

// unpack extracts the compiler assets that the server binary carries. Without
// them no implant can be built, so a failure here is fatal.
func (s *Server) unpack(ctx context.Context) error {
	if reason := s.toolchainStaleReason(); reason != "" {
		// Report the reason rather than a bare "unpacking". A stale toolchain
		// that is silently reused is the failure this exists to prevent, and
		// the operator cannot tell that from a first run without being told.
		log.Printf("[launch] re-unpacking compiler assets in %s: %s", s.opts.StateDir, reason)
	} else {
		log.Printf("[launch] compiler assets already unpacked in %s", s.opts.StateDir)
		// Repair in place as well: an install unpacked before this bit was
		// restored would otherwise stay broken until the state dir is wiped.
		return ensureExecutable(filepath.Join(s.opts.StateDir, "go", "bin"))
	}

	log.Printf("[launch] unpacking compiler assets into %s (first run, this takes a minute)", s.opts.StateDir)
	out, err := s.run(ctx, 20*time.Minute, "unpack")
	if err != nil {
		if !strings.Contains(out, "Unpacking assets") && !strings.Contains(out, "Initialized") {
			return fmt.Errorf("unpack failed: %w: %s", err, tail(out, 2000))
		}
	}
	// The check is for the binaries that actually have to exist, not for the
	// go/bin directory.
	//
	// `unpack` used to be judged by that directory alone, and it is created by
	// the first of three extractions: setupGo unzips go.zip (which makes go/bin),
	// then src.zip, then writes garble -- and it returns early on either of the
	// last two. Setup then discards setupGo's error, logs setupZig's, and writes
	// the version marker regardless, so a run that stopped after go.zip left
	// go/bin present and the marker claiming the toolchain was current. The
	// console reported "compiler assets ready", and the state was permanent:
	// toolchainStaleReason compares against that marker, so the next start
	// logged "already unpacked" and never retried.
	//
	// Every implant build then failed with a compiler error that named neither
	// the cause nor the fix.
	for _, tool := range []string{"go", "garble"} {
		if runtime.GOOS == "windows" {
			tool += ".exe"
		}
		p := filepath.Join(s.opts.StateDir, "go", "bin", tool)
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("compiler assets incomplete after unpack: %s is missing: %w", tool, err)
		}
	}
	// The embedded toolchain archives do not preserve the executable bit on
	// every entry, and `go` in particular unpacks as 0644. Windows never
	// notices because it keys executability off the extension; on Linux every
	// call fails with "Permission denied", `go tool dist list` returns nothing,
	// and every implant build dies with "invalid compiler target". Restore the
	// bit explicitly rather than trusting the archive.
	if err := ensureExecutable(filepath.Join(s.opts.StateDir, "go")); err != nil {
		return err
	}

	log.Printf("[launch] compiler assets ready")
	return nil
}

// ensureExecutable walks dir recursively, making directories traversable and
// regular files executable. It is scoped to the unpacked toolchain directory, so
// it never touches operator data.
//
// The embedded archives do not preserve the executable bit on every entry, so
// the Go toolchain unpacks with mode 0644 and nothing can run it. Recursing
// matters: fixing only go/bin leaves pkg/tool/*/compile and pkg/tool/*/link
// broken, which surfaces later as "fork/exec .../compile: permission denied".
func ensureExecutable(dir string) error {
	return filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Directories need the execute bit to be traversable at all.
			info, statErr := entry.Info()
			if statErr != nil {
				return statErr
			}
			return os.Chmod(path, info.Mode().Perm()|0o111)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		// Preserve the existing mode and just add the execute bits, so a
		// restrictive umask is not silently widened beyond executability.
		return os.Chmod(path, info.Mode().Perm()|0o111)
	})
}

// spawn starts the multiplayer daemon that serves the gRPC API.
func (s *Server) spawn(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.opts.LogPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(s.opts.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open server log: %w", err)
	}

	cmd := exec.Command(s.bin, "daemon",
		"-l", s.opts.MultiplayerHost,
		"-p", fmt.Sprint(s.opts.MultiplayerPort),
	)
	cmd.Env = s.env()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// Give the child its own process group on unix so a Ctrl-C aimed at the
	// launcher does not leave an orphaned listener behind.
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start sliver daemon: %w", err)
	}
	s.cmd = cmd
	go func() {
		// The result is published under the mutex, and kept rather than
		// discarded: this was `_ = cmd.Wait()`, so a daemon that died from a
		// signal or a non-zero exit was indistinguishable from one that stopped
		// cleanly, and nothing recorded why.
		s.publishExit(cmd.Wait())
		logFile.Close()
	}()
	log.Printf("[launch] sliver daemon started (pid %d), log: %s", cmd.Process.Pid, s.opts.LogPath)
	return nil
}

// publishExit records how the daemon stopped.
//
// It exists as a method so the write side and the read side
// (daemonExited/daemonExitReason) share one lock, and so a test can drive the
// pair concurrently under -race. Reading Cmd.ProcessState instead was a data
// race: Wait writes it from this goroutine while the launcher reads it from
// another, and both sides only testing it does not make the access safe.
func (s *Server) publishExit(err error) {
	s.mu.Lock()
	s.exited = true
	s.exitErr = err
	s.mu.Unlock()
}

// requireFreePort fails when something is already listening on the gRPC address.
//
// It runs before the daemon is spawned because the failure it prevents is
// otherwise invisible, and it is the failure that produced a console reporting
// "server unreachable" behind a log that looked healthy:
//
//	Sliver's daemon cannot bind, prints one line into its own log, and exits.
//	waitForPort then dials the address, reaches the *stranger* holding the port,
//	and answers "ready". The operator profile is written by a console command
//	with no daemon to talk to, auto-connect times out, and the UI says the server
//	is unreachable -- none of which points at a port conflict.
//
// The check is inherently racy: something can take the port between this call and
// the daemon's bind. The window is milliseconds, and the alternative is no check.
//
// When the port is taken the error names the process holding it, because that is
// the detail which turns the message into an action. The check shipped first
// without it, the operator was told only that *something* held the port, and
// finding out what cost a manual investigation -- the holder in that case was
// wslrelay.exe, a name that appears nowhere in Sliver's own log.
func (s *Server) requireFreePort(host string, port int) error {
	addr := net.JoinHostPort(host, fmt.Sprint(port))

	ln, err := net.Listen("tcp", addr)
	if err == nil {
		ln.Close()
		return nil
	}

	// Naming the holder is what turns "the port is busy" into something the
	// operator can act on without a second investigation. Best-effort by
	// construction: describeListener returns "" when the pid cannot be resolved.
	return portInUseError(addr, err, describeListener(port))
}

// pickFreePort returns the port the daemon should be started on, and whether it
// had to move off the configured one.
//
// requireFreePort is still the check; it is just no longer a fatal one. Refusing
// to start on a busy port was the failure the operator could not see: the
// console served its UI and reported the server unreachable, and the reason was
// in neither the launcher output nor the daemon log.
func (s *Server) pickFreePort() (int, bool, error) {
	if err := s.requireFreePort(s.opts.MultiplayerHost, s.opts.MultiplayerPort); err == nil {
		return s.opts.MultiplayerPort, false, nil
	}
	port, err := FreeRandomPort(s.opts.MultiplayerHost)
	if err != nil {
		return 0, false, err
	}
	return port, true, nil
}

// FreeRandomPort returns a port on host that was free a moment ago.
//
// Candidates are drawn at random rather than walked upwards, so a restart does
// not collide with whatever the previous run left behind and two instances
// starting together do not race each other to the same next number. The final
// fallback is the OS allocator (":0"), used only when the whole random range is
// somehow unusable.
//
// The port is free when it is probed, not when it is used: the caller binds it a
// moment later, and something can take it in between. That race is inherent to
// any pre-flight check and is the same one requireFreePort documents.
func FreeRandomPort(host string) (int, error) {
	for attempt := 0; attempt < 32; attempt++ {
		port := 1024 + rand.IntN(65535-1024)
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			continue
		}
		ln.Close()
		return port, nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, fmt.Errorf("no free port on %s: %w", host, err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// describeListener names the process holding port, or "" when it cannot be
// determined.
//
// Best-effort on purpose, and it has to stay that way: the port being unavailable
// is the fact that matters, and failing to name the holder must degrade to the
// message that already worked rather than turn a clear error into a vague one.
// Everything here is therefore optional detail layered on top -- including the
// syscall, which is why it lives behind the platform files.
//
// A bare pid is still an improvement over no name at all: it is the handle the
// operator needs to investigate, and it survives the case where the executable
// cannot be read because the holder belongs to another user and the query is
// refused.
func describeListener(port int) string {
	pid := listeningPID(port)
	if pid <= 0 {
		return ""
	}
	// filepath.Base because the platform helper returns a full path: the
	// operator needs "wslrelay.exe", not "C:\\Windows\\System32\\wslrelay.exe".
	if name := processName(pid); name != "" {
		return fmt.Sprintf("%s (pid %d)", filepath.Base(name), pid)
	}
	return fmt.Sprintf("pid %d", pid)
}

// portInUseError builds the port-conflict message.
//
// Split out from requireFreePort so both shapes -- with and without a named
// holder -- can be asserted without depending on whether the machine running the
// tests can resolve a pid, which is the environment-dependent part.
//
// With no holder the raw listen error is kept: it is then the only evidence
// left, and it names the address and the syscall failure.
func portInUseError(addr string, cause error, holder string) error {
	const consequence = "The embedded Sliver daemon cannot bind while another process " +
		"holds it, so the console would start, serve its UI and report the server unreachable. " +
		"Stop that process, or set a different mpPort in the settings file"
	if holder == "" {
		return fmt.Errorf("gRPC address %s is already in use (%v). %s", addr, cause, consequence)
	}
	return fmt.Errorf("gRPC address %s is already in use by %s. %s", addr, holder, consequence)
}

// daemonExited reports whether the daemon process has been reaped.
//
// It reads state published by the Wait goroutine rather than Cmd.ProcessState,
// because that field is written by Wait and reading it from here is a data race.
func (s *Server) daemonExited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// daemonExitReason describes how the daemon stopped, for the error the operator
// sees. A nil error means it exited on its own with status 0.
func (s *Server) daemonExitReason() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitErr
}

// waitForPort blocks until the multiplayer listener accepts connections.
func (s *Server) waitForPort(ctx context.Context) error {
	addr := net.JoinHostPort(s.opts.MultiplayerHost, fmt.Sprint(s.opts.MultiplayerPort))
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		if s.daemonExited() {
			return fmt.Errorf("sliver daemon exited early (%v), see %s", s.daemonExitReason(), s.opts.LogPath)
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for sliver daemon on %s, see %s", addr, s.opts.LogPath)
}

// generateProfile runs `operator` with the interactive console attached so the
// "already exists?" prompt is answered automatically. The profile is written
// straight into the config dir the web console scans.
func (s *Server) generateProfile(ctx context.Context) error {
	target := filepath.Join(s.opts.ConfigDir, profileName+".json")
	if existing, err := ReadProfile(target); err == nil {
		if existing.LHost == s.opts.MultiplayerHost && existing.LPort == s.opts.MultiplayerPort {
			log.Printf("[launch] operator profile already present: %s", target)
			return nil
		}
		// The profile points at a listener the daemon is not serving: the port
		// moved after a fallback, or the operator edited mpPort. Reusing it
		// makes auto-connect dial an address nothing answers, and the console
		// reports the server unreachable -- the same symptom as a dead daemon,
		// from a completely different cause.
		log.Printf("[launch] operator profile %s targets %s:%d, regenerating for %s:%d",
			target, existing.LHost, existing.LPort, s.opts.MultiplayerHost, s.opts.MultiplayerPort)
	}

	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.bin, "operator",
		"--name", s.opts.Operator,
		"--lhost", s.opts.MultiplayerHost,
		"--lport", fmt.Sprint(s.opts.MultiplayerPort),
		"--permissions", "all",
		// --save takes an explicit filename here: when handed a directory the
		// generator derives the name from the lhost flag (producing
		// operator_127.0.0.1.cfg), which the console would never find.
		"--save", target,
		"--output", "file",
	)
	cmd.Env = s.env()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start operator generator: %w", err)
	}

	go feedConfirmations(stdin)

	waitErr := cmd.Wait()
	return checkGeneratedProfile(target, waitErr, buf.String())
}

// feedConfirmations answers the generator's overwrite prompt.
//
// The generator asks before overwriting an existing operator record. Feed a
// stream of confirmations rather than parsing the prompt, which keeps this
// robust across upstream wording changes.
func feedConfirmations(stdin io.WriteCloser) {
	defer stdin.Close()
	scanner := bufio.NewScanner(strings.NewReader(strings.Repeat("y\n", 16)))
	for scanner.Scan() {
		if _, err := io.WriteString(stdin, scanner.Text()+"\n"); err != nil {
			return
		}
		time.Sleep(120 * time.Millisecond)
	}
}

// checkGeneratedProfile judges success by whether the profile is complete and
// usable, not by whether the file happens to exist.
//
// The old check was: if the generator exited non-zero AND the file was absent,
// report failure. A generator that wrote a partial file and then died therefore
// returned nil and logged "operator profile generated", while the file failed to
// parse -- and the damage was permanent, because Start stats that path and skips
// regeneration on every later run. The file holds the operator's mTLS key and
// token.
//
// So the file is parsed here. A partial write fails that parse and the caller is
// told, rather than the console starting against a profile it cannot use.
func checkGeneratedProfile(target string, waitErr error, output string) error {
	if _, err := ReadProfile(target); err != nil {
		how := "the generator reported success"
		if waitErr != nil {
			how = waitErr.Error()
		}
		return fmt.Errorf("operator profile at %s is not usable (%s): %w: %s",
			target, how, err, tail(output, 2000))
	}
	if waitErr != nil {
		// Usable but a non-zero exit: worth saying, since the generator may have
		// signalled a problem with part of what it did.
		log.Printf("[launch] operator generator exited non-zero (%v) but wrote a usable profile: %s",
			waitErr, target)
	}
	log.Printf("[launch] operator profile generated: %s", target)
	return nil
}

// Stop terminates the daemon and everything it forked.
//
// It kills the daemon's process group rather than the single pid: the daemon
// leads its own group (see setSysProcAttr) and its children inherit it, so the
// group kill is what actually frees the gRPC port. Killing only the leader left
// the listener bound and made a finished shutdown look like it had not worked.
func (s *Server) Stop() {
	if s == nil || s.cmd == nil {
		return
	}
	terminateProcess(s.cmd)
}

// PID returns the daemon process id, or 0 when not running.
func (s *Server) PID() int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
}

// MultiplayerPort reports the gRPC port the daemon was actually started on.
//
// It can differ from the port in the options the caller passed: Start moves to a
// free port when the configured one is already held. Anything that prints the
// address, or that decides whether a profile is still valid, has to read it back
// from here rather than from its own copy of the options.
func (s *Server) MultiplayerPort() int {
	if s == nil {
		return 0
	}
	return s.opts.MultiplayerPort
}

// BinaryPath reports which sliver-server binary the launcher is using.
func (s *Server) BinaryPath() string { return s.bin }

// tail returns the last n bytes of s, for compact error reporting.
func tail(s string, n int) string {
	if len(s) <= n {
		return strings.TrimSpace(s)
	}
	return "..." + strings.TrimSpace(s[len(s)-n:])
}

// ProfileSummary is a redacted view of the generated operator profile, safe to
// show in the web console.
type ProfileSummary struct {
	Operator string `json:"operator"`
	LHost    string `json:"lhost"`
	LPort    int    `json:"lport"`
	Path     string `json:"path"`
}

type profileFile struct {
	Operator string `json:"operator"`
	LHost    string `json:"lhost"`
	LPort    int    `json:"lport"`
}

// ReadProfile parses the generated profile for display purposes.
func ReadProfile(path string) (*ProfileSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p profileFile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &ProfileSummary{Operator: p.Operator, LHost: p.LHost, LPort: p.LPort, Path: path}, nil
}

// platformAssetName is retained for diagnostics in logs.
func platformAssetName() string { return runtime.GOOS + "/" + runtime.GOARCH }
