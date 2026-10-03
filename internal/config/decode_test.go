package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decode is documented as strict. DisallowUnknownFields governs only the keys
// inside the object it decodes, so before this fix a stray brace after the object,
// or a second JSON document, was accepted: the first document was applied and
// everything after it discarded with no error.
//
// That matters because this file controls the listen address and the login. An
// operator who breaks the file must be told, not silently served a config they did
// not write.
func TestDecodeRejectsTrailingContent(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"stray closing brace", `{"addr":"127.0.0.1:8080"} }`},
		{"second document", `{"addr":"127.0.0.1:8080"}{"addr":"0.0.0.0:9000"}`},
		{"trailing garbage", `{"addr":"127.0.0.1:8080"} not json`},
		{"trailing array", `{"addr":"127.0.0.1:8080"}[1,2,3]`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := decode([]byte(tc.body))
			if err == nil {
				t.Fatalf("decode accepted %q and returned addr=%q; trailing content "+
					"must be an error so a broken settings file is not silently applied",
					tc.body, cfg.Addr)
			}
		})
	}
}

// A valid file must still decode, or the strictness has broken the settings.
func TestDecodeAcceptsValidFiles(t *testing.T) {
	cases := map[string]string{
		"minimal":      `{"addr":"127.0.0.1:8080"}`,
		"trailing ws":  "{\"addr\":\"127.0.0.1:8080\"}\n\n  \t\n",
		"with auth":    `{"addr":"127.0.0.1:8080","auth":{"enabled":true,"user":"op"}}`,
		"empty object": `{}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decode([]byte(body)); err != nil {
				t.Errorf("decode rejected a valid file: %v", err)
			}
		})
	}
}

// The other half of "strict": an unknown key is a typo, and a typo in a file that
// controls the listen address is worth failing on rather than ignoring.
func TestDecodeRejectsUnknownKeys(t *testing.T) {
	for _, body := range []string{
		`{"adr":"127.0.0.1:8080"}`,
		`{"addr":"127.0.0.1:8080","nope":1}`,
	} {
		if _, err := decode([]byte(body)); err == nil {
			t.Errorf("decode accepted an unknown key: %q", body)
		}
	}
}

// A malformed file must not silently fall back to defaults, which would start the
// console with a config the operator never wrote.
func TestLoadDoesNotFallBackOnAMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)

	// Two documents: the second is discarded, so the first silently wins.
	if err := os.WriteFile(path, []byte(`{"addr":"127.0.0.1:8080"}{"addr":"0.0.0.0:9999"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted a settings file with two documents; the operator would " +
			"be served the first and never told the second was ignored")
	}
	if !strings.Contains(err.Error(), "unexpected content") &&
		!strings.Contains(err.Error(), "more than one") {
		t.Errorf("the error does not explain the problem: %v", err)
	}
}

// A well-formed file must still load.
func TestLoadAcceptsAValidFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName),
		[]byte(`{"addr":"127.0.0.1:18080"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load rejected a valid file: %v", err)
	}
	if cfg.Addr != "127.0.0.1:18080" {
		t.Errorf("addr = %q, want the value from the file", cfg.Addr)
	}
}
