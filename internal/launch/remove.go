package launch

import "os"

// removeQuietly deletes path, ignoring any error.
//
// Every caller is on a cleanup path: it is either about to return a more
// useful error, or discarding a temporary file whose absence is not itself a
// problem. Propagating a removal error there would mask the real failure or
// report noise the caller cannot act on.
func removeQuietly(path string) {
	_ = os.Remove(path)
}
