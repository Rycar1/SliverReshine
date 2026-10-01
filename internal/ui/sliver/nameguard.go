package sliver

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// This file holds the validator that every operator-supplied identifier has to
// pass before it reaches the filesystem.
//
// The rule it enforces is deliberately the strictest one that still accepts real
// names: letters, digits, dot, underscore and dash, nothing else. That set has no
// path separator, no parent-directory token, and no shell metacharacter in it, so
// a value that passes cannot escape the directory it is joined onto, cannot
// introduce a second path segment, and cannot be re-parsed as a command.
//
// It exists because three separate call sites were joining raw operator input
// onto a directory and handing the result to the filesystem -- one of them to
// os.RemoveAll. Fixing them one at a time would have left the fourth one to be
// written next month with the same hole; the allowlist is the single place the
// rule lives now.

// maxArtifactNameLen bounds an identifier so a name cannot be used to build a
// path longer than the filesystem will accept, which on Windows surfaces as a
// confusing "file not found" rather than "name too long".
const maxArtifactNameLen = 64

// errUnsafeName is the error every rejected identifier produces. It names the
// rule rather than the value, because the value is attacker-influenced and ends
// up in an HTTP response body.
var errUnsafeName = errors.New(
	"name may only contain letters, digits, dot, underscore and dash, and may not be a path")

// validateArtifactName reports whether name is safe to use as a single path
// component.
//
// Rejection is the default: anything that is not provably in the allowlist is
// refused, so a character this function has never heard of -- a Unicode
// homoglyph, a full-width solidus, a NUL byte -- fails closed rather than
// depending on the filesystem to interpret it the same way Go did.
func validateArtifactName(name string) error {
	if name == "" {
		return errors.New("a name is required")
	}
	if len(name) > maxArtifactNameLen {
		return fmt.Errorf("name is longer than %d characters", maxArtifactNameLen)
	}

	// "." and ".." pass the character allowlist, so they are rejected by name.
	// "." would resolve to the parent directory itself and ".." one level above
	// it; both are exactly the escape this validator exists to stop.
	if name == "." || name == ".." {
		return errUnsafeName
	}

	for _, r := range name {
		if r > unicode.MaxASCII {
			// Non-ASCII is refused rather than normalised. Two different byte
			// sequences can render as the same name, and the filesystem is the
			// one that decides which of them a path means -- so accepting them
			// would mean trusting that decision to match ours.
			return errUnsafeName
		}
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return errUnsafeName
		}
	}

	// A leading dot is refused so an identifier cannot name a hidden file, and a
	// trailing dot is refused because Windows silently strips it: "evil." and
	// "evil" would be the same file there and different files elsewhere.
	if strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".") {
		return errUnsafeName
	}

	return nil
}
