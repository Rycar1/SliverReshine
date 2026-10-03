package launch

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// hostMagic returns the executable magic the host OS expects, so a test probe can
// be built that the format check accepts.
func hostMagic() []byte {
	switch runtime.GOOS {
	case "windows":
		return magicPE
	case "darwin":
		return magicMachO64
	default:
		return magicELF
	}
}

// withExpectedContent installs a known digest/size for the duration of a test.
//
// The values are package-level because the real ones are computed once from the
// embedded payload, which only exists in `embedserver` builds. Overriding them
// lets the verification logic be exercised in every build instead of skipping,
// which is the difference between a test that runs in CI and one that never does.
func withExpectedContent(t *testing.T, content []byte) {
	t.Helper()
	oldDigest, oldSize := embeddedServerDigest, embeddedServerSize
	sum := sha256.Sum256(content)
	embeddedServerDigest = sum[:]
	embeddedServerSize = int64(len(content))
	t.Cleanup(func() {
		embeddedServerDigest, embeddedServerSize = oldDigest, oldSize
	})
}

// verifyExtractedContent must reject a file that is not byte-identical to the
// server the launcher carries.
//
// This is the defence the reuse path was missing: the extracted name is derived
// from the payload digest and printed in the launcher's own log, so anyone who can
// create a file in the extraction directory knows what to call it, and four magic
// bytes are satisfied by any PE or ELF. The file is then executed as
// `sliver-server daemon`, `unpack` and `operator`.
func TestVerifyExtractedContentRejectsAPlantedFile(t *testing.T) {
	dir := t.TempDir()

	// What the old check accepted: a valid header for THIS host and nothing else.
	// The magic has to match the host, or verifyExecutableFormat rejects it for the
	// wrong reason and the test would pass without proving anything.
	planted := filepath.Join(dir, "planted")
	head := make([]byte, 4096)
	copy(head, hostMagic())
	if err := os.WriteFile(planted, head, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyExecutableFormat(planted); err != nil {
		t.Fatalf("the probe is not shaped like an executable, so it does not test the "+
			"old check: %v", err)
	}

	// The launcher expects something else entirely.
	withExpectedContent(t, bytes.Repeat([]byte("real server bytes"), 512))

	if err := verifyExtractedContent(planted); err == nil {
		t.Error("verifyExtractedContent accepted a planted binary that the old " +
			"magic-byte check also accepted")
	}
}

// The correct content must be accepted, or the fix would break every start.
func TestVerifyExtractedContentAcceptsTheRealThing(t *testing.T) {
	content := bytes.Repeat([]byte("the actual server binary"), 1024)
	withExpectedContent(t, content)

	p := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(p, content, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyExtractedContent(p); err != nil {
		t.Errorf("verifyExtractedContent rejected the correct content: %v", err)
	}
}

// A file with the right size but the wrong bytes must be rejected -- that is what
// separates a digest check from a size check.
func TestVerifyExtractedContentRejectsRightSizeWrongBytes(t *testing.T) {
	content := bytes.Repeat([]byte("expected"), 1000)
	withExpectedContent(t, content)

	p := filepath.Join(t.TempDir(), "same-size")
	if err := os.WriteFile(p, make([]byte, len(content)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyExtractedContent(p); err == nil {
		t.Error("verifyExtractedContent accepted a same-sized file of zero bytes")
	}
}

// One byte different is enough to reject.
func TestVerifyExtractedContentRejectsOneByteChange(t *testing.T) {
	content := bytes.Repeat([]byte("expected"), 1000)
	withExpectedContent(t, content)

	tampered := append([]byte(nil), content...)
	tampered[len(tampered)/2] ^= 0x01

	p := filepath.Join(t.TempDir(), "tampered")
	if err := os.WriteFile(p, tampered, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyExtractedContent(p); err == nil {
		t.Error("verifyExtractedContent accepted a file differing by one byte")
	}
}

// With no payload to compare against, the check must not fail: a development
// build carries none, and refusing there would turn that into a startup failure.
func TestVerifyExtractedContentSkipsWhenNothingToCompare(t *testing.T) {
	oldDigest, oldSize := embeddedServerDigest, embeddedServerSize
	embeddedServerDigest, embeddedServerSize = nil, 0
	t.Cleanup(func() { embeddedServerDigest, embeddedServerSize = oldDigest, oldSize })

	p := filepath.Join(t.TempDir(), "anything")
	if err := os.WriteFile(p, []byte("whatever"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyExtractedContent(p); err != nil {
		t.Errorf("verifyExtractedContent failed with no payload to compare: %v", err)
	}
}

// tempExtractPath must not hand back a path that already exists, or the
// exclusive-create guarantee is worthless.
func TestTempExtractPathIsUniqueAndExclusive(t *testing.T) {
	dir := t.TempDir()

	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		p, err := tempExtractPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatalf("tempExtractPath returned %q twice", p)
		}
		seen[p] = true
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the reserved path %q does not exist: %v", p, err)
		}
		// A second exclusive create at the same path must fail.
		if _, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
			t.Errorf("O_EXCL succeeded on an existing path %q", p)
		}
	}
}

// The temp name must be unpredictable, since a guessable one makes the
// exclusive create a formality.
func TestTempExtractPathIsUnpredictable(t *testing.T) {
	dir := t.TempDir()
	a, err := tempExtractPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := tempExtractPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(a) == filepath.Base(b) {
		t.Errorf("two reservations produced the same name: %q", filepath.Base(a))
	}
	// It must not be the old fixed name either.
	if filepath.Base(a) == "sliver-server.part" {
		t.Error("the temp name is still predictable")
	}
}
