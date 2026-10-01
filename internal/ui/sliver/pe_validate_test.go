package sliver

import (
	"encoding/binary"
	"strings"
	"testing"
)

// Both of these payloads killed the sliver-server process before the checks
// existed. The console routes reachable without a session were the dangerous
// ones: an operator does not have to be authenticated as anyone in particular to
// send them, and a malformed body took the daemon -- and every live session --
// down with it.

// The exact body that crashed it: "AAAA" base64-decodes to three zero bytes, and
// the RDI converter indexed [60:64] into it.
func TestValidatePEPayloadRejectsTheThreeByteCrashPayload(t *testing.T) {
	problem := ValidatePEPayload([]byte{0x00, 0x00, 0x00})
	if problem == "" {
		t.Fatal("a 3-byte payload was accepted; the RDI converter panics on this")
	}
	if !strings.Contains(problem, "too short") {
		t.Errorf("message should say it is too short, got: %s", problem)
	}
}

func TestValidatePEPayloadRejectsShortAndMisalignedBlobs(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"empty", nil, "too short"},
		{"one byte", []byte{0x4d}, "too short"},
		{"63 bytes", make([]byte, 63), "too short"},
		{"no MZ", append([]byte{0x00, 0x00}, make([]byte, 200)...), "MZ"},
		// 64 bytes with a valid MZ but an e_lfanew of 0, which points back into
		// the DOS header.
		{"pe offset inside dos header", func() []byte {
			b := make([]byte, 64)
			b[0], b[1] = 'M', 'Z'
			binary.LittleEndian.PutUint32(b[60:64], 4)
			return b
		}(), "implausible"},
		// e_lfanew pointing past the end of the buffer.
		{"pe offset past end", func() []byte {
			b := make([]byte, 64)
			b[0], b[1] = 'M', 'Z'
			binary.LittleEndian.PutUint32(b[60:64], 5000)
			return b
		}(), "past the end"},
		// In range, but not a PE signature there.
		{"no pe signature", func() []byte {
			b := make([]byte, 200)
			b[0], b[1] = 'M', 'Z'
			binary.LittleEndian.PutUint32(b[60:64], 64)
			copy(b[64:], "XXXX")
			return b
		}(), "PE signature"},
	}
	for _, tc := range cases {
		problem := ValidatePEPayload(tc.data)
		if problem == "" {
			t.Errorf("%s: accepted a blob that would crash the converter", tc.name)
			continue
		}
		if !strings.Contains(problem, tc.want) {
			t.Errorf("%s: message %q does not contain %q", tc.name, problem, tc.want)
		}
	}
}

func TestValidatePEPayloadAcceptsAWellFormedHeader(t *testing.T) {
	b := make([]byte, 512)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[60:64], 64)
	copy(b[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[68:70], 0x8664) // amd64 machine

	if problem := ValidatePEPayload(b); problem != "" {
		t.Errorf("a valid PE header was refused: %s", problem)
	}
}
