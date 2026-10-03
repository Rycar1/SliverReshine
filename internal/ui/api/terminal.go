package api

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/websocket"

	"c2tool/internal/ui/sliver"
)

const (
	wsMsgData   = uint8(0x01)
	wsMsgResize = uint8(0x02)
	wsMsgClose  = uint8(0x03)

	// wsMsgFatal ends the terminal for good: the server decided it cannot
	// continue and reconnecting would reproduce the same failure.
	//
	// wsMsgClose and wsMsgFatal are not interchangeable, and collapsing them
	// into one type is what made the browser loop. The client treats a close as
	// "the connection dropped, try again", which is right for a session that
	// went away and wrong for a terminal the server refused to open at all: a
	// failed shell-copy was retried every few seconds forever, re-running the
	// same upload that had just failed, while the operator watched a terminal
	// that appeared to be reconnecting to something. The operator typing "exit"
	// took the same path, so leaving a terminal also reconnected it.
	wsMsgFatal = uint8(0x04)
)

// handleTerminalWS upgrades the HTTP connection to a WebSocket and bridges it
// to an interactive Sliver shell tunnel.
//
// Wire format (both directions):
//
//	[1 byte msgType][4 byte length][payload]
//
// Client -> server:
//
//	0x01: raw terminal bytes (DEL -> BS and bare CR -> CRLF are rewritten for
//	      Windows sessions)
//	0x02: JSON {"cols":N,"rows":N} (resize, forwarded to the shell)
//	0x03: close (sends "exit" to the shell)
//
// Server -> client:
//
//	0x01: raw terminal bytes
//	0x03: tunnel closed
//	0x04: fatal -- the terminal will not recover, do not reconnect
//
// The 0x04 code and the resize forwarding were both added after this comment was
// first written, and the comment kept describing the old behaviour: it listed only
// 0x01 and 0x03 for the server, and claimed resize was "acked but not forwarded to
// PTY" while the handler calls ResizeShell. A wire-format comment that disagrees
// with the code is worse than none, because it is what the next reader trusts.
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/ws/sessions/")
	rest = strings.TrimSuffix(rest, "/terminal")
	id := rest

	// An empty id used to flow into the tunnel manager and fail there, on an
	// already-upgraded socket -- which the operator sees as a terminal that opens
	// and immediately dies. Refusing before the upgrade keeps this failure in the
	// same shape as every other bad request.
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return
	}

	websocket.Server{
		Handshake: sameOriginHandshake,
		Handler: func(ws *websocket.Conn) {
			// x/net/websocket defaults to text frames; the frontend reads raw
			// binary frames (ArrayBuffer), so force binary on both directions.
			ws.PayloadType = websocket.BinaryFrame
			// The shell is chosen per connection rather than per session, so a
			// hanging terminal can be retried with a different program without
			// touching the session. Validated against a small allowlist: the
			// value is interpolated into an exec on the target, and "any path"
			// would turn a convenience into a remote-execution primitive for
			// anyone who can reach the console.
			mode := sliver.NormalizeTerminalMode(r.URL.Query().Get("mode"))
			shell := shellPathFor(r.URL.Query().Get("shell"))
			s.runTerminal(ws, c, id, shell, mode)
		},
	}.ServeHTTP(w, r)
}

// sameOriginHandshake rejects a WebSocket upgrade whose Origin is not this
// console.
//
// This is the only point at which the check is possible. Once the connection is
// upgraded the browser has committed and the console is already speaking to
// whatever asked, so a check in the read loop would be a check on a socket that
// should never have been opened. A terminal is an interactive shell on a
// compromised host: the thing a cross-site WebSocket hijack would hand over is
// exactly that.
//
// A missing Origin is allowed. Non-browser clients do not send one, and unlike
// the HTTP path there is no credential-replay problem to solve for them: a
// socket carries the credentials of whoever opened it, and a script that opens
// one already knows the password. The origin check exists to stop a *browser*
// from being used as the deputy.
func sameOriginHandshake(config *websocket.Config, r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	if !sameOrigin(origin, r.Host) {
		return fmt.Errorf("cross-origin websocket refused")
	}
	return nil
}

// shellShortcuts maps the names an operator can put in the terminal URL onto the
// paths an implant can execute.
//
// A name is offered rather than a bare path because the useful choice is "don't
// use PowerShell", not "use C:\Windows\System32\cmd.exe" -- the operator should
// not have to know the layout of a Windows install to work around a shell that
// will not start.
var shellShortcuts = map[string]string{
	"cmd":        "C:\\Windows\\System32\\cmd.exe",
	"powershell": "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
	"pwsh":       "C:\\Program Files\\PowerShell\\7\\pwsh.exe",
	"sh":         "/bin/sh",
	"bash":       "/bin/bash",
}

