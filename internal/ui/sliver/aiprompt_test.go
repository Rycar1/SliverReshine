package sliver

import (
	"strings"
	"testing"
)

// The prompt and the enforcement have to agree. While the policy is on the
// model is told it is read-only; the moment the operator turns the policy off
// the prompt must stop claiming otherwise, or the model refuses the writes the
// operator just enabled.
func TestAICollectPromptFollowsReadOnlyFlag(t *testing.T) {
	on := aiCollectSystemPrompt(true)
	off := aiCollectSystemPrompt(false)

	if !strings.Contains(on, "read-only reconnaissance assistant") {
		t.Errorf("read-only prompt does not state the read-only role: %q", promptFirstLine(on))
	}
	if !strings.Contains(on, "HARD RULES") {
		t.Errorf("read-only prompt is missing the enforced rules block")
	}
	if !strings.Contains(on, ReadOnlyAllowlist()[0]) {
		t.Errorf("read-only prompt does not carry the allowlist")
	}

	if strings.Contains(off, "HARD RULES") {
		t.Errorf("open prompt still carries the enforced read-only rules block")
	}
	if !strings.Contains(off, "read-only policy is OFF") {
		t.Errorf("open prompt does not tell the model the policy is off: %q", promptFirstLine(off))
	}
	if strings.Contains(off, "Never modify, create, delete") {
		t.Errorf("open prompt still forbids modification, which the operator just allowed")
	}

	if on == off {
		t.Fatal("the two prompts are identical; the flag has no effect")
	}
}

// The per-run task prompt says "read-only probe" only when that is true.
func TestAICollectTaskPromptFollowsReadOnlyFlag(t *testing.T) {
	on := aiCollectTaskPrompt("sess", "grab creds", 5, true)
	off := aiCollectTaskPrompt("sess", "grab creds", 5, false)

	if !strings.Contains(on, "read-only probe") {
		t.Errorf("read-only task prompt lost its wording: %q", on)
	}
	if strings.Contains(off, "read-only probe") {
		t.Errorf("open task prompt still calls the first step read-only: %q", off)
	}
	if !strings.Contains(off, "Session under test: sess") || !strings.Contains(off, "Operator objective: grab creds") {
		t.Errorf("open task prompt dropped the session or objective: %q", off)
	}
}

func promptFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
