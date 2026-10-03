package sliver

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// safeAliasRelPath must reject every path that escapes the install directory,
// not just the ones written with forward slashes.
//
// The alias manifest comes from a bundle the operator downloads -- Sliver's
// armory serves aliases from public repositories -- so its file paths are
// untrusted input, not operator input.
func TestSafeAliasRelPathRejectsEveryEscape(t *testing.T) {
	for _, p := range []string{
		"../../evil",
		"/../../evil",
		"a/../../evil",
		// Backslash traversal: path.Clean understands only '/', so these used to
		// survive as a single component and filepath.Join then resolved them.
		`..\..\evil`,
		`..\evil`,
		`a\..\..\evil`,
		`/..\..\evil`,
		`..\..\..\..\..\Users\Public\evil`,
		`C:\Windows\System32\evil`,
		`\\server\share\evil`,
		`..`,
		`a/..`,
	} {
		rel, err := safeAliasRelPath(p)
		if err != nil {
			t.Logf("rejected %-32q (%v)", p, err)
			continue
		}
		// Accepted values are normalised rather than rejected outright: the
		// traversal is collapsed, so the file lands inside with a different name.
		// What matters is the destination, computed the way the caller builds it.
		base := filepath.Join(t.TempDir(), "aliases", "myalias")
		dst := filepath.Join(base, rel)
		inside := dst == base || strings.HasPrefix(dst, base+string(filepath.Separator))
		if !inside {
			t.Errorf("safeAliasRelPath(%q) = %q\n  joined to the install dir gives %q\n  which is OUTSIDE %q (host %s)",
				p, rel, dst, base, runtime.GOOS)
		}
		if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
			t.Errorf("safeAliasRelPath(%q) = %q is absolute or has a volume", p, rel)
		}
	}
}

// A legitimate nested path must still work, or the fix is a regression.
func TestSafeAliasRelPathAcceptsNestedPaths(t *testing.T) {
	for _, p := range []string{
		"myalias",
		"bin/myalias",
		"bin/linux/myalias",
		"./bin/myalias",
		"a/b/c/d",
	} {
		rel, err := safeAliasRelPath(p)
		if err != nil {
			t.Errorf("rejected a legitimate path %q: %v", p, err)
			continue
		}
		if filepath.IsAbs(rel) {
			t.Errorf("safeAliasRelPath(%q) returned an absolute path %q", p, rel)
		}
		if strings.Contains(rel, "..") {
			t.Errorf("safeAliasRelPath(%q) = %q contains a parent reference", p, rel)
		}
	}
}
