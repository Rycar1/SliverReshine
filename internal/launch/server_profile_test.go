package launch

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// normalizeOptions: the defaults the launcher derives for an unset caller
// ---------------------------------------------------------------------------

func TestNormalizeOptionsFillsOnlyTheUnsetFields(t *testing.T) {
	got := normalizeOptions(Options{})
	if got.Operator != "operator" {
		t.Errorf("Operator = %q, want %q", got.Operator, "operator")
	}
	if got.MultiplayerHost != "127.0.0.1" {
		t.Errorf("MultiplayerHost = %q, want %q", got.MultiplayerHost, "127.0.0.1")
	}
	if got.MultiplayerPort != 31337 {
		t.Errorf("MultiplayerPort = %d, want %d", got.MultiplayerPort, 31337)
	}

	// An operator who wants a public listener must not be pulled back to
	// loopback: normalizeOptions may only fill blanks.
	explicit := normalizeOptions(Options{
		Operator:        "alice",
		MultiplayerHost: "0.0.0.0",
		MultiplayerPort: 4444,
	})
	if explicit.Operator != "alice" || explicit.MultiplayerHost != "0.0.0.0" || explicit.MultiplayerPort != 4444 {
		t.Errorf("normalizeOptions rewrote explicit values: %+v", explicit)
	}
}

// ---------------------------------------------------------------------------
// exportClientConfigs: never clobber an operator-set variable
// ---------------------------------------------------------------------------

func TestExportClientConfigsLeavesAnExistingValueAlone(t *testing.T) {
	t.Setenv("SLIVER_CLIENT_CONFIGS", `C:\somewhere\else`)
	if err := exportClientConfigs(t.TempDir()); err != nil {
		t.Fatalf("exportClientConfigs: %v", err)
	}
	if got := os.Getenv("SLIVER_CLIENT_CONFIGS"); got != `C:\somewhere\else` {
		t.Errorf("SLIVER_CLIENT_CONFIGS = %q, want the pre-existing value", got)
	}
}

func TestExportClientConfigsSetsTheVariableWhenUnset(t *testing.T) {
	t.Setenv("SLIVER_CLIENT_CONFIGS", "")
	dir := t.TempDir()
	if err := exportClientConfigs(dir); err != nil {
		t.Fatalf("exportClientConfigs: %v", err)
	}
	if got := os.Getenv("SLIVER_CLIENT_CONFIGS"); got != dir {
		t.Errorf("SLIVER_CLIENT_CONFIGS = %q, want %q", got, dir)
	}
}

// ---------------------------------------------------------------------------
// checkGeneratedProfile: success is a parseable profile, not an existing file
// ---------------------------------------------------------------------------

func writeProfileForTest(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "profile.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckGeneratedProfileAcceptsAUsableProfile(t *testing.T) {
	p := writeProfileForTest(t, `{"operator":"op","lhost":"127.0.0.1","lport":8443}`)
	if err := checkGeneratedProfile(p, nil, "generated"); err != nil {
		t.Errorf("checkGeneratedProfile rejected a complete profile: %v", err)
	}
}

func TestCheckGeneratedProfileRejectsAMissingProfile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "absent.json")
	err := checkGeneratedProfile(p, nil, "generator: cannot find operator record")
	if err == nil {
		t.Fatal("checkGeneratedProfile accepted a missing profile")
	}
	for _, want := range []string{"not usable", "the generator reported success", "cannot find operator record"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestCheckGeneratedProfileRejectsAPartialWrite(t *testing.T) {
	// What a generator killed mid-write leaves behind. Accepting it would make
	// Start skip regeneration forever, against a profile with no usable key.
	p := writeProfileForTest(t, `{"operator":"op","lhost":"127.0.0.1"`)
	err := checkGeneratedProfile(p, errors.New("exit status 1"), "boom: write failed")
	if err == nil {
		t.Fatal("checkGeneratedProfile accepted a truncated profile")
	}
	for _, want := range []string{"not usable", "exit status 1", "boom: write failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestCheckGeneratedProfileAcceptsAUsableProfileDespiteNonZeroExit(t *testing.T) {
	p := writeProfileForTest(t, `{"operator":"op","lhost":"127.0.0.1","lport":8443}`)
	if err := checkGeneratedProfile(p, errors.New("exit status 1"), "warned"); err != nil {
		t.Errorf("a usable profile with a non-zero exit should not fail: %v", err)
	}
}

// ---------------------------------------------------------------------------
// tail: compact, whitespace-trimmed error excerpts
// ---------------------------------------------------------------------------

func TestTailTrimsAndElides(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"short input is trimmed, not elided", "  hello  ", 16, "hello"},
		{"long input keeps the last n bytes", "abcdef", 3, "...def"},
		{"empty input", "", 5, ""},
		{"trailing whitespace inside the window is trimmed", "abcdef   ", 4, "...f"},
	}
	for _, tc := range cases {
		if got := tail(tc.in, tc.n); got != tc.want {
			t.Errorf("%s: tail(%q, %d) = %q, want %q", tc.name, tc.in, tc.n, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// feedConfirmations: answers the generator's prompt and closes stdin
// ---------------------------------------------------------------------------

func TestFeedConfirmationsAnswersAndClosesStdin(t *testing.T) {
	pr, pw := io.Pipe()
	go feedConfirmations(pw)

	data, err := io.ReadAll(pr)
	if err != nil {
		t.Fatalf("read from the fed pipe: %v", err)
	}
	if got, want := string(data), strings.Repeat("y\n", 16); got != want {
		t.Errorf("feedConfirmations wrote %q, want %q", got, want)
	}
}
