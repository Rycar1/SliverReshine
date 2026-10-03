package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CredentialStore is the single durable record of the console account.
//
// The format is one "user:pass" line, which is exactly what the shipped run.sh
// writes and reads. That matters more than it looks: the launcher script and
// the binary are two writers of the same fact, so if either kept its own copy
// the operator could set a password in one place and be asked for a different
// one at the next start. Sharing one file makes the login prompt, the Settings
// panel, and the restart all agree by construction.
type CredentialStore struct {
	Path string
}

// Load reads the stored account. A missing file is not an error: it means the
// console has never been configured, and the caller decides what to do.
func (c CredentialStore) Load() (user, pass string, ok bool, err error) {
	if c.Path == "" {
		return "", "", false, nil
	}
	raw, err := os.ReadFile(c.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, nil
		}
		return "", "", false, err
	}

	line := strings.TrimRight(string(raw), "\r\n")
	if line == "" {
		return "", "", false, nil
	}

	// Split on the FIRST colon only: a password is allowed to contain colons,
	// the username is not, and trimming both ends here is what lets a password
	// with trailing whitespace survive a round trip.
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false, fmt.Errorf("%s: expected \"user:pass\", found no colon", c.Path)
	}
	user, pass = line[:idx], line[idx+1:]
	if user == "" {
		return "", "", false, fmt.Errorf("%s: empty username", c.Path)
	}
	return user, pass, true, nil
}

// Save writes the account atomically so an interrupted write cannot leave a
// half-written credential file behind, which would read back as a wrong
// password rather than as a failure.
func (c CredentialStore) Save(user, pass string) error {
	if c.Path == "" {
		return errors.New("no credential file configured")
	}
	if user == "" {
		return errors.New("username must not be empty")
	}
	if strings.ContainsAny(user, ":\r\n") {
		return errors.New("username must not contain ':', CR or LF")
	}
	if strings.ContainsAny(pass, "\r\n") {
		return errors.New("password must not contain CR or LF")
	}

	// 0700 and 0600 protect this on Unix. On Windows they do almost nothing:
	// Go's os.Chmod there only toggles the read-only attribute, so the real
	// protection is whatever ACL the directory inherits. That matters because this
	// file holds the console password, and the console's default state directory
	// is alongside the executable -- so unpacking into a shared or world-writable
	// directory leaves the login readable by other local users.
	//
	// Setting an explicit DACL is deliberately not attempted here: a wrong one
	// locks the operator out of their own console, and the correct one depends on
	// the account the service runs as. The mitigation is documented instead; see
	// EnsureReadme in internal/config, which the state directory already writes.
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".console-auth-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	// CreateTemp already uses 0600; set it explicitly anyway so the intent
	// survives a future refactor that swaps in a different temp-file helper.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(user + ":" + pass + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, c.Path)
}
