package sliver

import (
	"strings"
	"testing"
)

// shellArgv must stay pure: the tests pin its exact output, and every module's
// command text flows through it.
func TestShellArgvStaysPure(t *testing.T) {
	win := shellArgv(platformWindows, `reg add "HKCU\Software\x" /v y /f`)
	want := []string{"cmd.exe", "/c", "reg", "add", `HKCU\Software\x`, "/v", "y", "/f"}
	if strings.Join(win, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("unexpected windows argv:\n got %q\nwant %q", win, want)
	}
	// No code-page prologue here; runPersistenceCommand adds it at execution.
	for _, a := range win {
		if strings.Contains(a, "chcp") {
			t.Errorf("shellArgv added the code-page prologue: %q", win)
		}
	}

	posix := shellArgv(platformLinux, "crontab -l")
	if len(posix) != 3 || posix[0] != "/bin/sh" || posix[2] != "crontab -l" {
		t.Errorf("posix argv was altered: %q", posix)
	}
}

// The UTF-8 prologue is added where the command is actually run, and only for
// cmd.exe: it is a cmd.exe-ism that would break /bin/sh.
func TestUtf8ProbeOnlyTargetsCmd(t *testing.T) {
	if got := filepathBase("cmd.exe"); got != "cmd.exe" {
		t.Errorf("filepathBase(cmd.exe) = %q", got)
	}
	if got := filepathBase(`C:\Windows\System32\cmd.exe`); got != "cmd.exe" {
		t.Errorf("filepathBase(quoted path) = %q", got)
	}
	if got := filepathBase("/bin/sh"); got != "sh" {
		t.Errorf("filepathBase(/bin/sh) = %q", got)
	}
}

// A stock Windows desktop already has autostart entries. Enumerating them must
// not be reported as "our persistence is installed".
func TestDetectRunKeyWithoutNameIsNotEvidence(t *testing.T) {
	// What `reg query <key>` prints on an ordinary machine.
	const enumeration = `
HKEY_CURRENT_USER\Software\Microsoft\Windows\CurrentVersion\Run
    Steam    REG_SZ    "D:\Steam\steam.exe" -silent
    GoogleChromeAutoLaunch    REG_SZ    "C:\...\chrome.exe" --startup
    OneDrive    REG_SZ    "C:\...\OneDrive.exe" /background
`
	installed, detail := detectPersistence("win-run-key", enumeration, "", 0, "")
	if installed {
		t.Fatalf("three unrelated autostarts were reported as our implant: %q", detail)
	}
	// The enumeration is still worth surfacing as reconnaissance.
	if detail == "" {
		t.Error("the count of autostart values should be reported as detail")
	}

	// With a name, a matching value IS evidence.
	installed, detail = detectPersistence("win-run-key", enumeration, "", 0, "Steam")
	if !installed {
		t.Error("a named match should be reported as installed")
	}
	if detail == "" {
		t.Error("a match should carry detail")
	}

	// A name that is not present is a negative answer, not a fallback to true.
	installed, _ = detectPersistence("win-run-key", enumeration, "", 0, "c2guard")
	if installed {
		t.Error("a name absent from the enumeration must not be reported as installed")
	}
}

func TestDetectRunKeyHonoursFailure(t *testing.T) {
	// reg query on a missing key exits non-zero; that is a negative answer.
	installed, _ := detectPersistence("win-run-key", "", "ERROR: The system was unable to find the specified registry key", 1, "c2guard")
	if installed {
		t.Error("a failed query must not report installed")
	}
}

// An empty key must not be mistaken for a populated one.
func TestDetectRunKeyOnEmptyKey(t *testing.T) {
	const empty = "HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Run\r\n\r\n"
	installed, detail := detectPersistence("win-run-key", empty, "", 0, "")
	if installed {
		t.Error("an empty Run key must not report installed")
	}
	if detail != "" {
		t.Errorf("an empty key has nothing to report, got %q", detail)
	}
}
