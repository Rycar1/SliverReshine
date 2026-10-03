package api

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	"sliverreshine/internal/ui/sliver"
)

// This file holds the two terminal modes that add compatibility at the cost of
// interaction. Both run entirely on the console, which is the point: implant
// changes require rebuilding and redistributing payloads, while these work
// against sessions that are already checked in.
//
//	shell-copy  a real shell, but copied to a writable temp directory first.
//	exec        no shell at all; each line is split and run directly.
//
// The mode is chosen per connection so an operator can try another without
// disturbing the session or any other terminal open on it.

// execPrompt renders the synthetic prompt used by exec mode.
//
// There is no remote shell to print one, so the console does. It is a
// deliberate fiction made visible: the absence of a real prompt is the clearest
// signal that this is not a shell, and the operator should be able to see which
// mode they are in at a glance.
func execPrompt(cwd string) string {
	if cwd == "" {
		cwd = "?"
	}
	return fmt.Sprintf("%s $ ", cwd)
}

// runExecTerminal implements exec mode.
//
// It reads a line, runs it, prints the result, and repeats. There is no tunnel,
// no PTY and no shell: the console splits the line itself and calls Execute. The
// result is a terminal that works on a host with no shell whatsoever -- a
// distroless or scratch container still has whatever binary you name -- at the
// cost of prompts, pipes, redirection, globbing and builtins.
//
// Input arrives as raw keystrokes rather than lines, so the console also has to
// do the local echo and line editing that a remote shell would normally provide.
// That editor is execSession, below.
func (s *Server) runExecTerminal(ws *websocket.Conn, c *sliver.Client, sessionID string) {
	e := newExecSession(ws, c, sessionID)
	e.banner()

	reader := newWSFrameReader(ws)
	for {
		msgType, payload, err := nextFrame(reader)
		if err != nil {
			return
		}
		switch msgType {
		case wsMsgClose:
			return
		case wsMsgResize:
			// Nothing to resize: there is no remote terminal.
			continue
		case wsMsgData:
			if e.handleData(payload) {
				return
			}
		}
	}
}

// execSession is the console-side line editor exec mode needs, because there is
// no remote shell to provide one.
type execSession struct {
	ws   *websocket.Conn
	c    *sliver.Client
	id   string
	cwd  string
	line strings.Builder
}

// newExecSession starts an editor for one terminal. The working directory is
// read once up front so the first prompt can show it.
func newExecSession(ws *websocket.Conn, c *sliver.Client, sessionID string) *execSession {
	cwd, err := c.Pwd(sessionID)
	if err != nil {
		// A working directory is not required to run a command; the target has
		// one whether or not we can read it.
		cwd = ""
	}
	return &execSession{ws: ws, c: c, id: sessionID, cwd: cwd}
}

// banner prints the one-time notice that this is not a shell.
func (e *execSession) banner() {
	_ = writeWS(e.ws, wsMsgData, []byte(
		"[*] exec mode: no shell is used, each line is run directly.\r\n"+
			"[*] Pipes, redirection, globbing and command chaining are not available.\r\n"+
			"[*] cd / pwd / exit are handled here. Type \"exit\" to close.\r\n\r\n"))
}

// handleData consumes one frame of keystrokes. It reports whether the operator
// asked to leave, in which case the terminal must end.
func (e *execSession) handleData(payload []byte) (done bool) {
	for _, b := range payload {
		if e.handleKey(b) {
			return true
		}
	}
	return false
}

// handleKey applies one keystroke to the line buffer, echoing it locally.
func (e *execSession) handleKey(b byte) (done bool) {
	switch b {
	case '\r', '\n':
		// Enter. Echo the newline, run the line, print a new prompt.
		return e.submit()
	case 0x7f, 0x08:
		e.backspace()
	case 0x03:
		// Ctrl+C: there is no foreground process group to signal. Clearing the
		// line is the honest approximation.
		e.line.Reset()
		_ = writeWS(e.ws, wsMsgData, []byte("^C\r\n"+execPrompt(e.cwd)))
	default:
		if b < 0x20 && b != '\t' {
			return false
		}
		e.line.WriteByte(b)
		_ = writeWS(e.ws, wsMsgData, []byte{b})
	}
	return false
}

