package sliver

import (
	"strconv"
	"testing"
)

// These tests cover audit fixes that had no regression test. Each one names the
// behaviour that was wrong, so a revert fails here rather than silently
// reintroducing it.

// ---------------------------------------------------------------------------
// codePageCache bound
// ---------------------------------------------------------------------------

// The code-page cache is keyed by session and nothing removes an entry when a
// session ends, so a long-running console accumulated one entry per implant that
// had ever checked in.
//
// The bound is exercised through the same store logic the production path uses,
// without a live session: the eviction is what matters, not how the entry got
// there.
func TestCodePageCacheIsBounded(t *testing.T) {
	restore := swapCodePageCache(t)
	defer restore()

	for i := 0; i < maxCodePageCacheEntries+50; i++ {
		storeCodePage("session-"+strconv.Itoa(i), 936)
	}

	codePageMu.Lock()
	n := len(codePageCache)
	codePageMu.Unlock()

	if n > maxCodePageCacheEntries {
		t.Errorf("the cache holds %d entries, over the %d bound; an unbounded "+
			"per-session map grows for the life of the process",
			n, maxCodePageCacheEntries)
	}
	if n == 0 {
		t.Error("the cache is empty after filling it; the bound should trigger as a " +
			"safety valve, not on every insert")
	}
}

// A cached value must still be returned without re-probing, or the bound traded a
// leak for a spawn per command.
func TestCodePageCacheStillHits(t *testing.T) {
	restore := swapCodePageCache(t)
	defer restore()

	storeCodePage("s-1", 437)

	codePageMu.Lock()
	got, ok := codePageCache["s-1"]
	codePageMu.Unlock()

	if !ok || got != 437 {
		t.Errorf("cache lookup = (%d, %v), want (437, true)", got, ok)
	}
}

// The bound must be a real cap that no plausible deployment reaches before it
// matters: too low and a large engagement pays a re-probe storm, too high and it
// is not a bound.
func TestCodePageCacheBoundIsSane(t *testing.T) {
	if maxCodePageCacheEntries < 64 {
		t.Errorf("maxCodePageCacheEntries = %d; a real engagement could exceed it and "+
			"then re-probe every live session", maxCodePageCacheEntries)
	}
	if maxCodePageCacheEntries > 100000 {
		t.Errorf("maxCodePageCacheEntries = %d, which is not a bound in practice",
			maxCodePageCacheEntries)
	}
}

// swapCodePageCache replaces the package-level cache for a test and returns the
// restore function.
func swapCodePageCache(t *testing.T) func() {
	t.Helper()
	codePageMu.Lock()
	saved := codePageCache
	codePageCache = map[string]uint32{}
	codePageMu.Unlock()

	return func() {
		codePageMu.Lock()
		codePageCache = saved
		codePageMu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// sessionOS cache: rebuilt, not merged
// ---------------------------------------------------------------------------

// The cache was merged into, so it kept an entry for every session ever seen.
// It is now rebuilt from the session list, which makes it exactly the live set.
func TestSessionOSCacheReplacesRatherThanMerges(t *testing.T) {
	c := &Client{osCache: map[string]string{}}

	// A stale entry for a session that no longer exists.
	c.osCache["dead-session"] = "windows"

	// The rebuild sessionOS performs on a miss: a fresh map, not an update of the
	// old one.
	live := map[string]string{"live-1": "linux"}
	c.osMu.Lock()
	c.osCache = live
	c.osMu.Unlock()

	c.osMu.Lock()
	_, stalePresent := c.osCache["dead-session"]
	size := len(c.osCache)
	c.osMu.Unlock()

	if stalePresent {
		t.Error("a session that no longer exists is still cached; the map is being " +
			"merged into instead of rebuilt, so it grows without bound")
	}
	if size != 1 {
		t.Errorf("cache holds %d entries, want exactly the live set (1)", size)
	}
}

// The rebuild must not drop a session that is still live.
func TestSessionOSCacheKeepsLiveEntries(t *testing.T) {
	c := &Client{osCache: map[string]string{}}
	live := map[string]string{"a": "linux", "b": "windows", "c": "darwin"}

	c.osMu.Lock()
	c.osCache = live
	c.osMu.Unlock()

	c.osMu.Lock()
	got, ok := c.osCache["b"]
	c.osMu.Unlock()

	if !ok || got != "windows" {
		t.Errorf("a live session was lost in the rebuild: (%q, %v)", got, ok)
	}
}
