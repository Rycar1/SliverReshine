package launch

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These tests cover launch-package fixes from the audit that had no regression
// test. Each names the behaviour that was wrong.

// toolName appends the platform's executable suffix, matching the production
// check.
func toolName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

// ---------------------------------------------------------------------------
// unpack completeness
// ---------------------------------------------------------------------------

// Success used to be judged by the go/bin directory existing. setupGo unzips
// go.zip first -- which creates go/bin -- and only then src.zip and garble, and
// returns early on either of the last two; sliver's Setup discards setupGo's
// error and writes the version marker regardless. So a run that stopped after
// go.zip left go/bin present AND a current marker, the console reported
// "compiler assets ready", and the state was permanent: the next start saw a
// current marker and never retried.
func TestToolchainStaleWhenGarbleIsMissing(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	goRoot := filepath.Join(stateDir, "go")
	if err := os.MkdirAll(filepath.Join(goRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}

	// A tree that stopped after go.zip: `go` exists, garble does not.
	if err := os.WriteFile(filepath.Join(goRoot, "bin", toolName("go")), []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	// And a marker that claims the toolchain is current -- the state that made
	// the breakage permanent.
	if err := os.WriteFile(filepath.Join(stateDir, versionFile), []byte("some-commit"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Server{opts: Options{StateDir: stateDir}}
	reason := s.toolchainStaleReason()

	if reason == "" {
		t.Fatal("a toolchain with no garble was reported as current. A run that " +
			"stopped after go.zip leaves a current marker, so this would never be " +
			"repaired and every implant build would fail with a compiler error " +
			"naming neither the cause nor the fix")
	}
	if !strings.Contains(reason, "garble") {
		t.Errorf("the reason does not name the missing tool: %q", reason)
	}
}

// A missing `go` must also be reported, and must not be masked by the marker.
func TestToolchainStaleWhenGoIsMissing(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	goRoot := filepath.Join(stateDir, "go")
	if err := os.MkdirAll(filepath.Join(goRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, versionFile), []byte("some-commit"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Server{opts: Options{StateDir: stateDir}}
	reason := s.toolchainStaleReason()

	if reason == "" {
		t.Fatal("a toolchain with no `go` binary was reported as current")
	}
	if !strings.Contains(reason, "go") {
		t.Errorf("the reason does not name the missing tool: %q", reason)
	}
}

// A missing marker is stale, not current. An install whose toolchain predates the
// marker file has no marker, and treating "no marker" as fine reuses a toolchain
// of unknown vintage.
func TestToolchainStaleWhenMarkerIsAbsent(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	goRoot := filepath.Join(stateDir, "go")
	if err := os.MkdirAll(filepath.Join(goRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"go", "garble"} {
		if err := os.WriteFile(filepath.Join(goRoot, "bin", toolName(tool)), []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	s := &Server{opts: Options{StateDir: stateDir}}
	if reason := s.toolchainStaleReason(); reason == "" {
		t.Fatal("a toolchain with no version marker was reported as current")
	}
}

// An empty marker is not a valid one either.
func TestToolchainStaleWhenMarkerIsEmpty(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	goRoot := filepath.Join(stateDir, "go")
	if err := os.MkdirAll(filepath.Join(goRoot, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"go", "garble"} {
		if err := os.WriteFile(filepath.Join(goRoot, "bin", toolName(tool)), []byte("x"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stateDir, versionFile), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Server{opts: Options{StateDir: stateDir}}
	if reason := s.toolchainStaleReason(); !strings.Contains(reason, "empty") {
		t.Errorf("reason = %q, want it to mention the empty marker", reason)
	}
}

// ---------------------------------------------------------------------------
// operator profile: parsed, not merely present
// ---------------------------------------------------------------------------

// generateProfile used to report success when the file existed at all, so a
// generator that wrote a partial file and died was reported as success -- and the
// damage was permanent, because Start stats that path and skips regeneration.
// The file holds the operator's mTLS key and token. ReadProfile is the check that
// replaced the existence test, so it must reject a partial write.
func TestReadProfileRejectsPartialWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "profile.json")
	// Exactly what a generator killed mid-write leaves behind.
	if err := os.WriteFile(p, []byte(`{"operator":"stub","lhost":"127.0.0.1"`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ReadProfile(p); err == nil {
		t.Fatal("ReadProfile accepted a truncated profile. generateProfile uses it to " +
			"decide success, so this would let a half-written profile -- and therefore " +
			"a half-written mTLS key -- be reported as generated")
	}
}

// A complete profile must still parse.
func TestReadProfileAcceptsACompleteProfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "profile.json")
	body := `{"operator":"op","lhost":"127.0.0.1","lport":8443}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := ReadProfile(p)
	if err != nil {
		t.Fatalf("a valid profile was rejected: %v", err)
	}
	if got.Operator != "op" || got.LHost != "127.0.0.1" || got.LPort != 8443 {
		t.Errorf("parsed profile = %+v, want operator/lhost/lport from the file", got)
	}
}

// A missing profile is an error, which is what makes the first-run path generate
// one instead of reporting success.
func TestReadProfileRejectsMissingFile(t *testing.T) {
	if _, err := ReadProfile(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Error("ReadProfile accepted a missing file")
	}
}
