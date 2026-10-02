package sliver

import (
	"strings"
	"testing"
)

// Two platforms built for one listener must not publish to the same path.
//
// Sliver's default stage path is the single name /stage.woff. Publishing a
// Windows stage and then a Linux stage therefore wrote both blobs to the same
// key, and the second replaced the first: the operator was handed two commands,
// one of which fetched the other platform's implant and failed instantly on the
// target. Nothing reported an error, because overwriting a path is an ordinary
// content update.
//
// The test asserts distinctness rather than the exact names, so the naming can
// change without silently reintroducing the collision.
func TestStagePathDiffersPerPlatform(t *testing.T) {
	windows := StagePathForPlatform(OneLinerWindows)
	linux := StagePathForPlatform(OneLinerLinux)
	darwin := StagePathForPlatform(OneLinerDarwin)

	if windows == linux {
		t.Fatalf("windows and linux share the stage path %q; one would overwrite the other", windows)
	}
	if windows == darwin || linux == darwin {
		t.Fatalf("darwin collides: windows=%q linux=%q darwin=%q", windows, linux, darwin)
	}

	// The path has to stay inside what Sliver's HTTP C2 profile serves, which is
	// why the default carries a .woff suffix at all. A path outside that is not
	// fetched by the listener, so the command would 404.
	for _, p := range []string{windows, linux, darwin} {
		if !strings.HasPrefix(p, "/") {
			t.Errorf("stage path %q is not absolute", p)
		}
		if !strings.HasSuffix(p, ".woff") {
			t.Errorf("stage path %q does not keep the .woff suffix the C2 profile expects", p)
		}
	}
}

// The platform name has to survive into the path, or the distinctness above is
// accidental. "stage-.woff" twice would pass a naive uniqueness check only if the
// implementation appended something else.
func TestStagePathNamesThePlatform(t *testing.T) {
	cases := map[OneLinerPlatform]string{
		OneLinerWindows: "windows",
		OneLinerLinux:   "linux",
		OneLinerDarwin:  "darwin",
	}
	for platform, want := range cases {
		got := StagePathForPlatform(platform)
		if !strings.Contains(got, want) {
			t.Errorf("StagePathForPlatform(%q) = %q, which does not name the platform", platform, got)
		}
	}
}

// oneLinerRequestFor is where the per-platform path is decided, and it must
// override whatever the caller supplied.
//
// The caller supplies one path for the whole request. Applying it verbatim to
// every platform recreates the collision the distinct paths exist to prevent, so
// this asserts the derived path wins even when an explicit one is present.
func TestOneLinerRequestForAlwaysDerivesThePath(t *testing.T) {
	base := OneLinerRequest{
		JobID: 7,
		// Deliberately a single shared path, which is what the frontend sends.
		Path: "/stage.woff",
	}

	windows := oneLinerRequestFor(base, OneLinerWindows)
	linux := oneLinerRequestFor(base, OneLinerLinux)

	if windows.Path == linux.Path {
		t.Fatalf("both platforms got path %q; the second build would overwrite the first", windows.Path)
	}
	if windows.Path == base.Path {
		t.Errorf("the caller's shared path %q was reused for windows", base.Path)
	}
	if windows.Path != StagePathForPlatform(OneLinerWindows) {
		t.Errorf("windows path = %q, want %q", windows.Path, StagePathForPlatform(OneLinerWindows))
	}
	if windows.Platform != OneLinerWindows || linux.Platform != OneLinerLinux {
		t.Errorf("platform not carried through: %q / %q", windows.Platform, linux.Platform)
	}
	// The rest of the request must survive, or the build loses the listener it
	// was made for.
	if windows.JobID != base.JobID {
		t.Errorf("job id = %d, want %d", windows.JobID, base.JobID)
	}
}
