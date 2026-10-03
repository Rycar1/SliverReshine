//go:build embedserver

package embed

import (
	"bytes"
	"sort"
	"strings"
	"testing"
)

// The keys are shown to an operator when the host platform is not covered, so
// they must be sorted and framed as goos/goarch rather than as filenames.
func TestListPayloadsIsSortedAndFramed(t *testing.T) {
	got := listPayloads()
	if !sort.StringsAreSorted(got) {
		t.Fatalf("listPayloads() = %v, want sorted for stable diagnostics", got)
	}
	for _, key := range got {
		if !strings.Contains(key, "/") {
			t.Errorf("key %q is not goos/goarch", key)
		}
		if strings.Contains(key, ".gz") || strings.Contains(key, "payload-") {
			t.Errorf("key %q still carries the filename framing", key)
		}
	}
	if len(got) != len(Embedded) {
		t.Errorf("Embedded = %v but listPayloads() = %v", Embedded, got)
	}
}

// A platform with no embedded payload must answer nil, not a zero-length slice:
// the launcher distinguishes "not embedded" from "embedded but empty" to decide
// whether to fall back to an external server binary.
func TestSelectPayloadRefusesAnAbsentPlatform(t *testing.T) {
	if data := selectPayload("bogus", "bogus"); data != nil {
		t.Errorf("selectPayload(bogus, bogus) returned %d bytes, want nil", len(data))
	}
}

// Every advertised key must actually resolve, and resolve to a gzip stream. This
// is what catches a payload added to the directory but not to the launcher's
// keying, or a file that was copied in truncated.
func TestEmbeddedPayloadsAreGzipStreams(t *testing.T) {
	if testing.Short() {
		t.Skip("reads every embedded payload; skipped under -short")
	}
	keys := listPayloads()
	if len(keys) == 0 {
		t.Skip("this build carries no server payload")
	}
	for _, key := range keys {
		goos, goarch, ok := strings.Cut(key, "/")
		if !ok {
			t.Errorf("key %q is not goos/goarch", key)
			continue
		}
		data := selectPayload(goos, goarch)
		if len(data) == 0 {
			t.Errorf("%s is listed but selectPayload returned nothing", key)
			continue
		}
		if !bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
			t.Errorf("%s does not start with the gzip magic", key)
		}
	}
}
