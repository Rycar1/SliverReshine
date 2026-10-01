package sliver

import (
	"strings"
	"testing"
)

// The build request used to be validated on the way out rather than on the way
// in, which produced two different flavours of wrong:
//
//	os "plan9"    -> Sliver compiled for whatever GOOS it defaulted to, and the
//	                 payload came back as a working ELF
//	format "rom"  -> the switch had no default, so the format became EXECUTABLE
//
// Both answered a request for something with a payload for something else. That
// is worse than an error: the operator ships the file believing it is what they
// chose.
//
// These tests pin the rejection, and the message, because "unsupported format"
// on its own sends the reader to the source to find out what is supported.

// The matrix has to be understood as OS/arch/format together, not as three
// independent lists: freebsd offers exe and shared but no shellcode at all, so
// iterating the format list against it asserts something the console does not
// claim.
func TestValidateBuildRequestAcceptsTheOfferedMatrix(t *testing.T) {
	for os, arches := range buildTargets {
		for _, arch := range arches {
			for format := range outputFormats {
				if format == "shellcode" {
					allowed := false
					for _, a := range shellcodeArches[os] {
						if a == arch {
							allowed = true
							break
						}
					}
					if !allowed {
						continue
					}
				}
				if problem := validateBuildRequest(os, arch, format); problem != "" {
					t.Errorf("%s/%s %s was refused: %s", os, arch, format, problem)
				}
			}
		}
	}
}

func TestValidateBuildRequestRejectsUnknownValues(t *testing.T) {
	cases := []struct {
		name             string
		os, arch, format string
		wantInMessage    string
	}{
		{"unknown os", "plan9", "amd64", "exe", "unsupported os"},
		{"unknown arch", "windows", "mips", "exe", "unsupported arch"},
		{"unknown format", "windows", "amd64", "rom", "unsupported format"},
		{"arch not on this os", "darwin", "386", "exe", "unsupported arch"},
		{"arch not on this os 2", "windows", "arm", "exe", "unsupported arch"},
	}
	for _, tc := range cases {
		problem := validateBuildRequest(tc.os, tc.arch, tc.format)
		if problem == "" {
			t.Errorf("%s: %s/%s %s was accepted", tc.name, tc.os, tc.arch, tc.format)
			continue
		}
		if tc.wantInMessage != "" && !strings.Contains(problem, tc.wantInMessage) {
			t.Errorf("%s: message %q does not contain %q", tc.name, problem, tc.wantInMessage)
		}
	}
}

// An empty field means "use the default", which is a different thing from a
// typo and must not be refused.
func TestValidateBuildRequestAllowsEmptyFields(t *testing.T) {
	if problem := validateBuildRequest("", "", ""); problem != "" {
		t.Errorf("an all-empty request was refused: %s", problem)
	}
	if problem := validateBuildRequest("windows", "", ""); problem != "" {
		t.Errorf("an os-only request was refused: %s", problem)
	}
	if problem := validateBuildRequest("", "", "exe"); problem != "" {
		t.Errorf("a format-only request was refused: %s", problem)
	}
}

// The message has to name the alternatives, or the operator is left guessing
// at what the console accepts.
func TestValidateBuildRequestListsWhatIsSupported(t *testing.T) {
	problem := validateBuildRequest("plan9", "amd64", "exe")
	for _, want := range []string{"windows", "linux", "darwin", "freebsd"} {
		if !strings.Contains(problem, want) {
			t.Errorf("the message does not list %q: %s", want, problem)
		}
	}

	problem = validateBuildRequest("windows", "mips", "exe")
	if !strings.Contains(problem, "amd64") {
		t.Errorf("the arch message does not list amd64: %s", problem)
	}

	problem = validateBuildRequest("windows", "amd64", "rom")
	for _, want := range []string{"exe", "service", "shellcode", "shared"} {
		if !strings.Contains(problem, want) {
			t.Errorf("the format message does not list %q: %s", want, problem)
		}
	}
}

// Case is not a mistake worth refusing: the console lowercases before sending,
// and a hand-written request should not have to.
func TestValidateBuildRequestIsCaseInsensitive(t *testing.T) {
	for _, tc := range []struct{ os, arch, format string }{
		{"WINDOWS", "AMD64", "EXE"},
		{"Windows", "Amd64", "Exe"},
	} {
		if problem := validateBuildRequest(tc.os, tc.arch, tc.format); problem != "" {
			t.Errorf("%s/%s %s was refused: %s", tc.os, tc.arch, tc.format, problem)
		}
	}
}

// The format map and the validation have to agree, or the fallback in
// buildImplantConfig could be reached with a name that was supposed to pass.
func TestOutputFormatsRoundTrip(t *testing.T) {
	for name, want := range outputFormats {
		got, ok := outputFormats[strings.ToLower(name)]
		if !ok || got != want {
			t.Errorf("format %q did not round-trip", name)
		}
	}
	if _, ok := outputFormats["exe"]; !ok {
		t.Error("exe is not an accepted format; the default build would be refused")
	}
}

// Shellcode is offered for an OS but only builds on some of its architectures,
// and the three rules differ per platform. Sliver enforces them inside its own
// builders, so a request the console accepted used to fail after a wait with
// "windows shellcode format is only supported for amd64 and 386 architectures".
func TestValidateBuildRequestRejectsUnsupportedShellcodeArches(t *testing.T) {
	cases := []struct {
		os, arch string
		// wantMentionsFormat is false when the pair is already impossible for a
		// reason other than shellcode -- windows/arm is not a build target at
		// all, so the arch check catches it first and its message is the
		// relevant one. Asserting "shellcode" there would be asserting the
		// wrong layer answered.
		wantMentionsFormat bool
	}{
		{"windows", "arm64", true},
		{"windows", "arm", false},
		{"linux", "386", true},
		{"linux", "arm", true},
		{"darwin", "amd64", true},
		{"freebsd", "amd64", true},
		{"freebsd", "386", true},
	}
	for _, tc := range cases {
		problem := validateBuildRequest(tc.os, tc.arch, "shellcode")
		if problem == "" {
			t.Errorf("%s/%s shellcode was accepted; Sliver would refuse it after a build attempt",
				tc.os, tc.arch)
			continue
		}
		if tc.wantMentionsFormat && !strings.Contains(problem, "shellcode") {
			t.Errorf("%s/%s: message does not name the format: %s", tc.os, tc.arch, problem)
		}
	}
}

// The combinations Sliver does support must still be accepted, or the fix is
// "refuse everything".
func TestValidateBuildRequestAcceptsSupportedShellcodeArches(t *testing.T) {
	for os, arches := range shellcodeArches {
		for _, arch := range arches {
			if problem := validateBuildRequest(os, arch, "shellcode"); problem != "" {
				t.Errorf("%s/%s shellcode was refused: %s", os, arch, problem)
			}
		}
	}
}

// Narrowing shellcode must not touch the other formats: exe builds on every
// architecture the OS offers.
func TestValidateBuildRequestLeavesOtherFormatsAlone(t *testing.T) {
	for os, arches := range buildTargets {
		for _, arch := range arches {
			for _, format := range []string{"exe", "shared"} {
				if _, ok := outputFormats[format]; !ok {
					continue
				}
				if problem := validateBuildRequest(os, arch, format); problem != "" {
					t.Errorf("%s/%s %s was refused: %s", os, arch, format, problem)
				}
			}
		}
	}
}
