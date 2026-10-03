package launch

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The machine-name tables feed the operator-facing message that explains why a
// payload was refused. A name that comes back empty for a machine the table
// knows would silently turn "this is an arm64 image" into "this is a  image",
// which is the one thing the message exists to say.

func TestPeMachineNameCoversTheTable(t *testing.T) {
	cases := map[uint16]string{
		peMachineI386:  "386",
		peMachineAMD64: "amd64",
		peMachineARM:   "arm",
		peMachineARM64: "arm64",
	}
	for machine, want := range cases {
		if got := peMachineName(machine); got != want {
			t.Errorf("peMachineName(%#x) = %q, want %q", machine, got, want)
		}
	}
	if got := peMachineName(0xDEAD); got != "" {
		t.Errorf("an unknown PE machine produced %q, want empty", got)
	}
}

func TestElfMachineNameCoversTheTable(t *testing.T) {
	cases := map[uint16]string{
		elfMachineI386:  "386",
		elfMachineAMD64: "amd64",
		elfMachineARM:   "arm",
		elfMachineARM64: "arm64",
	}
	for machine, want := range cases {
		if got := elfMachineName(machine); got != want {
			t.Errorf("elfMachineName(%#x) = %q, want %q", machine, got, want)
		}
	}
	if got := elfMachineName(0xDEAD); got != "" {
		t.Errorf("an unknown ELF machine produced %q, want empty", got)
	}
}

// hexMagic turns an unrecognised file into an actionable message. The "empty"
// case is reached by a zero-byte file, which is a truncated extraction.
func TestHexMagic(t *testing.T) {
	if got := hexMagic(nil); got != "empty" {
		t.Errorf("hexMagic(nil) = %q, want %q", got, "empty")
	}
	if got := hexMagic([]byte{0x4d, 0x5a, 0x90, 0x00}); got != "4d 5a 90 00" {
		t.Errorf("hexMagic = %q, want %q", got, "4d 5a 90 00")
	}
}

// want64 is the class table the 32/64-bit checks share.
func TestWant64(t *testing.T) {
	for _, arch := range []string{
		"amd64", "arm64", "ppc64", "ppc64le", "riscv64", "s390x", "mips64", "mips64le", "loong64",
	} {
		if !want64(arch) {
			t.Errorf("want64(%q) = false, want true", arch)
		}
	}
	for _, arch := range []string{"386", "arm", "mips", "mipsle", "wasm", ""} {
		if want64(arch) {
			t.Errorf("want64(%q) = true, want false", arch)
		}
	}
}

// is32BitImage decides whether a file that already passed the magic check is
// the width this host can run. Getting it wrong either refuses a valid 64-bit
// payload or accepts a 32-bit one.
func TestIs32BitImage(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// A PE header with the given optional-header magic at the offset
	// is32BitImage reads (e_lfanew + 24).
	pe := func(classMagic uint16) []byte {
		const peOff = 0x80
		buf := make([]byte, peOff+24+2)
		buf[0], buf[1] = 'M', 'Z'
		binary.LittleEndian.PutUint32(buf[0x3C:], peOff)
		binary.LittleEndian.PutUint32(buf[peOff:], 0x00004550) // "PE\0\0"
		binary.LittleEndian.PutUint16(buf[peOff+24:], classMagic)
		return buf
	}
	// An ELF header with the given EI_CLASS byte.
	elf := func(class byte) []byte {
		buf := make([]byte, 64)
		copy(buf, []byte{0x7F, 'E', 'L', 'F'})
		buf[4] = class
		return buf
	}

	pe32 := write("pe32.bin", pe(0x10B))
	pe64 := write("pe64.bin", pe(0x20B))
	elf32 := write("elf32.bin", elf(1))
	elf64 := write("elf64.bin", elf(2))
	unknown := write("unknown.bin", []byte{0x00, 0x01, 0x02, 0x03})

	cases := []struct {
		name string
		head []byte
		path string
		want bool
	}{
		{"PE32 is 32-bit", []byte{'M', 'Z'}, pe32, true},
		{"PE32+ is not", []byte{'M', 'Z'}, pe64, false},
		{"ELFCLASS32 is 32-bit", []byte{0x7F, 'E', 'L', 'F'}, elf32, true},
		{"ELFCLASS64 is not", []byte{0x7F, 'E', 'L', 'F'}, elf64, false},
		{"an unknown magic is not", []byte{0x00, 0x01, 0x02, 0x03}, unknown, false},
	}
	for _, tc := range cases {
		if got := is32BitImage(tc.head, tc.path); got != tc.want {
			t.Errorf("%s: is32BitImage = %v, want %v", tc.name, got, tc.want)
		}
	}
}
