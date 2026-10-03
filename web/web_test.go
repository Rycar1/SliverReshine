package web

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// distSub returns the view the API server serves from, and whether a frontend
// build is present at all.
//
// web/dist holds only .gitkeep in a fresh clone: the bundle is produced by the
// frontend build step and is deliberately not committed, so an unbuilt tree is a
// normal state rather than a failure. The tests below therefore assert the
// contract when a build is present and skip when it is not. The decision is made
// once, on "does index.html exist", so a build that exists but is broken still
// fails instead of silently skipping.
func distSub(t *testing.T) (fs.FS, bool) {
	t.Helper()
	sub, err := fs.Sub(Dist, "dist")
	if err != nil {
		t.Fatalf("fs.Sub(Dist, \"dist\"): %v", err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false
		}
		t.Fatalf("stat dist/index.html in the embedded FS: %v", err)
	}
	return sub, true
}

// The API server serves the frontend straight out of this FS with dist as the
// root. A missing or empty index.html makes the single-binary build answer every
// browser request with a 404 while the Go side looks healthy, so the contract is
// pinned here rather than discovered in a deployment.
func TestDistCarriesTheFrontendEntryPoint(t *testing.T) {
	sub, ok := distSub(t)
	if !ok {
		t.Skip("frontend not built: run the frontend build to exercise this test")
	}
	data, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("reading dist/index.html from the embedded FS: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("dist/index.html is empty")
	}
	head := strings.ToLower(string(data))
	if !strings.Contains(head, "<!doctype html") && !strings.Contains(head, "<html") {
		t.Fatalf("dist/index.html does not look like HTML: %.80q", data)
	}
}

// The embed directive is all:dist, which must also carry the hashed asset files
// the page references. If only index.html were embedded the console would render
// a blank page and nothing on the Go side would notice.
func TestDistCarriesAssets(t *testing.T) {
	sub, ok := distSub(t)
	if !ok {
		t.Skip("frontend not built: run the frontend build to exercise this test")
	}
	var assets int
	err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(p, "assets/") {
			assets++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if assets == 0 {
		t.Fatal("no files under dist/assets: the embedded bundle has no JS or CSS")
	}
}