// shellPathFor turns the ?shell= query value into a path the implant will run,
// or "" to let the implant choose.
//
// An unrecognised name is refused rather than passed through. The value ends up
// in an exec on a remote host, and while the console already offers that ability
// through its exec and sideload endpoints, a query string that silently becomes
// "run this program on the target" is a worse interface than one that only
// accepts what it advertises. An absolute path is still allowed, because a shell
// outside the usual locations is a real case -- portable PowerShell, SysWOW64,
// a hardened image -- and refusing it would push the operator back to guessing.
func shellPathFor(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if p, ok := shellShortcuts[strings.ToLower(name)]; ok {
		return p
	}
	if isAbsolutePath(name) {
		return name
	}
	return ""
}

// isAbsolutePath reports whether p is rooted, on either platform, so a relative
// path cannot be resolved against whatever the implant's working directory
// happens to be.
func isAbsolutePath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		return true
	}
	// UNC: \\server\share
	return strings.HasPrefix(p, "\\\\")
}

// maxWSFramePayload caps an incoming client frame so a corrupt header cannot
// force an unbounded read.
const maxWSFramePayload = 1 << 20

// wsFrameReader reassembles our [type][len][payload] frames. x/net/websocket's
// Conn.Read fills the caller's buffer and returns the rest of the same frame on
// subsequent calls, so a frame larger than the read buffer spans several reads;
// leftover bytes are kept here.
type wsFrameReader struct {
	r   io.Reader
	buf []byte
}

// newWSFrameReader wraps a byte source (the WebSocket connection) in a
// reassembling frame reader.
func newWSFrameReader(r io.Reader) *wsFrameReader {
	return &wsFrameReader{r: r}
}

// readFull returns exactly n bytes of the current frame.
func (r *wsFrameReader) readFull(n int) ([]byte, error) {
	for len(r.buf) < n {
		tmp := make([]byte, 8192)
		m, err := r.r.Read(tmp)
		if m > 0 {
			r.buf = append(r.buf, tmp[:m]...)
		}
		if err != nil {
			if len(r.buf) >= n {
				break
			}
			return nil, err
		}
	}
	out := r.buf[:n]
	r.buf = r.buf[n:]
	return out, nil
}

