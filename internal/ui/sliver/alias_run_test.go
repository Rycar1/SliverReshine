package sliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// RunAlias must refuse a name that would escape AliasDir.
//
// The name arrives as a URL path segment. Go's ServeMux hands back the
// percent-decoded value, so "%2F" is already a separator when filepath.Join sees
// it -- which is why validateArtifactName is the guard rather than a check for
// "..".
//
// The assertion is on the guard's own error text, not on "an error happened".
// With no session present RunAlias fails either way, so `err != nil` would pass
// with and without the fix -- which is what a first version of this test did, and
// reverting the fix did not make it fail. The distinguishing signal is that the
// guard rejects before the manifest is read: without it, name=".." finds the
// planted manifest and proceeds to the session lookup instead.
func TestRunAliasRejectsEscapingNames(t *testing.T) {
	old := AliasDir
	t.Cleanup(func() { AliasDir = old })

	root := t.TempDir()
	AliasDir = filepath.Join(root, "aliases")
	if err := os.MkdirAll(AliasDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// A manifest one level above AliasDir, which name=".." would reach.
	planted := filepath.Join(root, "alias.json")
	if err := os.WriteFile(planted, []byte(`{"command_name":"planted"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"..",
		"../..",
		"a/b",
		`a\b`,
		".",
		"",
		"nul\x00name",
	} {
		func() {
			// A nil client panics if execution gets past the guard, so recover
			// and report that as "the guard did not reject".
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RunAlias(%q) got past the name guard and reached the RPC "+
						"layer (panic: %v)", name, r)
				}
			}()

			c := &Client{}
			_, _, err := c.RunAlias("sess", name, "", "", "", "", "")
			if err == nil {
				t.Errorf("RunAlias accepted %q", name)
				return
			}
			if !strings.Contains(err.Error(), "invalid alias name") {
				t.Errorf("RunAlias(%q) was not rejected by the name guard: %v", name, err)
			}
		}()
	}
}

// A legitimate name must get past the guard. Without this the fix could be
// "reject everything" and the test above would still pass.
func TestRunAliasAcceptsLegitimateNames(t *testing.T) {
	old := AliasDir
	t.Cleanup(func() { AliasDir = old })
	AliasDir = filepath.Join(t.TempDir(), "aliases")
	if err := os.MkdirAll(filepath.Join(AliasDir, "good-alias"), 0o700); err != nil {
		t.Fatal(err)
	}

	defer func() { _ = recover() }() // the nil client may panic after the guard

	c := &Client{}
	_, _, err := c.RunAlias("sess", "good-alias", "", "", "", "", "")
	if err != nil && strings.Contains(err.Error(), "invalid alias name") {
		t.Errorf("a legitimate name was rejected by the guard: %v", err)
	}
}

// A manifest path built from an accepted name must stay inside AliasDir.
func TestAliasManifestPathStaysInsideForAcceptedNames(t *testing.T) {
	old := AliasDir
	t.Cleanup(func() { AliasDir = old })
	AliasDir = filepath.Join("state", "aliases")

	for _, name := range []string{"a", "a.b", "a-b_c", "A1"} {
		if err := validateArtifactName(name); err != nil {
			t.Fatalf("validateArtifactName(%q) rejected a legitimate name: %v", name, err)
		}
		p := aliasManifestPath(name)
		rel, err := filepath.Rel(AliasDir, p)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("name %q resolved outside AliasDir: %q", name, p)
		}
	}
}

// Every alias entry point that takes a name from outside the package must guard
// it. RemoveAlias already did; RunAlias did not.
func TestRemovalAlsoGuardsTheName(t *testing.T) {
	old := AliasDir
	t.Cleanup(func() { AliasDir = old })
	AliasDir = filepath.Join(t.TempDir(), "aliases")
	if err := os.MkdirAll(AliasDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := RemoveAlias(".."); err == nil {
		t.Error("RemoveAlias accepted a traversal name")
	} else if !strings.Contains(err.Error(), "alias name") {
		t.Errorf("RemoveAlias rejected for the wrong reason: %v", err)
	}
}
