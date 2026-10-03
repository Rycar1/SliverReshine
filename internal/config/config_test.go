package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEnsureWritesADefaultOnFirstRun(t *testing.T) {
	home := t.TempDir()

	cfg, created, err := Ensure(home)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !created {
		t.Error("created = false on an empty directory")
	}
	if cfg != Default() {
		t.Errorf("cfg = %+v, want the defaults", cfg)
	}

	// The file has to actually exist: the whole point is that the operator can
	// find and edit it.
	if _, err := os.Stat(Path(home)); err != nil {
		t.Fatalf("settings file was not written: %v", err)
	}
}

// The single most important property: a restart must not discard an edit.
func TestEnsureNeverOverwritesAnExistingFile(t *testing.T) {
	home := t.TempDir()

	edited := Default()
	edited.Addr = "127.0.0.1:9443"
	edited.Auth.Enabled = false
	edited.Auth.User = "alice"
	if err := Save(home, edited); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, created, err := Ensure(home)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if created {
		t.Error("created = true for a directory that already had a file")
	}
	if got != edited {
		t.Errorf("cfg = %+v, want the stored values %+v", got, edited)
	}
}

// A config that cannot be parsed must fail loudly. Falling back to defaults
// would start the console on a different port, or with auth off, and the
// operator would have no idea why.
func TestEnsureRejectsAMalformedFile(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(Path(home), []byte("{ this is not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Ensure(home); err == nil {
		t.Fatal("a malformed settings file was accepted")
	}
}

// A typo in a settings file that controls the listen address and the login is
// worth failing on rather than silently ignoring.
func TestEnsureRejectsAnUnknownKey(t *testing.T) {
	home := t.TempDir()
	body := `{"addr": "0.0.0.0:8080", "authentication": {"enabled": true}}`
	if err := os.WriteFile(Path(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := Ensure(home)
	if err == nil {
		t.Fatal("an unknown key was accepted")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Errorf("error %q does not name the offending key", err)
	}
}

// A partial file is legitimate: the operator deleted the keys they did not care
// about. Everything absent falls back to the default.
func TestPartialFileFallsBackToDefaults(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(Path(home), []byte(`{"addr": "10.0.0.1:9000"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "10.0.0.1:9000" {
		t.Errorf("Addr = %q, want the stored value", cfg.Addr)
	}
	if cfg.Operator != Default().Operator {
		t.Errorf("Operator = %q, want the default to survive", cfg.Operator)
	}
	if !cfg.Auth.Enabled {
		t.Error("Auth.Enabled was cleared by a file that did not mention it")
	}
}

func TestSaveIsAtomicAndLeavesNoTempFile(t *testing.T) {
	home := t.TempDir()
	if err := Save(home, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file %q was left behind", e.Name())
		}
	}
}

func TestSaveUsesRestrictivePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not apply")
	}
	home := t.TempDir()
	if err := Save(home, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestNormalizeRepairsUnusableValues(t *testing.T) {
	cases := []struct {
		name string
		in   Config
		want Config
	}{
		{
			"empty address",
			Config{Addr: "", Operator: "op", MultiplayerHost: "h", MultiplayerPort: 1},
			Config{Addr: Default().Addr, Operator: "op", MultiplayerHost: "h", MultiplayerPort: 1},
		},
		{
			"port out of range",
			Config{Addr: "a", Operator: "op", MultiplayerHost: "h", MultiplayerPort: 99999},
			Config{Addr: "a", Operator: "op", MultiplayerHost: "h", MultiplayerPort: Default().MultiplayerPort},
		},
		{
			"negative port",
			Config{Addr: "a", Operator: "op", MultiplayerHost: "h", MultiplayerPort: -1},
			Config{Addr: "a", Operator: "op", MultiplayerHost: "h", MultiplayerPort: Default().MultiplayerPort},
		},
	}
	for _, tc := range cases {
		got := tc.in.Normalize()
		if got.Addr != tc.want.Addr || got.MultiplayerPort != tc.want.MultiplayerPort {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// An enabled login with no username would start a console nobody can log into.
func TestNormalizeFillsAMissingUsernameWhenAuthIsOn(t *testing.T) {
	c := Default()
	c.Auth.User = ""
	if got := c.Normalize(); got.Auth.User == "" {
		t.Error("Normalize left an enabled login without a username")
	}

	// With auth off the username is irrelevant and stays as it is.
	off := Default()
	off.Auth.Enabled = false
	off.Auth.User = ""
	if got := off.Normalize(); got.Auth.User != "" {
		t.Errorf("Normalize invented a username for a disabled login: %q", got.Auth.User)
	}
}

func TestEnsureReadmeWritesOnceAndNeverOverwrites(t *testing.T) {
	home := t.TempDir()

	wrote, err := EnsureReadme(home)
	if err != nil {
		t.Fatalf("EnsureReadme: %v", err)
	}
	if !wrote {
		t.Error("wrote = false on an empty directory")
	}

	path := filepath.Join(home, ReadmeName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readme was not written: %v", err)
	}
	if !strings.Contains(string(raw), "sliverreshine.json") {
		t.Error("readme does not mention the settings file")
	}

	// An operator who annotated their copy keeps their notes.
	annotated := string(raw) + "\n# my note\n"
	if err := os.WriteFile(path, []byte(annotated), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err = EnsureReadme(home)
	if err != nil {
		t.Fatalf("EnsureReadme: %v", err)
	}
	if wrote {
		t.Error("wrote = true for a file that already existed")
	}
	after, _ := os.ReadFile(path)
	if string(after) != annotated {
		t.Error("the annotated readme was overwritten")
	}
}

// The default the binary writes must round-trip through the parser, or a first
// run would leave behind a file the second run rejects.
func TestDefaultRoundTrips(t *testing.T) {
	home := t.TempDir()
	if err := Save(home, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatalf("the file written by Save cannot be read back: %v", err)
	}
	if got != Default() {
		t.Errorf("round trip changed the config: %+v", got)
	}
}
