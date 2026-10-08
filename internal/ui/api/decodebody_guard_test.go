package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDecodeBodyIsTheOnlyBodyDecoder keeps the P1-1 fix from regressing.
//
// Every JSON request body must go through decodeBody so that an unknown field
// is rejected instead of silently dropped and an oversized body is answered
// with 413 rather than 400. A hand-rolled json.NewDecoder(r.Body) brings back
// the silent field-drop bug the shared helper exists to prevent, so the call is
// allowed in exactly one place: the helper itself.
func TestDecodeBodyIsTheOnlyBodyDecoder(t *testing.T) {
	const helper = "handlers_common.go"

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(src), "json.NewDecoder(r.Body)") {
			found = append(found, name)
		}
	}

	if len(found) != 1 || found[0] != helper {
		t.Fatalf("json.NewDecoder(r.Body) must appear only in %s, found in %v; route request bodies through decodeBody", helper, found)
	}
}
