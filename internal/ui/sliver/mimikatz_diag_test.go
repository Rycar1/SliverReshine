package sliver

import (
	"strings"
	"testing"
)

// The real ACCESS_DENIED from sekurlsa on a non-elevated token must name the
// reason and the modules that do work without elevation.
func TestDiagnoseSekurlsaAccessDenied(t *testing.T) {
	const raw = `mimikatz(commandline) # sekurlsa::logonpasswords
ERROR kuhl_m_sekurlsa_acquireLSA ; Handle on memory (0x00000005)

mimikatz(commandline) # exit
Bye!`
	msg := diagnoseMimikatzFailure(raw, "sekurlsa::logonpasswords")
	low := strings.ToLower(msg)
	if !strings.Contains(low, "elevat") {
		t.Errorf("diagnosis does not mention elevation: %q", msg)
	}
	if !strings.Contains(low, "vault::cred") {
		t.Errorf("diagnosis does not offer a workable alternative: %q", msg)
	}
}

func TestDiagnosePrivilegeNotHeld(t *testing.T) {
	const raw = `mimikatz(commandline) # privilege::debug
ERROR kuhl_m_privilege_simple ; RtlAdjustPrivilege (20) c0000061
mimikatz(commandline) # exit
Bye!`
	msg := diagnoseMimikatzFailure(raw, "privilege::debug")
	if !strings.Contains(msg, "SeDebugPrivilege") {
		t.Errorf("diagnosis should name the missing privilege: %q", msg)
	}
}

func TestDiagnoseUnknownModule(t *testing.T) {
	msg := diagnoseMimikatzFailure("mimikatz(commandline) # nope::nope\nERROR ; command not found", "nope::nope")
	if !strings.Contains(msg, "nope::nope") {
		t.Errorf("diagnosis should name the rejected command: %q", msg)
	}
}

func TestDiagnoseEmptyOutput(t *testing.T) {
	msg := diagnoseMimikatzFailure("", "sekurlsa::logonpasswords")
	if msg == "" {
		t.Fatal("empty output must still produce an explanation")
	}
	if !strings.Contains(strings.ToLower(msg), "av") && !strings.Contains(strings.ToLower(msg), "path") {
		t.Errorf("empty output should suggest a cause: %q", msg)
	}
}

// A run that completed but found nothing is not a failure and must not be
// described with one of the error diagnoses.
func TestDiagnoseCleanRunWithNoCreds(t *testing.T) {
	const raw = `mimikatz(commandline) # vault::cred
mimikatz(commandline) # exit
Bye!`
	msg := diagnoseMimikatzFailure(raw, "vault::cred")
	if strings.Contains(strings.ToLower(msg), "denied") {
		t.Errorf("a clean run was described as an access failure: %q", msg)
	}
	if !strings.Contains(msg, "no credentials") {
		t.Errorf("message should say nothing was found: %q", msg)
	}
}

// Guard against the diagnosis itself panicking or returning nothing, whatever
// the input.
func TestDiagnoseNeverReturnsEmpty(t *testing.T) {
	inputs := []string{
		"", "x", "ERROR", "0x00000005", "c0000061", "kuhl_m_lsadump_sam",
		"Credential Guard", "unknown module", "Bye!", "\x00\xff\xfe",
		strings.Repeat("A", 4096),
	}
	for _, in := range inputs {
		if got := diagnoseMimikatzFailure(in, "any::cmd"); got == "" {
			t.Errorf("diagnoseMimikatzFailure(%q) returned empty", in)
		}
	}
}
