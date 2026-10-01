package launch

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// verifyExecutableFormat exists to replace an opaque loader error with a sentence
// that names the problem. The audit found it checking the OS magic and the
// 32/64-bit class but not the CPU the image is built for -- so an ARM64 PE on an
// x64 host passed every check and then failed exactly the way the function was
// written to prevent. These tests pin the dimension that was missing.

// writePE builds a minimal but structurally valid PE image with the given
// machine value, 64-bit class, and no CLR directory.
func writePE(t *testing.T, machine uint16) string {
	t.Helper()

	const peOff = 0x80
	const optSize = 240 // PE32+ optional header with all data directories
	buf := make([]byte, peOff+4+20+optSize)

	buf[0], buf[1] = 'M', 'Z'
	binary.LittleEndian.PutUint32(buf[0x3C:], peOff)
	binary.LittleEndian.PutUint32(buf[peOff:], 0x00004550) // "PE\0\0"

	coff := peOff + 4
	binary.LittleEndian.PutUint16(buf[coff:], machine)    // Machine
	binary.LittleEndian.PutUint16(buf[coff+16:], optSize) // SizeOfOptionalHeader

	// Optional header magic: PE32+ (0x20B). Not a DLL.
	opt := peOff + 4 + 20
	binary.LittleEndian.PutUint16(buf[opt:], 0x20B)

	path := filepath.Join(t.TempDir(), "payload.exe")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeELF builds a minimal ELF64 image with the given e_machine.
func writeELF(t *testing.T, machine uint16) string {
	t.Helper()

	buf := make([]byte, 64)
	copy(buf, []byte{0x7F, 'E', 'L', 'F'})
	buf[4] = 2 // ELFCLASS64
	buf[5] = 1 // ELFDATA2LSB
	binary.LittleEndian.PutUint16(buf[18:], machine)

	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Only run on the architectures the constants cover; on anything else the check
// deliberately abstains, and the abstention is asserted separately below.
func TestVerifyExecutableFormatRejectsWrongMachine(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skipf("this assertion is written for amd64 hosts, running on %s", runtime.GOARCH)
	}

	// An ARM64 PE is a 64-bit PE32+ image. It passed every check the old code
	// had, and this is the case that produced the reported startup failure.
	if runtime.GOOS == "windows" {
		arm := writePE(t, peMachineARM64)
		err := verifyExecutableFormat(arm)
		if err == nil {
			t.Fatal("an ARM64 PE was accepted on an amd64 host")
		}
		if !strings.Contains(err.Error(), "arm64") || !strings.Contains(err.Error(), "amd64") {
			t.Errorf("the error does not name both architectures: %v", err)
		}
	}

	armELF := writeELF(t, elfMachineARM64)
	if err := verifyExecutableFormat(armELF); err == nil && runtime.GOOS == "linux" {
		t.Error("an ARM64 ELF was accepted on an amd64 host")
	}
}

func TestVerifyExecutableFormatAcceptsMatchingMachine(t *testing.T) {
	switch runtime.GOARCH {
	case "amd64":
		if runtime.GOOS == "windows" {
			if err := verifyExecutableFormat(writePE(t, peMachineAMD64)); err != nil {
				t.Errorf("a matching PE was rejected: %v", err)
			}
		}
		if runtime.GOOS == "linux" {
			if err := verifyExecutableFormat(writeELF(t, elfMachineAMD64)); err != nil {
				t.Errorf("a matching ELF was rejected: %v", err)
			}
		}
	default:
		t.Skipf("no fixture for %s", runtime.GOARCH)
	}
}

// An unknown machine must not be refused. The launcher can be built for a
// platform this table has not been taught, and refusing there would break a
// working deployment to catch a mistake it cannot make.
func TestVerifyExecutableFormatAbstainsOnUnknownMachine(t *testing.T) {
	if runtime.GOARCH != "amd64" {
		t.Skipf("written for amd64 hosts, running on %s", runtime.GOARCH)
	}

	// 0xDEAD is not a machine this table knows; the wide check must abstain
	// rather than treat "unrecognised" as "wrong".
	if err := verifyMachine(writePE(t, 0xDEAD), readHead(t, writePE(t, 0xDEAD))); err != nil {
		t.Errorf("an unrecognised machine was refused: %v", err)
	}
}

func readHead(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	head := make([]byte, 4)
	n, _ := f.Read(head)
	return head[:n]
}

func TestMachineMatchesHostAbstainsOffTheTable(t *testing.T) {
	// verifyMachine filters unrecognised machine values out before it calls this,
	// so the only values that reach here are ones the table knows. The assertion
	// is therefore about the comparison itself: a known machine that is not this
	// host's is refused.
	if runtime.GOARCH == "amd64" {
		if machineMatchesHost(peMachineARM64) {
			t.Error("an ARM64 image should not match an amd64 host")
		}
		if !machineMatchesHost(peMachineAMD64) {
			t.Error("an amd64 image should match an amd64 host")
		}
	}
}

// A truncated or nonsense file must not panic. This runs on bytes that may have
// come from a damaged extraction.
func TestVerifyMachineHandlesMalformedInput(t *testing.T) {
	dir := t.TempDir()

	cases := map[string][]byte{
		"empty":        {},
		"mz only":      {'M', 'Z'},
		"elf only":     {0x7F, 'E', 'L', 'F'},
		"short pe":     append([]byte{'M', 'Z'}, bytes.Repeat([]byte{0}, 10)...),
		"garbage":      bytes.Repeat([]byte{0xAB}, 256),
		"unreadable h": {'M', 'Z', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}

	for name, data := range cases {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_"))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		// The only requirement is that it returns; the value is not meaningful
		// for input this malformed.
		_ = verifyMachine(path, data[:min(4, len(data))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
