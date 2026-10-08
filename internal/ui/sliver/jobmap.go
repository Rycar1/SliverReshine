package sliver

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
)

// persistedJobMap is a uint32->string map that outlives the process.
//
// Sliver's Job is the only thing the console can read back about a running
// listener, and it carries a name, a port, a description and a list of domains
// -- no website, and no bind address. The website is needed to publish a stage
// where the listener can see it, and the bind address is needed to tell the
// implant where to call back. Neither can be recovered from Sliver afterwards,
// so both are recorded here when the listener is started, keyed by job id, in a
// file beside the console's profiles.
//
// Only listeners started by this console have an entry. One started elsewhere
// (a previous run, the server CLI) has none, and the callers say so rather than
// guessing a value that would silently not match.
//
// A missing file is the normal first-run case and a malformed one is a bad
// cache; both are logged and ignored rather than returned, because a console
// that cannot read its cache must still be able to start a listener. Values
// already in memory win over what is on disk, so something recorded before the
// load is never replaced by a stale copy.
type persistedJobMap struct {
	mu     sync.Mutex
	values map[uint32]string
	loaded bool
	path   string
	file   string
	what   string
}

func newPersistedJobMap(file, what string) *persistedJobMap {
	return &persistedJobMap{file: file, what: what}
}

// get returns the value recorded for id and whether it is known.
func (m *persistedJobMap) get(id uint32) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	v, ok := m.values[id]
	return v, ok
}

// remember records value for id and writes the map back to disk.
func (m *persistedJobMap) remember(id uint32, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.loadLocked()
	if m.values == nil {
		m.values = map[uint32]string{}
	}
	m.values[id] = value
	m.persistLocked()
}

// loadLocked reads the file once per process. The caller holds m.mu.
func (m *persistedJobMap) loadLocked() {
	if m.loaded {
		return
	}
	m.loaded = true

	path := jobMapPath(m.file)
	if path == "" {
		log.Printf("[listeners] no writable config directory found; %s will not survive a restart", m.what)
		return
	}
	m.path = path

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
	if m.values == nil {
		m.values = stored
		return
	}
	for id, value := range stored {
		if _, ok := m.values[id]; !ok {
			m.values[id] = value
		}
	}
}

// persistLocked writes the map atomically so an interrupted write cannot leave
// a truncated file that reads back as "no listeners". The caller holds m.mu.
// Failures are logged and dropped: the map is a cache, and a console that cannot
// write it still works for the life of the process.
func (m *persistedJobMap) persistLocked() {
	if m.path == "" {
		return
	}
	data, err := json.Marshal(m.values)
	if err != nil {
		log.Printf("[listeners] could not encode %s: %v", m.what, err)
		return
	}

	dir := filepath.Dir(m.path)
	tmp, err := os.CreateTemp(dir, ".listener-*")
	if err != nil {
		log.Printf("[listeners] could not persist %s to %s: %v", m.what, m.path, err)
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
	if err := os.Rename(tmpName, m.path); err != nil {
		log.Printf("[listeners] could not replace %s: %v", m.path, err)
	}
}

// jobMapPath picks the profile directory a persisted job map is kept in.
//
// Directories that already exist come first, so a map lands beside the profiles
// the console is actually using instead of creating a new directory elsewhere.
// The first directory this process can write to wins; when none can be written
// the map stays in memory and the caller logs that.
func jobMapPath(file string) string {
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
		probe, err := os.CreateTemp(dir, ".listener-probe-*")
		if err != nil {
			continue
		}
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		return filepath.Join(dir, file)
	}
	return ""
}
