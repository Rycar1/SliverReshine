package sliver

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// listenerSitesFile is the file the listener-to-website map is persisted to,
// inside the console's profile directory.
//
// The map is the only record of which website an HTTP listener started here
// serves: Sliver's Job carries a name, a port and a description, but no field
// for the website it was bound to. Without it, a console restart forgets the
// association, the one-liner refuses to publish ("the website it serves is
// unknown"), and the only way to recover is to stop and restart the listener.
// Persisting it makes the association outlive the process.
const listenerSitesFile = "listener-sites.json"

// loadListenerSitesLocked reads the persisted map once per client.
//
// The caller holds lsMu. A missing file is the normal first-run case; a
// malformed one is logged and ignored rather than returned, because a bad
// cache must not stop the console from starting a listener. Anything already
// in memory wins, so a value recorded before the load is never overwritten by
// a stale one on disk.
func (c *Client) loadListenerSitesLocked() {
	if c.lsLoaded {
		return
	}
	c.lsLoaded = true

	path := listenerSitesPath()
	if path == "" {
		log.Printf("[listeners] no writable config directory found; listener websites will not survive a restart")
		return
	}
	c.lsPath = path

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[listeners] could not read %s: %v", path, err)
		}
		return
	}
	var stored map[uint32]string
	if err := json.Unmarshal(data, &stored); err != nil {
		log.Printf("[listeners] ignoring malformed %s: %v", path, err)
		return
	}
	if len(stored) == 0 {
		return
	}
	if c.lsMap == nil {
		c.lsMap = stored
		return
	}
	for id, site := range stored {
		if _, ok := c.lsMap[id]; !ok {
			c.lsMap[id] = site
		}
	}
}

// persistListenerSitesLocked writes the map atomically so an interrupted write
// cannot leave a truncated file that reads back as "no listeners". The caller
// holds lsMu. Failures are logged and dropped: the map is a cache, and a
// console that cannot write it still works for the life of the process.
func (c *Client) persistListenerSitesLocked() {
	if c.lsPath == "" {
		return
	}
	data, err := json.Marshal(c.lsMap)
	if err != nil {
		log.Printf("[listeners] could not encode listener websites: %v", err)
		return
	}

	dir := filepath.Dir(c.lsPath)
	tmp, err := os.CreateTemp(dir, ".listener-sites-*")
	if err != nil {
		log.Printf("[listeners] could not persist listener websites to %s: %v", c.lsPath, err)
		return
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		log.Printf("[listeners] could not secure %s: %v", tmpName, err)
		return
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		log.Printf("[listeners] could not write %s: %v", tmpName, err)
		return
	}
	if err := tmp.Close(); err != nil {
		log.Printf("[listeners] could not flush %s: %v", tmpName, err)
		return
	}
	if err := os.Rename(tmpName, c.lsPath); err != nil {
		log.Printf("[listeners] could not replace %s: %v", c.lsPath, err)
	}
}

// listenerSitesPath picks the profile directory the map is kept in.
//
// Directories that already exist come first, so the map lands beside the
// profiles the console is actually using instead of creating a new directory
// elsewhere. The first one this process can write to wins; when none can be
// written the map stays in memory and the caller logs that.
func listenerSitesPath() string {
	dirs := ConfigPaths()
	ordered := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			ordered = append(ordered, dir)
		}
	}
	for _, dir := range dirs {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			ordered = append(ordered, dir)
		}
	}
	for _, dir := range ordered {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			continue
		}
		probe, err := os.CreateTemp(dir, ".listener-sites-probe-*")
		if err != nil {
			continue
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return filepath.Join(dir, listenerSitesFile)
	}
	return ""
}
