package main

import (
	"path/filepath"
	"strings"
	"testing"

	"c2tool/internal/ui/api"
)

// The credential precedence, which is the whole content of resolveAccount.
//
// The regression it guards: the stored record used to overwrite an explicit
// password, so `-auth-pass X` and C2TOOL_AUTH_PASS silently did nothing once a
// record existed. An operator who set a password and restarted was still asked
// for the old one, which looks exactly like the console changing their password.
func TestResolveAccountPrecedence(t *testing.T) {
	dir := t.TempDir()

	// A store with a record already in it.
	withRecord := api.CredentialStore{Path: filepath.Join(dir, "console-auth")}
	if err := withRecord.Save("stored-user", "stored-password"); err != nil {
		t.Fatal(err)
	}
	// A store with nothing in it (first run).
	empty := api.CredentialStore{Path: filepath.Join(dir, "missing-console-auth")}

	t.Run("explicit password beats the stored record", func(t *testing.T) {
		got := resolveAccount("settings-user", "typed-password", withRecord)
		if got.pass != "typed-password" {
			t.Errorf("pass = %q, want the explicit override", got.pass)
		}
		if got.generated {
			t.Error("generated = true, want false: a password was supplied")
		}
	})

	t.Run("stored username still wins over the settings file", func(t *testing.T) {
		got := resolveAccount("settings-user", "", withRecord)
		if got.user != "stored-user" {
			t.Errorf("user = %q, want the stored name so a rename is not reverted", got.user)
		}
	})

	t.Run("stored password is used when none is supplied", func(t *testing.T) {
		got := resolveAccount("settings-user", "", withRecord)
		if got.pass != "stored-password" {
			t.Errorf("pass = %q, want the stored password", got.pass)
		}
		if got.generated {
			t.Error("generated = true, want false")
		}
	})

	t.Run("first run generates one", func(t *testing.T) {
		got := resolveAccount("settings-user", "", empty)
		if !got.generated {
			t.Error("generated = false, want true on a first run")
		}
		if len(got.pass) != 24 {
			t.Errorf("generated password is %d characters, want 24", len(got.pass))
		}
	})

	t.Run("explicit password wins on a first run too", func(t *testing.T) {
		got := resolveAccount("settings-user", "typed", empty)
		if got.pass != "typed" || got.generated {
			t.Errorf("got pass=%q generated=%v, want the typed password and no generation", got.pass, got.generated)
		}
	})

	t.Run("an empty username falls back to operator", func(t *testing.T) {
		got := resolveAccount("", "p", empty)
		if got.user != "operator" {
			t.Errorf("user = %q, want the default", got.user)
		}
	})

	t.Run("a generated password is not the same twice", func(t *testing.T) {
		a := resolveAccount("", "", empty)
		b := resolveAccount("", "", empty)
		if a.pass == b.pass {
			t.Error("two generated passwords are identical; the source is not random")
		}
		if strings.ContainsAny(a.pass, `:"'\`) {
			t.Errorf("generated password contains a character that needs escaping: %q", a.pass)
		}
	})
}