// skip discards exactly n bytes without retaining them.
//
// This exists for the oversized-frame path. The frame length is read straight
// off the wire, so it is attacker-controlled and can be as large as 4 GiB;
// routing it through readFull would append the whole thing to r.buf before
// returning, letting one client exhaust the console's memory. n is int64 so a
// declared length above 2^31 cannot overflow into a negative on a 32-bit build.
func (r *wsFrameReader) skip(n int64) error {
	// Bytes already buffered count towards the total.
	if drop := int64(len(r.buf)); drop > 0 {
		if drop > n {
			drop = n
		}
		r.buf = r.buf[drop:]
		n -= drop
	}

	buf := make([]byte, 8192)
	for n > 0 {
		want := int64(len(buf))
		if want > n {
			want = n
		}
		m, err := r.r.Read(buf[:want])
		if m > 0 {
			n -= int64(m)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runTerminal bridges one browser terminal to one implant shell.
//
// shellPath is the program to run on the target, taken from the WebSocket query
// string. Empty means the implant picks its default. It is exposed because the
// default is not always the one that works: a Windows target whose PowerShell
// never becomes interactive leaves the operator with a blank terminal and no way
// to try cmd.exe, which is both the workaround and the diagnosis.
//
// The body is a list of phases -- dispatch, tunnel, shell, pump -- so the two
// directions of the bridge live in their own functions instead of in two halves
// of one 180-line loop.
func (s *Server) runTerminal(ws *websocket.Conn, c *sliver.Client, sessionID, shellPath, mode string) {
	// exec mode uses no tunnel at all, so it never reaches the shell path below.
	// Dispatching here rather than in the WebSocket handler keeps the two modes
	// behind one entry point, which is where an operator expects the choice to
	// be applied.
	if mode == sliver.TerminalModeExec {
		s.runExecTerminal(ws, c, sessionID)
		return
	}
	defer ws.Close()

	tm, err := sliver.NewTunnelManager(c)
	if err != nil {
		// Retryable: this is the console failing to establish its gRPC stream to
		// the server, which is often transient (the server is restarting, or
		// briefly busy). Reconnecting is the right response.
		_ = writeWS(ws, wsMsgClose, []byte("failed to open tunnel stream: "+err.Error()))
		return
	}
	defer tm.Close()

	enablePTY, windowsSession := terminalTargetProfile(c, sessionID)

	// shell-copy stages a shell in the target's temp directory and runs that
	// instead. It survives path-based policy (execution allowed from temp but
	// not from System32) and read-only system volumes, at the cost of one
	// upload and a leftover file on the target.
	if mode == sliver.TerminalModeShellCopy {
		staged, ok := s.stageShellForCopyMode(ws, c, sessionID, shellPath)
		if !ok {
			return
		}
		shellPath = staged
	}

	tunnel, err := tm.StartShell(sessionID, shellPath, enablePTY)
	if err != nil {
		// Deterministic: the target refused this shell. Reconnecting re-sends the
		// identical request and fails identically, so the terminal ends here
		// instead of starting a retry loop.
		_ = writeWS(ws, wsMsgFatal, []byte("failed to start shell: "+err.Error()))
		return
	}

	// The console codec transcodes between the target's OEM code page and UTF-8.
	//
	// A Windows shell spawned without a console writes its OEM code page -- 936
	// on a Chinese install -- while the browser reads UTF-8, so passing the bytes
	// through is what produced "Microsoft Windows [汾 6.3.9600]" from
	// "Microsoft Windows [版本 6.3.9600]". Exec mode already decoded its output;
	// the shell tunnel did not, so the same target read correctly in one mode and
	// as mojibake in the other. It is nil for a target that needs no transcoding,
	// and every method tolerates that.
	codec := c.NewConsoleCodec(sessionID)

	// No local echo, deliberately.
	//
	// Typing looks dead until Enter, which is worth explaining because the
	// obvious fix is wrong. A console-less cmd.exe does not echo keystrokes --
	// there is no console to do it -- but it DOES echo the completed line once it
	// reads it, right after the prompt. Echoing locally as well therefore prints
	// the command twice on one line:
	//
	//	C:\>whoamiwhoami
	//
	// which reads as a different command than the one that ran. Leaving the echo
	// to the shell is the lesser evil, and it is what the operator sees today.
	//
	// The real fix is a console, not an echo: ConPTY gives Windows a PTY and
	// Windows 10 1809+ has it. Windows 8.1 does not, and neither does anything
	// older, so on those targets the shell reads from a pipe and the choice is
	// between a delayed echo and a doubled one.
	//
	// A unix session is unaffected: its PTY echoes as the operator types.

	// Tunnel -> WS: forward implant output to the browser.
	go forwardTunnelToWS(ws, tunnel, codec)

	// WS -> tunnel: forward browser keystrokes to the implant.
	pumpWSToTunnel(ws, tm, tunnel, sessionID, codec, windowsSession)
}

// nextFrame reads one [type][len][payload] frame from the browser.
//
// A frame whose declared length exceeds maxWSFramePayload is discarded without
// being buffered. The length is read straight off the wire, so it is
// attacker-controlled, and reading it into the frame reader is what would let
// one client exhaust the console's memory.
//
// The error is the read error that ended the connection: io.EOF for a clean
// close, anything else for a connection that broke.
func nextFrame(r *wsFrameReader) (msgType uint8, payload []byte, err error) {
	for {
		header, rerr := r.readFull(5)
		if rerr != nil {
			return 0, nil, rerr
		}
		length := binary.BigEndian.Uint32(header[1:])
		if length > maxWSFramePayload {
			if serr := r.skip(int64(length)); serr != nil {
				return 0, nil, serr
			}
			continue
		}
		body, berr := r.readFull(int(length))
		if berr != nil {
			return 0, nil, berr
		}
		return header[0], body, nil
	}
}

// terminalTargetProfile reports what the session's OS means for the tunnel:
// whether a PTY can be requested, and whether the keystroke rewrite for a
// console-less Windows shell is needed.
//
// A session that cannot be found is treated as neither: the tunnel still opens
// and the operator gets the conservative behaviour rather than a refusal.
func terminalTargetProfile(c *sliver.Client, sessionID string) (enablePTY, windowsSession bool) {
	sessions, err := c.Sessions()
	if err != nil {
		return false, false
	}
	for _, s := range sessions {
		if s.ID != sessionID {
			continue
		}
		switch s.OS {
		case "linux", "darwin":
			enablePTY = true
		case "windows":
			windowsSession = true
		}
		return enablePTY, windowsSession
	}
	return false, false
}

// forwardTunnelToWS copies implant output to the browser until the tunnel ends,
// transcoding the target's code page to UTF-8 on the way.
func forwardTunnelToWS(ws *websocket.Conn, tunnel *sliver.TunnelIO, codec *sliver.ConsoleCodec) {
	buf := make([]byte, 8192)
	for {
		n, err := tunnel.Read(buf)
		if n > 0 {
			out := codec.Decode(buf[:n])
			if len(out) > 0 {
				if werr := writeWS(ws, wsMsgData, out); werr != nil {
					return
				}
			}
		}
		if err != nil {
			_ = writeWS(ws, wsMsgClose, []byte{})
			return
		}
	}
}

// pumpWSToTunnel reads browser frames and applies them to the shell tunnel until
// the connection ends.
//
// A connection that breaks is passed on to the shell as "exit": the alternative
// leaves a half-typed command sitting at a prompt nobody is watching.
func pumpWSToTunnel(ws *websocket.Conn, tm *sliver.TunnelManager, tunnel *sliver.TunnelIO, sessionID string, codec *sliver.ConsoleCodec, windowsSession bool) {
	reader := newWSFrameReader(ws)
	for {
		msgType, payload, err := nextFrame(reader)
		if err != nil {
			if err != io.EOF {
				_, _ = tunnel.Write([]byte("exit\n"))
			}
			return
		}
		switch msgType {
		case wsMsgResize:
			applyResize(tm, sessionID, tunnel.ID, payload)
		case wsMsgClose:
			_, _ = tunnel.Write([]byte("exit\n"))
			return
		case wsMsgData:
			if len(payload) > 0 {
				if windowsSession {
					payload = translateBackspace(normalizeCRLF(payload))
				}
				// UTF-8 from the browser into the target's code page. Without
				// this a non-ASCII character is sent as UTF-8 bytes that the
				// shell reads as two or three unrelated OEM characters, so a
				// path or a filename the operator typed correctly names
				// something that does not exist.
				_, _ = tunnel.Write(codec.Encode(payload))
			}
		}
	}
}

// applyResize forwards a browser resize frame to the shell.
//
// This used to parse the frame and drop it, so a remote shell never learned the
// window size and full-screen programs laid themselves out for 80x24.
//
// A malformed frame is ignored rather than closing the terminal over: the shell
// still works at whatever size it already had.
func applyResize(tm *sliver.TunnelManager, sessionID string, tunnelID uint64, payload []byte) {
	var dims struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	}
	if err := json.Unmarshal(payload, &dims); err != nil {
		return
	}
	// Clamped to what the wire type holds before the conversion -- a negative or
	// oversized value would otherwise wrap on the uint16 cast and resize the
	// terminal to nonsense.
	if dims.Rows <= 0 || dims.Cols <= 0 {
		return
	}
	if dims.Rows > 0xffff {
		dims.Rows = 0xffff
	}
	if dims.Cols > 0xffff {
		dims.Cols = 0xffff
	}
	_ = tm.ResizeShell(sessionID, tunnelID, uint16(dims.Rows), uint16(dims.Cols))
}

// translateBackspace maps DEL (0x7f) to BS (0x08). xterm.js sends DEL for the
// backspace key; Windows shells running over a pipe have no console, so they
// pass DEL through literally and corrupt the command line. BS is the edit
// character honored by both Windows and POSIX shells. This must only be applied
// to Windows sessions — POSIX shells (canonical mode / readline) treat DEL as
// their native erase character.
func translateBackspace(b []byte) []byte {
	hasDel := false
	for _, c := range b {
		if c == 0x7f {
			hasDel = true
			break
		}
	}
	if !hasDel {
		return b
	}
	out := make([]byte, len(b))
	for i, c := range b {
		if c == 0x7f {
			out[i] = 0x08
		} else {
			out[i] = c
		}
	}
	return out
}

// normalizeCRLF rewrites bare CR (carriage return) bytes into CRLF. Windows
// pipes shells (powershell.exe/cmd.exe) run without a console, so a lone \r
// only moves the cursor back to column 0 instead of starting a new line.
// Existing CRLF sequences are left untouched.
func normalizeCRLF(b []byte) []byte {
	cr := false
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			cr = true
			break
		}
	}
	if !cr {
		return b
	}
	out := make([]byte, 0, len(b)+8)
	for i := 0; i < len(b); i++ {
		if b[i] == '\r' {
			out = append(out, '\r')
			if i+1 >= len(b) || b[i+1] != '\n' {
				out = append(out, '\n')
			}
		} else {
			out = append(out, b[i])
		}
	}
	return out
}

// writeWS frames a WebSocket message: [type][4-byte BE length][payload].
func writeWS(ws *websocket.Conn, msgType uint8, payload []byte) error {
	frame := make([]byte, 1+4+len(payload))
	frame[0] = msgType
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	_, err := ws.Write(frame)
	return err
}
