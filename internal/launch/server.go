// Package launch provisions and supervises the embedded Sliver server, then
// generates an operator profile so the web console can attach over mTLS.
//
// The whole point of this package is to remove the manual steps the upstream
// project expects an operator to perform in the sliver-server TUI:
//
//	1. first-run asset unpack (Go toolchain + Zig + garble, extracted from the
//	   server binary itself into the state directory)
//	2. `multiplayer` listener start (the gRPC surface the web console speaks)
//	3. `new-operator --permissions all` (profile generation)
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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

	// ProfilePath is the generated sliver-client profile the console loads.
	ProfilePath string
	// Version is the version string read back from the generated profile
	// directory when available.
	Version string
}

const (
	// profileName is the operator profile filename (minus .json).
	profileName = "c2tool"

	// versionFile mirrors sliver's own asset version marker. Bumping it forces
	// a re-unpack.
	versionFile = "version"
)

// Start provisions, launches and waits for the embedded Sliver server.
//
// The returned Server owns the child process; call Stop to terminate it.
func Start(ctx context.Context, opts Options) (*Server, error) {
	if opts.Operator == "" {
		opts.Operator = "operator"
	}
	if opts.MultiplayerHost == "" {
		opts.MultiplayerHost = "127.0.0.1"
	}
	if opts.MultiplayerPort == 0 {
		opts.MultiplayerPort = 31337
	}
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

	// The console discovers profiles through this variable; setting it here
	// keeps the console and the launcher in agreement without any UI step.
	_ = os.Setenv("SLIVER_CLIENT_CONFIGS", opts.ConfigDir)

	if err := s.unpack(ctx); err != nil {
		return nil, err
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

	log.Printf("[launch] sliver server ready (gRPC %s:%d)", opts.MultiplayerHost, opts.MultiplayerPort)
	return s, nil
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
	_ = os.MkdirAll(tmp, 0o700)

	env := os.Environ()
	env = append(env,
		"SLIVER_ROOT_DIR="+s.opts.StateDir,
		"GOTMPDIR="+tmp,
		"TMPDIR="+tmp,
		"TMP="+tmp,
		"TEMP="+tmp,
	)
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

// unpack extracts the compiler assets that the server binary carries. Without
// them no implant can be built, so a failure here is fatal.
func (s *Server) unpack(ctx context.Context) error {
	marker := filepath.Join(s.opts.StateDir, versionFile)
	if _, err := os.Stat(filepath.Join(s.opts.StateDir, "go", "bin")); err == nil {
		if _, err := os.Stat(marker); err == nil {
			log.Printf("[launch] compiler assets already unpacked in %s", s.opts.StateDir)
			// Repair in place as well: an install unpacked before this bit was
			// restored would otherwise stay broken until the state dir is wiped.
			return ensureExecutable(filepath.Join(s.opts.StateDir, "go", "bin"))
		}
	}
	log.Printf("[launch] unpacking compiler assets into %s (first run, this takes a minute)", s.opts.StateDir)
	out, err := s.run(ctx, 20*time.Minute, "unpack")
	if err != nil {
		if !strings.Contains(out, "Unpacking assets") && !strings.Contains(out, "Initialized") {
			return fmt.Errorf("unpack failed: %w: %s", err, tail(out, 2000))
		}
	}
	if _, err := os.Stat(filepath.Join(s.opts.StateDir, "go", "bin")); err != nil {
		return fmt.Errorf("compiler assets missing after unpack: %w", err)
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
		_ = cmd.Wait()
		logFile.Close()
	}()
	log.Printf("[launch] sliver daemon started (pid %d), log: %s", cmd.Process.Pid, s.opts.LogPath)
	return nil
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
		if s.cmd != nil && s.cmd.ProcessState != nil && s.cmd.ProcessState.Exited() {
			return fmt.Errorf("sliver daemon exited early, see %s", s.opts.LogPath)
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
	if _, err := os.Stat(target); err == nil {
		log.Printf("[launch] operator profile already present: %s", target)
		return nil
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

	// The generator asks before overwriting an existing operator record. Feed
	// a stream of confirmations rather than parsing the prompt, which keeps
	// this robust across upstream wording changes.
	go func() {
		defer stdin.Close()
		scanner := bufio.NewScanner(strings.NewReader(strings.Repeat("y\n", 16)))
		for scanner.Scan() {
			if _, err := io.WriteString(stdin, scanner.Text()+"\n"); err != nil {
				return
			}
			time.Sleep(120 * time.Millisecond)
		}
	}()

	if err := cmd.Wait(); err != nil {
		if _, statErr := os.Stat(target); statErr != nil {
			return fmt.Errorf("operator generation failed: %w: %s", err, tail(buf.String(), 2000))
		}
	}
	log.Printf("[launch] operator profile generated: %s", target)
	return nil
}

// Stop terminates the daemon process.
func (s *Server) Stop() {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Kill()
}

// PID returns the daemon process id, or 0 when not running.
func (s *Server) PID() int {
	if s == nil || s.cmd == nil || s.cmd.Process == nil {
		return 0
	}
	return s.cmd.Process.Pid
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
