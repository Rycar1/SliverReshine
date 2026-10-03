package sliver

import "testing"

// The platform dispatchers route on the module name's platform prefix. These
// cases pin the three paths the split introduced -- Windows, POSIX, and an
// unknown module -- so a future edit cannot silently route a module to the
// wrong switch and start answering "not present" for everything.

func TestDetectPersistenceDispatch(t *testing.T) {
	// Non-zero status is a negative answer before any routing happens.
	if ok, detail := detectPersistence("win-local-account", "User name  name", "", 1, "name"); ok || detail != "" {
		t.Errorf("non-zero status: got (%v, %q), want (false, \"\")", ok, detail)
	}
	// A win- module reaches the Windows switch: the account record matches.
	if ok, _ := detectPersistence("win-local-account", "User name  name", "", 0, "name"); !ok {
		t.Error("win-local-account should report installed when the record names the account")
	}
	// A linux- module reaches the POSIX switch: a grep count of 0 is not present.
	if ok, detail := detectPersistence("linux-cron", "0", "", 0, "x"); ok || detail != "" {
		t.Errorf("linux-cron count 0: got (%v, %q), want (false, \"\")", ok, detail)
	}
	// Unknown modules are not present, and routing must not guess a platform.
	for _, m := range []string{"unknown", "win-unknown", "linux-unknown", ""} {
		if ok, detail := detectPersistence(m, "x", "", 0, "x"); ok || detail != "" {
			t.Errorf("unknown module %q: got (%v, %q), want (false, \"\")", m, ok, detail)
		}
	}
}

func TestInstallCommandDispatch(t *testing.T) {
	if _, err := installCommand("plan9", "win-run-key", "p", "n"); err == nil {
		t.Error("unknown platform should error")
	}
	if _, err := installCommand(platformWindows, "win-run-key", "p", "n"); err != nil {
		t.Errorf("windows install: %v", err)
	}
	if _, err := installCommand(platformLinux, "linux-cron", "p", "n"); err != nil {
		t.Errorf("linux install: %v", err)
	}
	if _, err := installCommand(platformWindows, "linux-cron", "p", "n"); err == nil {
		t.Error("a POSIX module under windows should error")
	}
}
