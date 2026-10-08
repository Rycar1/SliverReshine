package sliver

import (
	"errors"
	"fmt"
	"github.com/bishopfox/sliver/protobuf/clientpb"
	"strings"
	"testing"
)

// A console-only deployment has no C cross-compiler. Sliver runs one for
// shared libraries and for linux shellcode, and a Darwin target needs
// osxcross, so those requests fail with the compiler’s own terse message:
// cgo’s `C compiler "gcc" not found`, or a bare `exit status 1` from the
// zig/osxcross wrapper. The operator was told none of that, and had to read
// the source to learn that the fix was `apt-get install gcc`.

func TestBuildToolchainHintNamesGcc(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "linux", GOARCH: "amd64", Format: clientpb.OutputFormat_SHARED_LIB}
	cases := []string{
		`rpc error: code = Unknown desc = exit status 1`,
		`cgo: C compiler "gcc" not found: exec: "gcc": executable file not found in $PATH`,
		`exec: "gcc": executable file not found in $PATH`,
	}
	for _, msg := range cases {
		hint := buildToolchainHint(cfg, errors.New(msg))
		if !strings.Contains(hint, "gcc") {
			t.Errorf("error %q produced no gcc hint: %q", msg, hint)
		}
	}
}

// linux shellcode is the other format that shells out to a C compiler; the
// shared-library case above must not be the only one that names gcc.
func TestBuildToolchainHintNamesGccForLinuxShellcode(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "linux", GOARCH: "amd64", Format: clientpb.OutputFormat_SHELLCODE}
	hint := buildToolchainHint(cfg, errors.New("rpc error: code = Unknown desc = exit status 1"))
	if !strings.Contains(hint, "gcc") {
		t.Fatalf("linux shellcode failure did not mention gcc: %q", hint)
	}
	if strings.Contains(hint, "osxcross") {
		t.Errorf("a linux hint suggested osxcross, which is the wrong fix: %q", hint)
	}
}

func TestBuildToolchainHintNamesOsxcrossForDarwin(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "darwin", GOARCH: "arm64", Format: clientpb.OutputFormat_SHELLCODE}
	hint := buildToolchainHint(cfg, errors.New("rpc error: code = Unknown desc = exit status 1"))
	if !strings.Contains(hint, "osxcross") {
		t.Fatalf("darwin shellcode failure did not mention osxcross: %q", hint)
	}
	if strings.Contains(hint, "apt-get install -y gcc") {
		t.Errorf("darwin hint suggested installing gcc, which is the wrong fix: %q", hint)
	}
}

// A plain executable never invokes a C compiler, so a failure there must not
// be rewritten as a toolchain problem.
func TestBuildToolchainHintLeavesExecutableAlone(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "linux", GOARCH: "amd64", Format: clientpb.OutputFormat_EXECUTABLE}
	if hint := buildToolchainHint(cfg, errors.New("exit status 1")); hint != "" {
		t.Errorf("an executable build was blamed on the toolchain: %q", hint)
	}
}

// The hint is added, never substituted: whatever the server said stays in the
// message, so a genuine compile error is still readable underneath it.
func TestExplainBuildFailureKeepsTheOriginalDiagnostic(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "linux", GOARCH: "amd64", Format: clientpb.OutputFormat_SHARED_LIB}
	msg := "exit status 1: undefined reference to `main’"
	// "exit status 1" is the documented zig/osxcross signal, so it does match;
	// the contract is only that the original error stays attached, not that it
	// is discarded.
	var err error = errors.New(msg)
	wrapped := explainBuildFailure(cfg, err)
	if !errors.Is(wrapped, err) {
		t.Fatalf("the original error was dropped: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "undefined reference") {
		t.Errorf("the compiler diagnostic was lost: %v", wrapped)
	}
}

func TestExplainBuildFailurePassesThroughUnrelatedErrors(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "windows", GOARCH: "amd64", Format: clientpb.OutputFormat_EXECUTABLE}
	orig := errors.New("UNIQUE constraint failed: implant_builds.name")
	got := explainBuildFailure(cfg, orig)
	if got != orig {
		t.Errorf("an unrelated error was rewritten: %v", got)
	}
}

func TestExplainBuildFailureHandlesNil(t *testing.T) {
	if got := explainBuildFailure(nil, nil); got != nil {
		t.Errorf("nil error produced %v", got)
	}
	if hint := buildToolchainHint(nil, errors.New("exit status 1")); hint != "" {
		t.Errorf("a nil config produced a hint: %q", hint)
	}
}

// A toolchain failure is a fault on the console's own host, not a missing
// record. It used to be answered with a 404 because the hint says the compiler
// is "not installed" and the API's not-found marker list claims that phrase;
// the type lets the status be chosen by what happened rather than by the words.
func TestIsMissingToolchainRecognisesTheWrappedError(t *testing.T) {
	cfg := &clientpb.ImplantConfig{GOOS: "linux", GOARCH: "amd64", Format: clientpb.OutputFormat_SHARED_LIB}
	err := explainBuildFailure(cfg, errors.New("exit status 1"))
	if !IsMissingToolchain(err) {
		t.Fatalf("a toolchain failure was not classified as one: %v", err)
	}
	if !IsMissingToolchain(fmt.Errorf("generate stage: %w", err)) {
		t.Errorf("classification was lost when the error was wrapped")
	}
	if IsMissingToolchain(errors.New("exit status 1")) {
		t.Errorf("a bare error was classified as a toolchain failure")
	}
	if IsMissingToolchain(nil) {
		t.Errorf("nil was classified as a toolchain failure")
	}
}

func TestMissingToolchainErrorKeepsHintAndCause(t *testing.T) {
	cause := errors.New(`exec: "gcc": executable file not found in $PATH`)
	err := NewMissingToolchainError("install gcc on this host", cause)
	if !errors.Is(err, cause) {
		t.Errorf("the original error is no longer reachable: %v", err)
	}
	if !strings.Contains(err.Error(), "install gcc on this host") || !strings.Contains(err.Error(), "executable file not found") {
		t.Errorf("hint or cause missing from the message: %v", err)
	}
}
