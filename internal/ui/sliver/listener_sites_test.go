package sliver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// isolateListenerSites points the console at a private profile directory so the
// persisted listener map neither reads nor writes the developer's own.
func isolateListenerSites(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	isolateHome(t)
	t.Setenv("SLIVER_CLIENT_CONFIGS", dir)
	return dir
}

func TestListenerSitesSurviveReconnect(t *testing.T) {
	isolateListenerSites(t)

	first := &Client{}
	first.rememberListenerSite(42, "webdelivery")

	// A fresh client is what a console restart produces: same profile
	// directory, no in-memory state carried over.
	second := &Client{}
	site, ok := second.listenerSite(42)
	if !ok || site != "webdelivery" {
		t.Fatalf("listenerSite after restart = (%q, %v), want (webdelivery, true)", site, ok)
	}
}

func TestListenerSitesMergeAcrossClients(t *testing.T) {
	isolateListenerSites(t)

	(&Client{}).rememberListenerSite(1, "one")
	(&Client{}).rememberListenerSite(2, "two")

	third := &Client{}
	for id, want := range map[uint32]string{1: "one", 2: "two"} {
		site, ok := third.listenerSite(id)
		if !ok || site != want {
			t.Errorf("listenerSite(%d) = (%q, %v), want (%q, true)", id, site, ok, want)
		}
	}
}

func TestListenerSitesIgnoreMalformedFile(t *testing.T) {
	dir := isolateListenerSites(t)
	if err := os.WriteFile(filepath.Join(dir, listenerSitesFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed malformed cache: %v", err)
	}

	c := &Client{}
	if site, ok := c.listenerSite(7); ok {
		t.Fatalf("malformed cache reported site %q as known", site)
	}

	// A malformed cache must not stop the map from working; the next write
	// replaces it with valid JSON.
	c.rememberListenerSite(7, "site7")
	if site, ok := (&Client{}).listenerSite(7); !ok || site != "site7" {
		t.Fatalf("after rewrite listenerSite(7) = (%q, %v), want (site7, true)", site, ok)
	}
}

func TestListenerSitesMissingFileIsFine(t *testing.T) {
	dir := isolateListenerSites(t)

	c := &Client{}
	if site, ok := c.listenerSite(3); ok {
		t.Fatalf("missing cache reported site %q as known", site)
	}
	// Reading must not create the file: only a recorded site is worth writing.
	if _, err := os.Stat(filepath.Join(dir, listenerSitesFile)); !os.IsNotExist(err) {
		t.Fatalf("read created %s (err=%v)", listenerSitesFile, err)
	}
}

func TestListenerSitesViewDelegatesToRoot(t *testing.T) {
	isolateListenerSites(t)

	root := &Client{}
	view := root.WithRequestContext(context.Background())

	// A per-request view writes through to the console-wide client...
	view.rememberListenerSite(9, "vsite")
	if site, ok := root.listenerSite(9); !ok || site != "vsite" {
		t.Fatalf("root.listenerSite(9) = (%q, %v), want (vsite, true)", site, ok)
	}
	// ...and reads through to it as well, so a view never reloads its own copy.
	if site, ok := view.listenerSite(9); !ok || site != "vsite" {
		t.Fatalf("view.listenerSite(9) = (%q, %v), want (vsite, true)", site, ok)
	}
}

func TestListenerSitesFileIsValidJSONInConfigDir(t *testing.T) {
	dir := isolateListenerSites(t)

	(&Client{}).rememberListenerSite(5, "s5")

	data, err := os.ReadFile(filepath.Join(dir, listenerSitesFile))
	if err != nil {
		t.Fatalf("read persisted map: %v", err)
	}
	var got map[uint32]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("persisted map is not valid JSON: %v", err)
	}
	if got[5] != "s5" {
		t.Fatalf("persisted map = %v, want {5:s5}", got)
	}
}
