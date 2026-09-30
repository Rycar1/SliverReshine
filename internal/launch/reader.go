package launch

import "bytes"

// newByteReader wraps a byte slice in a reader. Kept as a tiny helper so the
// gzip path reads the same way whether the payload came from embed or from a
// test fixture.
func newByteReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }
