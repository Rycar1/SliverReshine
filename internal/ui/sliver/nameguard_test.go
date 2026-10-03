package sliver

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The audit found three sites that joined operator input onto a directory and
// handed the result to the filesystem -- one of them to os.RemoveAll. These
// tests exercise the escape that exploited each one, so a regression shows up
// here rather than on an operator's machine.

// traversalPayloads are the encodings that reached the filesystem before the
// validator existed. They are not hypothetical: "%2F" is what a URL carries, and
// Go's ServeMux decodes it before PathValue ever sees it.
var traversalPayloads = []string{
	"../../etc",
	"..",
	".",
	"../../../Windows",
	`..\..\Windows`,
	"..%2F..%2Fetc",
	"/etc/passwd",
	`C:\Windows`,
	"foo/bar",
	`foo\bar`,
	"..",
	".hidden",
	"evil.",
	"with space",
	"semi;colon",
	"pipe|char",
	"dollar$sign",
	"back`tick",
	"new\nline",
	"nul\x00byte",
	"café",
	"日本",
	strings.Repeat("a", maxArtifactNameLen+1),
	"",
}

func TestValidateArtifactNameRejectsTraversal(t *testing.T) {
	for _, name := range traversalPayloads {
		if err := validateArtifactName(name); err == nil {
			t.Errorf("validateArtifactName(%q) accepted a name that must be refused", name)
		}
	}
}

func TestValidateArtifactNameAcceptsRealNames(t *testing.T) {
	// The values a real engagement produces: alias command names, profile names
	// from the sliver-client TUI, and the IDs the artifact itself uses.
	for _, name := range []string{
		"sliverreshine",
		"operator",
		"SharpHound",
		"rubeus",
		"seatbelt-v2",
		"my_alias",
		"scan.local",
		"a",
		"0",
		"ABC123",
	} {
		if err := validateArtifactName(name); err != nil {
			t.Errorf("validateArtifactName(%q) rejected a legitimate name: %v", name, err)
		}
	}
}

// The regression that matters most: a name that escapes must not reach
// os.RemoveAll. This test creates a real tree, attempts the delete through
// RemoveAlias, and asserts the outside directory survived.
func TestRemoveAliasCannotDeleteOutsideAliasDir(t *testing.T) {
	base := t.TempDir()

	// aliases/ is the install directory; victim/ is a sibling that a traversal
	// would reach.
	aliasDir := filepath.Join(base, "aliases")
	victimDir := filepath.Join(base, "victim")
	for _, d := range []string{aliasDir, victimDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	canary := filepath.Join(victimDir, "important.txt")
	if err := os.WriteFile(canary, []byte("must survive"), 0o600); err != nil {
		t.Fatal(err)
	}

	restore := AliasDir
	AliasDir = aliasDir
	defer func() { AliasDir = restore }()

	for _, name := range []string{"../victim", "..\\victim", "..%2Fvictim", ".", ".."} {
		if err := RemoveAlias(name); err == nil {
			t.Errorf("RemoveAlias(%q) returned nil; it should have refused", name)
		}
		if _, err := os.Stat(canary); err != nil {
			t.Fatalf("RemoveAlias(%q) deleted a file outside AliasDir: %v", name, err)
		}
	}
}

// A legitimate removal must still work, or the fix is "refuse everything".
func TestRemoveAliasStillRemovesTheNamedDirectory(t *testing.T) {
	base := t.TempDir()
	aliasDir := filepath.Join(base, "aliases")
	target := filepath.Join(aliasDir, "myalias")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "alias.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	restore := AliasDir
	AliasDir = aliasDir
	defer func() { AliasDir = restore }()

	if err := RemoveAlias("myalias"); err != nil {
		t.Fatalf("RemoveAlias rejected a legitimate name: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("the alias directory was not removed (stat err = %v)", err)
	}
}

// RemoveAlias is not the only caller of the containment guard, but it is the one
// whose failure mode is destructive, so the guard gets its own direct test.
func TestMustStayInside(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "aliases", "good")

	if err := mustStayInside(filepath.Join(base, "aliases"), inside); err != nil {
		t.Errorf("rejected a path that is inside: %v", err)
	}
	// The directory itself is inside by definition; the guard must not refuse it.
	if err := mustStayInside(base, base); err != nil {
		t.Errorf("rejected the directory itself: %v", err)
	}

	for _, outside := range []string{
		filepath.Join(base, "victim"),
		filepath.Join(base, "aliases", "..", "victim"),
		filepath.Dir(base),
	} {
		if err := mustStayInside(filepath.Join(base, "aliases"), outside); err == nil {
			t.Errorf("mustStayInside accepted %q, which is outside", outside)
		}
	}
}

// M2: the profile name arrives from a URL path segment. These are the values a
// crafted request would put there.
func TestLoadProfileRejectsTraversal(t *testing.T) {
	// Paths that exist and are readable, so a missing validator would find them
	// rather than failing for an unrelated reason.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}

	victim := filepath.Join(home, ".sliver-client", "configs", "..", "..", "secret.json")
	if err := os.MkdirAll(filepath.Dir(victim), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte(`{"operator":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"../../secret", "..%2F..%2Fsecret", "../secret", "/etc/passwd"} {
		if _, err := LoadProfile(name); err == nil {
			t.Errorf("LoadProfile(%q) succeeded; it must refuse a name containing a path", name)
		} else if !strings.Contains(err.Error(), "invalid profile name") {
			t.Errorf("LoadProfile(%q) failed for the wrong reason: %v", name, err)
		}
	}
}