// submit runs the buffered line and prints its result.
func (e *execSession) submit() (done bool) {
	_ = writeWS(e.ws, wsMsgData, []byte("\r\n"))
	cmdLine := strings.TrimSpace(e.line.String())
	e.line.Reset()
	if cmdLine == "" {
		e.prompt()
		return false
	}
	res := e.c.RunExecLine(e.id, cmdLine, &e.cwd)
	if sliver.IsExecExit(res) {
		// Fatal: the operator asked to leave. Sending a plain close made the
		// browser treat "exit" as a dropped connection and reopen the terminal,
		// so the only way out was to close the tab.
		_ = writeWS(e.ws, wsMsgFatal, []byte("[*] exit\r\n"))
		return true
	}
	_ = writeWS(e.ws, wsMsgData, []byte(formatExecResult(res)))
	if e.cwd == "" || res.Handled {
		// A cd may have moved the working directory; refresh it so the prompt
		// tells the truth.
		if p, err := e.c.Pwd(e.id); err == nil {
			e.cwd = p
		}
	}
	e.prompt()
	return false
}

// backspace erases one byte locally, since nothing remote is echoing.
func (e *execSession) backspace() {
	cur := e.line.String()
	if cur == "" {
		return
	}
	e.line.Reset()
	e.line.WriteString(cur[:len(cur)-1])
	_ = writeWS(e.ws, wsMsgData, []byte("\b \b"))
}

// prompt writes the synthetic prompt for the current working directory.
func (e *execSession) prompt() {
	_ = writeWS(e.ws, wsMsgData, []byte(execPrompt(e.cwd)))
}

// formatExecResult renders one command's outcome the way a shell would.
func formatExecResult(res sliver.ExecLineResult) string {
	var b strings.Builder
	if res.Err != nil {
		fmt.Fprintf(&b, "[!] %s\r\n", res.Err)
		return b.String()
	}
	out := res.Stdout
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	b.WriteString(strings.ReplaceAll(out, "\n", "\r\n"))
	if res.Stderr != "" {
		errOut := strings.ReplaceAll(res.Stderr, "\n", "\r\n")
		b.WriteString(errOut)
	}
	// A non-zero exit is reported explicitly. There is no shell to surface it
	// through $?, and a silently failed command is worse than a noisy one.
	if res.ExitCode != 0 {
		fmt.Fprintf(&b, "[exit %d]\r\n", res.ExitCode)
	}
	return b.String()
}

// stageShellForCopyMode prepares a shell in the target's temp directory and
// reports what it did.
//
// The report matters as much as the copy. A shell-copy that silently fell back
// to the original path would leave the operator believing the mode had done
// something, which is exactly the confusion this project keeps finding in its
// own error paths.
func (s *Server) stageShellForCopyMode(ws *websocket.Conn, c *sliver.Client, sessionID, shellPath string) (string, bool) {
	_ = writeWS(ws, wsMsgData, []byte("[*] shell-copy: staging a shell in the target's temp directory...\r\n"))

	done := make(chan *sliver.TempShellResult, 1)
	failed := make(chan error, 1)
	go func() {
		res, err := c.StageShellForSession(sessionID, shellPath)
		if err != nil {
			failed <- err
			return
		}
		done <- res
	}()

	select {
	case res := <-done:
		_ = writeWS(ws, wsMsgData, []byte(fmt.Sprintf(
			"[*] copied %s -> %s (%d bytes)\r\n\r\n", res.Source, res.Path, res.Bytes)))
		return res.Path, true
	case err := <-failed:
		// Fatal, not retryable: this failure comes from the target refusing the
		// upload, and a reconnect re-runs the same upload against the same
		// target. Reporting it as a plain close is what produced an endless
		// reconnect loop that re-attempted a copy which could never succeed.
		_ = writeWS(ws, wsMsgFatal, []byte("shell-copy failed: "+err.Error()))
		return "", false
	case <-time.After(3 * time.Minute):
		// Fatal as well. A timeout is not obviously deterministic, but it has
		// already consumed three minutes; retrying silently restarts that clock
		// and leaves the operator with a terminal that never resolves.
		_ = writeWS(ws, wsMsgFatal, []byte("shell-copy timed out while staging the shell"))
		return "", false
	}
}
