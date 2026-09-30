package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestTranslateBackspace(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, ""},
		{"no del", []byte("ls -la\r"), "ls -la\r"},
		{"single del", []byte("ab\x7f"), "ab\x08"},
		{"all dels", []byte{0x7f, 0x7f, 0x7f}, "\x08\x08\x08"},
		{"mixed", []byte("ab\x08\x7f"), "ab\x08\x08"},
		{"multibyte untouched", []byte("你好\x7f"), "你好\x08"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateBackspace(tt.in)
			if string(got) != tt.want {
				t.Fatalf("translateBackspace(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeCRLF(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"bare cr", "ls\r", "ls\r\n"},
		{"existing crlf untouched", "ls\r\n", "ls\r\n"},
		{"mixed", "a\rb\r\nc", "a\r\nb\r\nc"},
		{"cr at end", "cmd\r", "cmd\r\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(normalizeCRLF([]byte(tt.in)))
			if got != tt.want {
				t.Fatalf("normalizeCRLF(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// chunkReader hands out at most chunk bytes per Read, simulating a WebSocket
// Conn whose Read returns a frame payload across multiple calls.
type chunkReader struct {
	data  []byte
	pos   int
	chunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := r.chunk
	if n > len(r.data)-r.pos {
		n = len(r.data) - r.pos
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

// buildFrame renders a [type][4-byte BE len][payload] frame.
func buildFrame(msgType byte, payload []byte) []byte {
	frame := make([]byte, 5+len(payload))
	frame[0] = msgType
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	copy(frame[5:], payload)
	return frame
}

func TestWSFrameReaderReassemblesSplitFrames(t *testing.T) {
	// Frame payloads larger than the 8192-byte read buffer exercise the
	// reassembly path (Conn.Read fills the buffer and returns the rest of the
	// same frame on the next call).
	big := bytes.Repeat([]byte("x"), 20_000)
	stream := bytes.Join([][]byte{
		buildFrame(wsMsgData, []byte("ls\r")),
		buildFrame(wsMsgResize, []byte(`{"cols":120,"rows":30}`)),
		buildFrame(wsMsgData, big),
		buildFrame(wsMsgClose, nil),
	}, nil)

	// 6 bytes per read: header+payload straddle read boundaries constantly.
	reader := newWSFrameReader(&chunkReader{data: stream, chunk: 6})

	var got [][]byte
	var types []byte
	for {
		header, err := reader.readFull(5)
		if err != nil {
			break
		}
		length := binary.BigEndian.Uint32(header[1:])
		payload, err := reader.readFull(int(length))
		if err != nil {
			t.Fatalf("readFull payload: %v", err)
		}
		types = append(types, header[0])
		got = append(got, payload)
	}

	if len(got) != 4 {
		t.Fatalf("expected 4 frames, got %d", len(got))
	}
	if string(got[0]) != "ls\r" {
		t.Fatalf("frame 0 = %q, want %q", got[0], "ls\r")
	}
	if string(got[1]) != `{"cols":120,"rows":30}` {
		t.Fatalf("frame 1 = %q", got[1])
	}
	if len(got[2]) != 20_000 || !bytes.Equal(got[2], big) {
		t.Fatalf("frame 2 size = %d, want 20000", len(got[2]))
	}
	if string(got[3]) != "" {
		t.Fatalf("frame 3 = %q, want empty", got[3])
	}
	if !bytes.Equal(types, []byte{wsMsgData, wsMsgResize, wsMsgData, wsMsgClose}) {
		t.Fatalf("types = %v", types)
	}
}

func TestWSFrameReaderSurfacesReadErrors(t *testing.T) {
	reader := newWSFrameReader(&chunkReader{data: buildFrame(wsMsgData, []byte("hi")), chunk: 1})
	if _, err := reader.readFull(5); err != nil {
		t.Fatalf("unexpected error reading header: %v", err)
	}
	if _, err := reader.readFull(2); err != nil {
		t.Fatalf("unexpected error reading payload: %v", err)
	}
	// Stream is exhausted; the next header read must surface EOF.
	if _, err := reader.readFull(5); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}
