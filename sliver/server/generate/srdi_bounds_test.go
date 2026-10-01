package generate

import (
	"encoding/binary"
	"testing"
)

// is64BitDLL indexed a byte slice without checking its length, which crashed the
// daemon rather than failing the request.
//
// The console reaches this through POST /api/shellcode/rdi. A three-byte body --
// "AAAA" in base64 -- produced
//
//	panic: runtime error: slice bounds out of range [:64] with capacity 8
//
// and sliver-server exited, taking every live session with it. These tests pin
// the guard; the console validates the same shape one layer earlier, but the RPC
// is reachable directly, so the converter has to be safe on its own.

func TestIs64BitDLLDoesNotPanicOnShortInput(t *testing.T) {
	for _, n := range []int{0, 1, 2, 8, 32, 60, 63} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("is64BitDLL panicked on a %d-byte input: %v", n, r)
				}
			}()
			if is64BitDLL(make([]byte, n)) {
				t.Errorf("is64BitDLL reported a %d-byte blob as 64-bit", n)
			}
		}()
	}
}

// The same slice expression with a different trigger: a header offset that is
// in range for the field but past the end of the buffer.
func TestIs64BitDLLDoesNotPanicOnAnOutOfRangeHeaderOffset(t *testing.T) {
	b := make([]byte, 64)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[60:64], 0xFFFFFF)

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("is64BitDLL panicked on an out-of-range header offset: %v", r)
		}
	}()
	if is64BitDLL(b) {
		t.Error("an out-of-range header offset was reported as 64-bit")
	}
}

// The guard must not break detection: a real amd64 PE still has to be seen.
func TestIs64BitDLLStillDetectsA64BitPE(t *testing.T) {
	b := make([]byte, 512)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[60:64], 64)
	copy(b[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[68:70], 34404) // machineAMD64

	if !is64BitDLL(b) {
		t.Error("a valid amd64 PE was not detected as 64-bit")
	}
}

// And a 32-bit PE must still be reported as 32-bit, so the positive test above
// is not passing for the wrong reason.
func TestIs64BitDLLReportsA32BitPE(t *testing.T) {
	b := make([]byte, 512)
	b[0], b[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(b[60:64], 64)
	copy(b[64:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[68:70], 0x014c) // machineI386

	if is64BitDLL(b) {
		t.Error("a 32-bit PE was reported as 64-bit")
	}
}
