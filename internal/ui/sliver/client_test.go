package sliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestProfile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write profile: %v", err)
	}
}

func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
}

func TestListProfiles(t *testing.T) {
	dir := t.TempDir()
	writeTestProfile(t, dir, "alpha.json", `{}`)
	writeTestProfile(t, dir, "beta.json", `{}`)
	writeTestProfile(t, dir, "notes.txt", `not a profile`)

	isolateHome(t)
	t.Setenv("SLIVER_CLIENT_CONFIGS", dir)

	profiles := ListProfiles()
	if len(profiles) != 2 {
		t.Fatalf("expected 2 profiles, got %d: %v", len(profiles), profiles)
	}
	seen := map[string]bool{}
	for _, p := range profiles {
		seen[p] = true
	}
	if !seen["alpha"] || !seen["beta"] {
		t.Errorf("expected alpha and beta in %v", profiles)
	}
}

func TestListProfiles_DeduplicatesAcrossDirs(t *testing.T) {
	dir := t.TempDir()
	writeTestProfile(t, dir, "dup.json", `{}`)
	dir2 := t.TempDir()
	writeTestProfile(t, dir2, "dup.json", `{}`)

	isolateHome(t)
	t.Setenv("SLIVER_CLIENT_CONFIGS", dir+string(os.PathListSeparator)+dir2)

	profiles := ListProfiles()
	count := 0
	for _, p := range profiles {
		if p == "dup" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected dup to appear once, got %d in %v", count, profiles)
	}
}

func TestLoadProfile_Success(t *testing.T) {
	dir := t.TempDir()
	content := `{
		"operator": "alice",
		"lhost": "127.0.0.1",
		"lport": 31337,
		"token": "tok",
		"ca_certificate": "ca",
		"certificate": "cert",
		"private_key": "key"
	}`
	writeTestProfile(t, dir, "alice.json", content)
	isolateHome(t)
	t.Setenv("SLIVER_CLIENT_CONFIGS", dir)

	cfg, err := LoadProfile("alice")
	if err != nil {
		t.Fatalf("LoadProfile: %v", err)
	}
	if cfg.Operator != "alice" {
		t.Errorf("Operator = %q, want alice", cfg.Operator)
	}
	if cfg.LHost != "127.0.0.1" || cfg.LPort != 31337 {
		t.Errorf("LHost/LPort = %s:%d", cfg.LHost, cfg.LPort)
	}
	if cfg.CACertificate != "ca" || cfg.Certificate != "cert" || cfg.PrivateKey != "key" {
		t.Errorf("cert fields not parsed: %+v", cfg)
	}
}

func TestLoadProfile_NotFound(t *testing.T) {
	dir := t.TempDir()
	isolateHome(t)
	t.Setenv("SLIVER_CLIENT_CONFIGS", dir)

	_, err := LoadProfile("missing")
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
	if !strings.Contains(err.Error(), "missing") {
		t.Errorf("error should mention profile name, got %v", err)
	}
}

func TestLoadProfile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeTestProfile(t, dir, "broken.json", `{not json`)
	isolateHome(t)
	t.Setenv("SLIVER_CLIENT_CONFIGS", dir)

	_, err := LoadProfile("broken")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parse profile") {
		t.Errorf("error should mention parse failure, got %v", err)
	}
}

func TestConnect_EmptyCA(t *testing.T) {
	_, err := Connect(&ProfileConfig{})
	if err == nil {
		t.Fatal("expected error when CA is empty")
	}
	if !strings.Contains(err.Error(), "missing CA") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConnect_InvalidCert(t *testing.T) {
	cfg := &ProfileConfig{
		CACertificate: "not a pem",
		Certificate:   "also not pem",
		PrivateKey:    "nope",
	}
	_, err := Connect(cfg)
	if err == nil {
		t.Fatal("expected error for invalid certificates")
	}
}
