package sliver

import (
	"strings"
	"testing"
)

// The PE classifier decides which loader a payload is sent to. Getting it wrong
// sends a native executable down the assembly path (or the reverse) and the
// failure only shows up on a live target, so the classification is pinned here
// rather than left to an integration test that needs a Windows host.

// buildPE assembles the smallest byte sequence that carries the headers
// classifyPE reads. Only the fields under test are meaningful; the rest is
// zero-filled, which is what a real header looks like before it is filled in.
func buildPE(t *testing.T, magic uint16, dataDirOff int, isDLL bool, comSize uint32) []byte {
	t.Helper()

	const peOff = 0x80
	buf := make([]byte, peOff+peOptionalOff+dataDirOff+16*peDirEntrySize)

	// DOS header: "MZ" and e_lfanew.
	buf[0], buf[1] = 'M', 'Z'
	putU32(buf, peDOSLfanewOff, peOff)

	// PE signature.
	putU32(buf, peOff, 0x00004550)

	// COFF header.
	coff := peOff + peSignatureSize
	putU16(buf, coff+16, uint16(dataDirOff+16*peDirEntrySize)) // SizeOfOptionalHeader
	var characteristics uint16
	if isDLL {
		characteristics |= peFileDLLFlag
	}
	putU16(buf, coff+18, characteristics)

	// Optional header magic and the CLR data directory entry.
	opt := peOff + peOptionalOff
	putU16(buf, opt, magic)
	comOff := opt + dataDirOff + peDirEntryComDescriptor*peDirEntrySize
	putU32(buf, comOff+4, comSize)

	return buf
}

func putU16(b []byte, off int, v uint16) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
}

func putU32(b []byte, off int, v uint32) {
	b[off] = byte(v)
	b[off+1] = byte(v >> 8)
	b[off+2] = byte(v >> 16)
	b[off+3] = byte(v >> 24)
}

