package sliver

import (
	"strings"
	"testing"
)

// prepareMimikatzRun resolves the command, mode and payload before anything
// touches the target. The distinction it must keep is the one the run depends
// on: a supplied upload is used verbatim and marked custom, while an empty one
// selects the bundled tool.
func TestPrepareMimikatzRun(t *testing.T) {
	// Default run: the sweep command, auto mode, the bundled payload.
	command, mode, payload, custom, err := prepareMimikatzRun(MimikatzRequest{})
	if err != nil {
		// A build that ships no bundled binary is a legitimate state, and the
		// error must say so rather than hand back an empty payload.
		if !strings.Contains(err.Error(), "no mimikatz binary") {
			t.Fatalf("default run failed for the wrong reason: %v", err)
		}
	} else {
		if command != DefaultMimikatzCommand {
			t.Errorf("command = %q, want %q", command, DefaultMimikatzCommand)
		}
		if mode != MimikatzModeAuto {
			t.Errorf("mode = %q, want %q", mode, MimikatzModeAuto)
		}
		if custom {
			t.Error("an empty upload must not be reported as custom")
		}
		if len(payload) == 0 {
			t.Error("a nil error must not come with an empty payload")
		}
	}

	// A supplied upload is used verbatim, marked custom, and the command trimmed.
	command, mode, payload, custom, err = prepareMimikatzRun(MimikatzRequest{
		Command: "  sekurlsa::wdigest  ",
		Mode:    MimikatzModeUpload,
		Upload:  []byte("MZ-custom"),
	})
	if err != nil {
		t.Fatalf("custom run: %v", err)
	}
	if command != "sekurlsa::wdigest" {
		t.Errorf("command = %q, want the trimmed value", command)
	}
	if mode != MimikatzModeUpload {
		t.Errorf("mode = %q, want %q", mode, MimikatzModeUpload)
	}
	if !custom {
		t.Error("a supplied upload must be marked custom")
	}
	if string(payload) != "MZ-custom" {
		t.Errorf("payload = %q, want the supplied bytes", payload)
	}

	// An unrecognised mode normalises to auto rather than passing through.
	if _, mode, _, _, err := prepareMimikatzRun(MimikatzRequest{Mode: "nonsense", Upload: []byte("x")}); err != nil || mode != MimikatzModeAuto {
		t.Errorf("unknown mode: got (%q, %v), want (%q, nil)", mode, err, MimikatzModeAuto)
	}
}
