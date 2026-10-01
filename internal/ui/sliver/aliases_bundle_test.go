package sliver

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The upload body limit bounds the COMPRESSED size of an alias bundle. gzip
// reaches ratios of a thousand to one, so a small body could still expand to
// more memory than the console has -- and the console is the one process the
// operator cannot afford to lose. These tests pin the caps on the decompressed
// side, which is where the limit has to be.

// buildBundle packs files into a tar.gz exactly the way a real bundle arrives.
func buildBundle(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for name, body := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o600,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return gz.Bytes()
}

func validManifest(commandName string) string {
	m := map[string]any{
		"name":         "test",
		"command_name": commandName,
	}
	blob, _ := json.Marshal(m)
	return string(blob)
}

// A bundle with too many entries is refused before the memory is committed.
func TestAliasBundleRejectsTooManyFiles(t *testing.T) {
	files := map[string]string{"alias.json": validManifest("ok")}
	for i := 0; i < maxAliasBundleFiles+10; i++ {
		files[fmt.Sprintf("f%04d.bin", i)] = "x"
	}

	_, _, err := readAliasTarGz(buildBundle(t, files))
	if err == nil {
		t.Fatal("a bundle with too many files was accepted")
	}
	if !strings.Contains(err.Error(), "more than") {
		t.Errorf("the error does not name the limit: %v", err)
	}
}

// A single entry over the per-file cap is refused, so one enormous file cannot
// get through even when the total stays under the bundle cap.
func TestAliasBundleRejectsOversizedEntry(t *testing.T) {
	big := strings.Repeat("A", maxAliasEntryBytes+1024)
	_, _, err := readAliasTarGz(buildBundle(t, map[string]string{
		"alias.json": validManifest("ok"),
		"big.bin":    big,
	}))
	if err == nil {
		t.Fatal("an oversized entry was accepted")
	}
	if !strings.Contains(err.Error(), "larger than") {
		t.Errorf("the error does not name the limit: %v", err)
	}
}

// The decompressed total is capped independently of the entry count, which is
// the property that defeats a compression bomb: many entries, each just under
// the per-file cap.
func TestAliasBundleRejectsOversizedTotal(t *testing.T) {
	// Just under the per-entry cap, repeated until the total must exceed the
	// bundle cap. Sizes are derived from the constants so this keeps testing the
	// real limits if they are ever changed.
	entry := strings.Repeat("B", maxAliasEntryBytes/4)
	files := map[string]string{"alias.json": validManifest("ok")}
	for i := 0; i < (maxAliasBundleBytes/maxAliasEntryBytes)*4+2; i++ {
		files[fmt.Sprintf("part%02d.bin", i)] = entry
	}

	_, _, err := readAliasTarGz(buildBundle(t, files))
	if err == nil {
		t.Fatal("a bundle that expands past the total cap was accepted")
	}
}

// The caps must not reject a legitimate bundle, or the fix is "refuse
// everything". This is the shape a real armory alias has.
func TestAliasBundleAcceptsARealisticBundle(t *testing.T) {
	files := map[string]string{
		"alias.json": validManifest("sharphound"),
		// A few megabytes of .NET assembly, which is the largest real case.
		"SharpHound.exe": strings.Repeat("MZ", 2<<20),
		"SharpHound.dll": strings.Repeat("MZ", 1<<20),
		"README.md":      "collects AD data",
	}

	manifest, out, err := readAliasTarGz(buildBundle(t, files))
	if err != nil {
		t.Fatalf("a legitimate bundle was refused: %v", err)
	}
	if len(manifest) == 0 {
		t.Error("the manifest was not extracted")
	}
	if len(out) != 3 {
		t.Errorf("extracted %d files, want 3", len(out))
	}
}

// The end-to-end path: a crafted bundle must not escape AliasDir, and the
// decompression caps must not have broken the normal install.
func TestInstallAliasRejectsTraversalCommandName(t *testing.T) {
	base := t.TempDir()
	restore := AliasDir
	AliasDir = base
	defer func() { AliasDir = restore }()

	bundle := base64.StdEncoding.EncodeToString(buildBundle(t, map[string]string{
		"alias.json": validManifest("../../escaped"),
	}))

	if _, err := InstallAlias(bundle); err == nil {
		t.Fatal("a bundle whose command_name escapes AliasDir was installed")
	}
}

func TestInstallAliasAcceptsALegitimateBundle(t *testing.T) {
	base := t.TempDir()
	restore := AliasDir
	AliasDir = base
	defer func() { AliasDir = restore }()

	bundle := base64.StdEncoding.EncodeToString(buildBundle(t, map[string]string{
		"alias.json": validManifest("myalias"),
		"tool.exe":   "MZ binary",
	}))

	view, err := InstallAlias(bundle)
	if err != nil {
		t.Fatalf("a legitimate bundle was refused: %v", err)
	}
	if view == nil || view.CommandName != "myalias" {
		t.Fatalf("unexpected install result: %+v", view)
	}
}
