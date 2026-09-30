//go:build mimikatzshellcode

package embed

import _ "embed"

// MimikatzShellcode is the donut-generated shellcode form of the embedded
// mimikatz executable.
//
// Built by build/build-mimikatz-shellcode.ps1, which runs donut over
// mimikatz/mimikatz.x64.exe and writes the .bin next to it. The release build
// adds the `mimikatzshellcode` tag only after that file exists, because the
// embed directive below fails to compile when it does not.
//
// The blob is what makes the in-memory path work for mimikatz: donut resolves
// the executable into position-independent shellcode, which is the one form
// Sliver's injection can execute without a file ever touching the target's disk.
//
//go:embed mimikatz/mimikatz.x64.bin
var MimikatzShellcode []byte
