package api

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/net/websocket"
)

// ---------------------------------------------------------------------------
// nextFrame
// ---------------------------------------------------------------------------

func TestNextFrameReadsFramesInOrder(t *testing.T) {
	stream := bytes.Join([][]byte{
		buildFrame(wsMsgData, []byte("ls\r")),
		buildFrame(wsMsgResize, []byte(`{"cols":120,"rows":30}`)),
		buildFrame(wsMsgClose, nil),
	}, nil)
	// 7 bytes per read: headers and payloads straddle read boundaries.
	r := newWSFrameReader(&chunkReader{data: stream, chunk: 7})

	want := []struct {
		msgType uint8
		payload string
	}{
		{wsMsgData, "ls\r"},
		{wsMsgResize, `{"cols":120,"rows":30}`},
		{wsMsgClose, ""},
	}
	for i, w := range want {
		msgType, payload, err := nextFrame(r)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if msgType != w.msgType || string(payload) != w.payload {
			t.Errorf("frame %d = (0x%02x, %q), want (0x%02x, %q)",
				i, msgType, payload, w.msgType, w.payload)
		}
	}
	if _, _, err := nextFrame(r); err != io.EOF {
		t.Errorf("after the last frame: err = %v, want io.EOF", err)
	}
}

// A declared length over the cap must be skipped, not buffered. The length is
// read straight off the wire, so buffering it lets one client exhaust the
// console's memory.
func TestNextFrameSkipsAnOversizedFrame(t *testing.T) {
	const declared = maxWSFramePayload + 1

	oversized := make([]byte, 5)
	oversized[0] = wsMsgData
	binary.BigEndian.PutUint32(oversized[1:], declared)

	junk := bytes.Repeat([]byte("j"), declared)
	after := buildFrame(wsMsgData, []byte("after"))

	r := newWSFrameReader(bytes.NewReader(append(append(oversized, junk...), after...)))
	msgType, payload, err := nextFrame(r)
	if err != nil {
		t.Fatalf("nextFrame: %v", err)
	}
	if msgType != wsMsgData || string(payload) != "after" {
		t.Errorf("got (0x%02x, %q), want the frame after the oversized one", msgType, payload)
	}
}

// The cap itself is allowed through: only a length strictly above it is skipped.
func TestNextFrameAcceptsTheMaximumPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("m"), maxWSFramePayload)

	msgType, got, err := nextFrame(newWSFrameReader(bytes.NewReader(buildFrame(wsMsgData, payload))))
	if err != nil {
		t.Fatalf("nextFrame: %v", err)
	}
	if msgType != wsMsgData || !bytes.Equal(got, payload) {
		t.Errorf("frame at the cap was not delivered intact (type 0x%02x, %d bytes)", msgType, len(got))
	}
}

func TestNextFrameReportsATruncatedFrame(t *testing.T) {
	frame := buildFrame(wsMsgData, []byte("abc"))
	// Declares 10 bytes but carries 3.
	binary.BigEndian.PutUint32(frame[1:], 10)

	if _, _, err := nextFrame(newWSFrameReader(bytes.NewReader(frame))); err == nil {
		t.Fatal("nextFrame accepted a truncated frame")
	}
}

func TestNextFrameReportsATruncatedHeader(t *testing.T) {
	if _, _, err := nextFrame(newWSFrameReader(bytes.NewReader([]byte{wsMsgData, 0, 0}))); err == nil {
		t.Fatal("nextFrame accepted a truncated header")
	}
}

// ---------------------------------------------------------------------------
// sameOriginHandshake
// ---------------------------------------------------------------------------

// The handshake is the only point at which a cross-site WebSocket hijack can be
// stopped: once upgraded, the console is already speaking to whatever asked.
func TestSameOriginHandshakeDecidesOnOriginAlone(t *testing.T) {
	cases := []struct {
		name    string
		origin  string
		host    string
		wantErr bool
	}{
		{"no origin is allowed for non-browser clients", "", "console.example:8443", false},
		{"same origin", "https://console.example:8443", "console.example:8443", false},
		{"same authority over http", "http://console.example:8443", "console.example:8443", false},
		{"host case is not significant", "https://Console.Example:8443", "console.example:8443", false},
		{"a path after the authority is ignored", "https://console.example:8443/", "console.example:8443", false},
		{"foreign origin", "https://evil.example", "console.example:8443", true},
		{"same host on another port", "https://console.example:9999", "console.example:8443", true},
		{"scheme-relative origin", "//console.example:8443", "console.example:8443", true},
		{"origin with no scheme", "console.example:8443", "console.example:8443", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://"+tc.host+"/ws/sessions/s1/terminal", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}

			err := sameOriginHandshake(&websocket.Config{}, r)
			if (err != nil) != tc.wantErr {
				t.Errorf("sameOriginHandshake(origin=%q, host=%q) = %v, want error = %v",
					tc.origin, tc.host, err, tc.wantErr)
			}
		})
	}
}
