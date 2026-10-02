package api

import (
	"fmt"
	"strings"
	"time"

	"golang.org/x/net/websocket"

	"c2tool/internal/ui/sliver"
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
func (s *Server) runExecTerminal(ws *websocket.Conn, c *sliver.Client, sessionID string) {
	cwd, err := c.Pwd(sessionID)
	if err != nil {
		// A working directory is not required to run a command; the target has
		// one whether or not we can read it.
		cwd = ""
	}

	_ = writeWS(ws, wsMsgData, []byte(
		"[*] exec mode: no shell is used, each line is run directly.\r\n"+
			"[*] Pipes, redirection, globbing and command chaining are not available.\r\n"+
			"[*] cd / pwd / exit are handled here. Type \"exit\" to close.\r\n\r\n"))

	reader := newWSFrameReader(ws)
	var line strings.Builder

	for {
		header, err := reader.readFull(5)
		if err != nil {
			return
		}
		length := int(be32(header[1:]))
		if length > maxWSFramePayload {
			if err := reader.skip(int64(length)); err != nil {
				return
			}
			continue
		}
		payload, err := reader.readFull(length)
		if err != nil {
			return
		}

		switch header[0] {
		case wsMsgClose:
			return
		case wsMsgResize:
			// Nothing to resize: there is no remote terminal.
			continue
		case wsMsgData:
			for _, b := range payload {
				switch b {
				case '\r', '\n':
					// Enter. Echo the newline, run the line, print a new prompt.
					_ = writeWS(ws, wsMsgData, []byte("\r\n"))
					cmdLine := strings.TrimSpace(line.String())
					line.Reset()
					if cmdLine == "" {
						_ = writeWS(ws, wsMsgData, []byte(execPrompt(cwd)))
						continue
					}
					res := c.RunExecLine(sessionID, cmdLine, &cwd)
					if sliver.IsExecExit(res) {
						// Fatal: the operator asked to leave. Sending a plain close made
						// the browser treat "exit" as a dropped connection and reopen the
						// terminal, so the only way out was to close the tab.
						_ = writeWS(ws, wsMsgFatal, []byte("[*] exit\r\n"))
						return
					}
					_ = writeWS(ws, wsMsgData, []byte(formatExecResult(res)))
					if cwd == "" || res.Handled {
						// A cd may have moved the working directory; refresh it so
						// the prompt tells the truth.
						if p, perr := c.Pwd(sessionID); perr == nil {
							cwd = p
						}
					}
					_ = writeWS(ws, wsMsgData, []byte(execPrompt(cwd)))
				case 0x7f, 0x08:
					// Backspace: erase locally, since nothing remote is echoing.
					cur := line.String()
					if cur == "" {
						continue
					}
					line.Reset()
					line.WriteString(cur[:len(cur)-1])
					_ = writeWS(ws, wsMsgData, []byte("\b \b"))
				case 0x03:
					// Ctrl+C: there is no foreground process group to signal.
					// Clearing the line is the honest approximation.
					line.Reset()
					_ = writeWS(ws, wsMsgData, []byte("^C\r\n"+execPrompt(cwd)))
				default:
					if b < 0x20 && b != '\t' {
						continue
					}
					line.WriteByte(b)
					_ = writeWS(ws, wsMsgData, []byte{b})
				}
			}
		}
	}
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

// be32 reads a big-endian uint32 without pulling in encoding/binary for one call.
func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
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
