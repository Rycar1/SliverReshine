package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The shipped default must stay 0.0.0.0. Binding loopback by default would make
// a console that is reached from another machine silently unreachable, which is
// a worse failure than the warning it would avoid. The warning is what makes the
// exposure a decision; RequireTLS is what makes it a hard one.
func TestDefaultStillBindsTheWildcard(t *testing.T) {
	if got := Default().Addr; got != "0.0.0.0:8080" {
		t.Errorf("Default().Addr = %q, want the wildcard so cross-machine access keeps working", got)
	}
}

// The new key has to be off by default: turning it on would change the shipped
// behaviour from "warns" to "refuses to start" without the operator asking.
func TestRequireTLSIsOffByDefault(t *testing.T) {
	if Default().RequireTLS {
		t.Error("Default() has RequireTLS set; the shipped console would refuse to start unencrypted")
	}
}

// A settings file from an older release has no requireTLS key, and the strict
// decoder must not reject it -- an unknown-key failure here would brick every
// existing deployment on upgrade.
func TestExistingFileWithoutRequireTLStillLoads(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(Path(home), []byte(`{"addr": "0.0.0.0:8080"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.RequireTLS {
		t.Error("RequireTLS was inferred from a file that never mentioned it")
	}
}

// The setting has to survive a round trip, otherwise the operator sets it once
// and the next restart silently drops the protection they asked for.
func TestRequireTLSSurvivesARoundTrip(t *testing.T) {
	home := t.TempDir()

	cfg := Default()
	cfg.RequireTLS = true
	cfg.TLSCert = "/etc/c2tool/console.crt"
	cfg.TLSKey = "/etc/c2tool/console.key"
	if err := Save(home, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.RequireTLS {
		t.Error("RequireTLS did not survive Save/Load")
	}
	if got != cfg {
		t.Errorf("cfg = %+v, want %+v", got, cfg)
	}
}

// It is a real JSON field, not a struct-only one, so it can be written by hand
// into the file the README tells the operator to edit.
func TestRequireTLSIsWritableByHand(t *testing.T) {
	home := t.TempDir()
	body := `{"addr": "0.0.0.0:8080", "requireTLS": true}`
	if err := os.WriteFile(Path(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.RequireTLS {
		t.Error("a hand-written requireTLS was not picked up")
	}
}

// Normalize must not clear it: Normalize exists to fill blanks in, and dropping
// a security setting while doing so would be the opposite of its purpose.
func TestNormalizeKeepsRequireTLS(t *testing.T) {
	cfg := Config{RequireTLS: true}
	if got := cfg.Normalize(); !got.RequireTLS {
		t.Error("Normalize cleared RequireTLS")
	}
}

// The JSON name is part of the file format the README documents, so pin it.
func TestRequireTLSJSONName(t *testing.T) {
	blob, err := json.Marshal(Config{RequireTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"requireTLS":true`) {
		t.Errorf("marshalled config = %s, want a requireTLS key", blob)
	}
}

// RequireTLS is about the absence of TLS, so a half-configured TLS pair is still
// "no TLS" for policy purposes -- and main refuses that configuration separately
// anyway.
func TestRequireTLSOnlyMattersWithoutTLS(t *testing.T) {
	both := Default()
	both.RequireTLS = true
	both.TLSCert = "c"
	both.TLSKey = "k"
	if !both.TLSConfigured() {
		t.Error("TLSConfigured() = false for a complete pair")
	}

	half := Default()
	half.RequireTLS = true
	half.TLSCert = "c"
	if !half.TLSHalfConfigured() {
		t.Error("TLSHalfConfigured() = false for a cert without a key")
	}
	if half.TLSConfigured() {
		t.Error("TLSConfigured() = true for a cert without a key")
	}
}
