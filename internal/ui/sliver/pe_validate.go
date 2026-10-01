package sliver

import (
	"encoding/binary"
	"fmt"
)

// validatePEPayload reports why a blob cannot be treated as a PE, or "" when it
// plausibly can.
//
// This exists because a malformed payload did not fail -- it panicked. The RDI
// converter in the sliver tree reads a DOS header and indexes into a PE header
// without checking lengths, so a body of a few bytes reached
//
//	panic: runtime error: slice bounds out of range [:64] with capacity 8
//
// inside the sliver-server process. The daemon exited, every live session died
// with it, and the console was left reporting the server unreachable.
//
// It was reachable with a three-byte body, because the console's only check had
// been "is the field present and base64-decodable", which "AAAA" passes.
//
// The bounds check belongs in the converter too, and now is. Refusing the
// request here is what turns a crash into a 400 with a sentence. It is checked
// in both places because the two failures are different in kind: one is a bad
// request, the other is a daemon that must not die.
func ValidatePEPayload(data []byte) string {
	// A DOS header is 64 bytes, and e_lfanew at offset 0x3c gives the PE header
	// offset. Anything shorter cannot be a PE, and is exactly what crashed the
	// converter.
	const dosHeaderLen = 64
	if len(data) < dosHeaderLen {
		return fmt.Sprintf("payload is %d bytes, too short to be a PE (needs at least %d)",
			len(data), dosHeaderLen)
	}
	if data[0] != 'M' || data[1] != 'Z' {
		return "payload does not start with the MZ signature, so it is not a PE"
	}
	peOffset := int(binary.LittleEndian.Uint32(data[60:64]))
	if peOffset < dosHeaderLen {
		return fmt.Sprintf("payload has an implausible PE header offset (%d)", peOffset)
	}
	// The PE signature is 4 bytes and the COFF machine field is 2 more, which is
	// what the converter reads next.
	if peOffset+6 > len(data) {
		return fmt.Sprintf("payload's PE header offset (%d) points past the end of the %d-byte payload",
			peOffset, len(data))
	}
	if string(data[peOffset:peOffset+4]) != "PE\x00\x00" {
		return "payload's PE header offset does not point at a PE signature"
	}
	return ""
}