func TestClassifyPE(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want payloadKind
	}{
		{"native exe", buildPE(t, peMagic64, peDataDirOff64, false, 0), kindNativeEXE},
		{"native dll", buildPE(t, peMagic64, peDataDirOff64, true, 0), kindNativeDLL},
		{"managed exe", buildPE(t, peMagic64, peDataDirOff64, false, 0x48), kindDotNet},
		// A managed image that is also marked as a DLL must classify as
		// managed: the assembly loader is the one that can run it, and the
		// native DLL route would fail on the target.
		{"managed dll", buildPE(t, peMagic64, peDataDirOff64, true, 0x48), kindDotNet},
		{"pe32 native exe", buildPE(t, peMagic32, peDataDirOff32, false, 0), kindNativeEXE},
		{"pe32 managed exe", buildPE(t, peMagic32, peDataDirOff32, false, 0x48), kindDotNet},
	}

	for _, tc := range cases {
		if got := classifyPE(tc.data); got != tc.want {
			t.Errorf("%s: classifyPE = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A malformed payload must classify as unknown rather than panic. This runs on
// bytes an operator uploaded, so the failure mode has to be a refusal and not a
// crash in the console's own process.
func TestClassifyPEHandlesMalformedInput(t *testing.T) {
	valid := buildPE(t, peMagic64, peDataDirOff64, false, 0)

	cases := map[string][]byte{
		"nil":      nil,
		"empty":    {},
		"one byte": {0x4D},
		"wrong dos magic": {'Z', 'M', 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"lfanew past end": func() []byte {
			b := append([]byte(nil), valid...)
			putU32(b, peDOSLfanewOff, 0xFFFFFF)
			return b
		}(),
		"bad pe signature": func() []byte {
			b := append([]byte(nil), valid...)
			putU32(b, 0x80, 0xDEADBEEF)
			return b
		}(),
		// Truncated inside the PE header itself, and truncated before the magic
		// is readable at all: both are refuse-not-panic cases.
		"truncated at the signature":          valid[:0x80+2],
		"truncated before the optional magic": valid[:0x80+peOptionalOff+1],
		"unknown optional magic": func() []byte {
			b := append([]byte(nil), valid...)
			putU16(b, 0x80+peOptionalOff, 0x1234)
			return b
		}(),
	}

	for name, data := range cases {
		if got := classifyPE(data); got != kindUnknown {
			t.Errorf("%s: classifyPE = %d, want kindUnknown", name, got)
		}
	}
}

// A size claiming more than the file holds must not be trusted into an
// out-of-range read.
func TestClassifyPEToleratesTruncatedDataDirectory(t *testing.T) {
	full := buildPE(t, peMagic64, peDataDirOff64, false, 0x48)
	// Cut the file inside the data directory the CLR entry lives in.
	truncated := full[:0x80+peOptionalOff+peDataDirOff64+4]
	if got := classifyPE(truncated); got == kindDotNet {
		t.Error("a truncated data directory was read as if it were complete")
	}
}

func TestNormalizeMimikatzMode(t *testing.T) {
	cases := map[string]string{
		"":         MimikatzModeAuto,
		"   ":      MimikatzModeAuto,
		"auto":     MimikatzModeAuto,
		"AUTO":     MimikatzModeAuto,
		"memory":   MimikatzModeMemory,
		" MEMORY ": MimikatzModeMemory,
		"upload":   MimikatzModeUpload,
		"Upload":   MimikatzModeUpload,
		// An unrecognised value falls back to auto rather than erroring: the
		// field is cosmetic, and refusing the run over it would be worse than
		// running it the way every earlier client did.
		"bogus": MimikatzModeAuto,
		"disk":  MimikatzModeAuto,
	}
	for in, want := range cases {
		if got := normalizeMimikatzMode(in); got != want {
			t.Errorf("normalizeMimikatzMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMimikatzModesAreTheAcceptedSet(t *testing.T) {
	got := MimikatzModes()
	want := []string{MimikatzModeAuto, MimikatzModeMemory, MimikatzModeUpload}
	if len(got) != len(want) {
		t.Fatalf("MimikatzModes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MimikatzModes() = %v, want %v", got, want)
		}
		if normalizeMimikatzMode(got[i]) != got[i] {
			t.Errorf("mode %q is offered but normalises to something else", got[i])
		}
	}
}

func TestInjectionHostFallsBackToTheDefault(t *testing.T) {
	if got := injectionHost(""); got != defaultInjectionHost {
		t.Errorf("injectionHost(\"\") = %q, want the default", got)
	}
	if got := injectionHost("   "); got != defaultInjectionHost {
		t.Errorf("injectionHost(blank) = %q, want the default", got)
	}
	const custom = `C:\Windows\System32\rundll32.exe`
	if got := injectionHost(custom); got != custom {
		t.Errorf("injectionHost(custom) = %q, want %q", got, custom)
	}
	if got := injectionHost("  " + custom + "  "); got != custom {
		t.Errorf("injectionHost(custom with padding) = %q, want %q", got, custom)
	}
}

// The refusal is the whole point of an explicit memory request, so the message
// has to say what to do next. "In-memory execution failed" would leave the
// operator with nothing but guessing.
func TestMemoryPayloadErrorNamesAWayForward(t *testing.T) {
	for _, custom := range []bool{false, true} {
		err := memoryPayloadError(kindNativeEXE, custom)
		if err == nil {
			t.Fatal("expected an error for a native executable")
		}
		msg := err.Error()
		for _, want := range []string{"donut", "DLL", "上传执行"} {
			if !strings.Contains(msg, want) {
				t.Errorf("custom=%v: message %q does not mention %q", custom, msg, want)
			}
		}
	}

	// The origin is named so the operator knows which payload was refused.
	if !strings.Contains(memoryPayloadError(kindNativeEXE, false).Error(), "embedded") {
		t.Error("the built-in payload was not named as such")
	}
	if !strings.Contains(memoryPayloadError(kindNativeEXE, true).Error(), "supplied") {
		t.Error("an operator-supplied payload was not named as such")
	}

	if err := memoryPayloadError(kindUnknown, false); err == nil {
		t.Error("expected an error for an unclassifiable payload")
	}
}
