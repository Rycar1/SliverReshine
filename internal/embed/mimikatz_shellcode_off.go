//go:build !mimikatzshellcode

package embed

// MimikatzShellcode is nil in a build that did not generate the donut blob.
//
// The file internal/embed/mimikatz/mimikatz.x64.bin is produced by
// build/build-mimikatz-shellcode.ps1 and is not in the repository, so the
// //go:embed directive for it lives behind a build tag. Without this file a
// clone that has not run donut would not compile at all, which would make the
// in-memory path a build requirement rather than an upgrade.
//
// A nil blob is handled, not an error: runMimikatzInMemory reports that the
// build carries no shellcode and the operator falls back to the upload path.
var MimikatzShellcode []byte
