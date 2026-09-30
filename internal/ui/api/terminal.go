package api

import (
	"encoding/binary"
	"encoding/json"
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
//	0x02: JSON {"cols":N,"rows":N} (resize; acked but not forwarded to PTY)
//	0x03: close (sends "exit" to the shell)
//
// Server -> client:
//
//	0x01: raw terminal bytes
//	0x03: tunnel closed
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/ws/sessions/")
	rest = strings.TrimSuffix(rest, "/terminal")
	id := rest

	websocket.Server{Handler: func(ws *websocket.Conn) {
		// x/net/websocket defaults to text frames; the frontend reads raw
		// binary frames (ArrayBuffer), so force binary on both directions.
		ws.PayloadType = websocket.BinaryFrame
		s.runTerminal(ws, c, id)
	}}.ServeHTTP(w, r)
}

//	maxWSFramePayload caps an incoming client frame so a corrupt header cannot
//	force an unbounded read.
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

func (s *Server) runTerminal(ws *websocket.Conn, c *sliver.Client, sessionID string) {
	defer ws.Close()

	tm, err := sliver.NewTunnelManager(c)
	if err != nil {
		_ = writeWS(ws, wsMsgClose, []byte("failed to open tunnel stream: "+err.Error()))
		return
	}
	defer tm.Close()

	// Only enable PTY for unix-like sessions.
	enablePTY := false
	windowsSession := false
	if sessions, err := c.Sessions(); err == nil {
		for _, s := range sessions {
			if s.ID == sessionID {
				if s.OS == "linux" || s.OS == "darwin" {
					enablePTY = true
				}
				if s.OS == "windows" {
					windowsSession = true
				}
				break
			}
		}
	}

	tunnel, err := tm.StartShell(sessionID, enablePTY)
	if err != nil {
		_ = writeWS(ws, wsMsgClose, []byte("failed to start shell: "+err.Error()))
		return
	}

	// Tunnel -> WS: forward implant output to the browser.
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := tunnel.Read(buf)
			if n > 0 {
				if werr := writeWS(ws, wsMsgData, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = writeWS(ws, wsMsgClose, []byte{})
				return
			}
		}
	}()

	// WS -> tunnel: forward browser keystrokes to the implant.
	reader := newWSFrameReader(ws)
	for {
		header, err := reader.readFull(5)
		if err != nil {
			if err != io.EOF {
				_, _ = tunnel.Write([]byte("exit\n"))
			}
			return
		}
		msgType := header[0]
		length := binary.BigEndian.Uint32(header[1:])
		// Oversized frame: discard it without buffering. The length is read
		// straight off the wire, so it is attacker-controlled; reading it into
		// r.buf is what would let one client exhaust the console's memory.
		if length > maxWSFramePayload {
			if err := reader.skip(int64(length)); err != nil {
				return
			}
			continue
		}
		payload, err := reader.readFull(int(length))
		if err != nil {
			if err != io.EOF {
				_, _ = tunnel.Write([]byte("exit\n"))
			}
			return
		}
		switch msgType {
		case wsMsgResize:
			var dims struct {
				Cols int `json:"cols"`
				Rows int `json:"rows"`
			}
			_ = json.Unmarshal(payload, &dims)
		case wsMsgClose:
			_, _ = tunnel.Write([]byte("exit\n"))
			return
		case wsMsgData:
			if len(payload) > 0 {
				if windowsSession {
					payload = translateBackspace(normalizeCRLF(payload))
				}
				_, _ = tunnel.Write(payload)
			}
		}
	}
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
