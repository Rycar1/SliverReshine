//go:build !embedserver

package embed

import (
	"bytes"
	"strings"
	"testing"
)

// A development build must carry no server payload: that is what makes the
// launcher fall back to an external sliver-server instead of failing at exec
// time on a binary it cannot run. If this starts failing because a payload was
// embedded unconditionally, the fallback path is dead and release-only data is
// now in every build.
func TestDevBuildCarriesNoServerPayload(t *testing.T) {
	if Payload != nil {
		t.Fatalf("dev build carries a %d-byte server payload; it belongs behind the embedserver tag", len(Payload))
	}
	if len(Embedded) != 0 {
		t.Fatalf("dev build reports embedded platforms %v, want none", Embedded)
	}
	if !Compressed {
		t.Error("Compressed must stay true: the launcher decides whether to gunzip based on it")
	}
}

// The mimikatz executable is embedded in every build, release or development, so
// the credential path never depends on an operator supplying a file. A truncated
// or mis-declared embed is caught here rather than at the moment credentials are
// needed on a target.
func TestMimikatzExecutableIsEmbedded(t *testing.T) {
	if len(Mimikatz) == 0 {
		t.Fatal("Mimikatz is empty; the credential harvest path has no binary to upload")
	}
	if !bytes.HasPrefix(Mimikatz, []byte("MZ")) {
		t.Fatal("Mimikatz does not start with the MZ magic: it is not a Windows executable")
	}
}

// The names written to the target must not advertise the tool. This is the whole
// point of staging under an innocuous stem in a temp directory, and it is the
// first thing a responder greps for, so it is pinned rather than left to a
// comment.
func TestMimikatzOnTargetNamesDoNotAdvertiseTheTool(t *testing.T) {
	for _, name := range []string{MimikatzName, MimikatzDLLName} {
		if strings.Contains(strings.ToLower(name), "mimikatz") {
			t.Errorf("%q names the tool on the target", name)
		}
	}
	if !strings.HasSuffix(MimikatzDLLName, ".dll") {
		t.Errorf("MimikatzDLLName = %q, want a .dll suffix for the reflective loader", MimikatzDLLName)
	}
}
