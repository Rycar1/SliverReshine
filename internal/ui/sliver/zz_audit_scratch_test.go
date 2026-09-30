package sliver

import (
	"fmt"
	"testing"
)

func dump(t *testing.T, label string, argv []string, err error) {
	if err != nil {
		t.Logf("%-46s ERR: %v", label, err)
		return
	}
	t.Logf("%-46s %q", label, argv)
}

func TestZZAuditDump(t *testing.T) {
	// Adversarial payloads
	payloads := []string{
		`C:\Temp\a.exe`,
		`C:\Temp\a&calc.exe`,
		`C:\Temp\a%TEMP%b.exe`,
		`C:\Temp\%COMSPEC%`,
		`C:\Temp\a.bat&whoami`,
		`x%USERNAME%`,
		`/tmp/a; id`,
		`/tmp/a'$(id)'`,
		`/tmp/a#b`,
	}
	for _, p := range payloads {
		argv, err := installCommand(platformWindows, "win-schtask", p, "Updater")
		dump(t, "win-schtask install "+p, argv, err)
	}
	t.Log("---- runkey / service ----")
	for _, p := range payloads[:5] {
		a, e := installCommand(platformWindows, "win-run-key", p, "Updater")
		dump(t, "win-run-key "+p, a, e)
	}
	for _, p := range payloads[:5] {
		a, e := installCommand(platformWindows, "win-service", p, "Updater")
		dump(t, "win-service "+p, a, e)
	}
	t.Log("---- local account ----")
	a, e := installCommand(platformWindows, "win-local-account", "P@ss&calc.exe", "svc")
	dump(t, "win-local-account pw=P@ss&calc.exe", a, e)
	a, e = installCommand(platformWindows, "win-local-account", `P%USERNAME%ss`, "svc")
	dump(t, "win-local-account pw=P%USERNAME%ss", a, e)
	t.Log("---- winlogon userinit ----")
	a, e = installCommand(platformWindows, "win-winlogon-userinit", `C:\a.exe&calc`, "x")
	dump(t, "winlogon-userinit &", a, e)
	t.Log("---- startup folder ----")
	a, e = installCommand(platformWindows, "win-startup-folder", `C:\a.exe`, "Updater")
	dump(t, "startup install", a, e)
	a, e = removeCommand(platformWindows, "win-startup-folder", "", "Updater")
	dump(t, "startup remove", a, e)
	t.Log("---- linux bashrc / authorized_keys ----")
	for _, p := range []string{`/tmp/a`, `/tmp/a' ; id #`, `a`} {
		a, e = installCommand(platformLinux, "linux-bashrc", p, "n")
		dump(t, "bashrc install "+p, a, e)
		a, e = removeCommand(platformLinux, "linux-bashrc", p, "")
		dump(t, "bashrc remove "+p, a, e)
		a, e = removeCommand(platformLinux, "linux-ssh-authorized-keys", p, "")
		dump(t, "authkeys remove "+p, a, e)
	}
	t.Log("---- invalid names ----")
	for _, n := range []string{"", "ok", "a b", "a;b", "a'b", `a"b`, "a%b", "a&b", "a|b", "a$b", "a`b", "a.b-c_d"} {
		_, e := installCommand(platformWindows, "win-schtask", `C:\a.exe`, n)
		a2, e2 := installCommand(platformWindows, "win-logon-script", `C:\a.exe`, n)
		t.Logf("name=%-10q schtaskErr=%v logonScriptErr=%v logonScriptArgv=%q", n, e, e2, a2)
	}
	t.Log("---- inspect names ----")
	for _, n := range []string{"", "ok", "a b", "a&b"} {
		a, e := inspectCommand(platformWindows, "win-schtask", n)
		dump(t, fmt.Sprintf("inspect schtask name=%q", n), a, e)
	}
	t.Log("---- office-test / logon-script installs (name unvalidated) ----")
	for _, n := range []string{"ok", "a b", "a&b", "a|b", "a^b", "a%b"} {
		a, e := installCommand(platformWindows, "win-office-test", `C:\a.exe`, n)
		dump(t, fmt.Sprintf("office-test install name=%q", n), a, e)
		a, e = installCommand(platformWindows, "win-logon-script", `C:\a.exe`, n)
		dump(t, fmt.Sprintf("logon-script install name=%q", n), a, e)
		a, e = removeCommand(platformWindows, "win-office-test", "", n)
		dump(t, fmt.Sprintf("office-test remove name=%q", n), a, e)
	}
	t.Log("---- linux systemd name unvalidated ----")
	for _, n := range []string{"ok", "a b", "a;id", "a$b", "a`id`"} {
		a, e := installCommand(platformLinux, "linux-ssh-authorized-keys", "KEY", n)
		dump(t, fmt.Sprintf("authkeys install name=%q", n), a, e)
		_ = a
		_ = e
	}
	t.Log("---- detectPersistence fooling ----")
	var st uint32 = 0
	inst, det := detectPersistence("win-run-key", "    Foo    REG_SZ    C:\\x.exe\n", "", st, "")
	t.Logf("runkey unnamed -> installed=%v detail=%q", inst, det)
	inst, det = detectPersistence("win-run-key", "    Updater    REG_SZ    C:\\x.exe\n", "", st, "Updater")
	t.Logf("runkey named -> installed=%v detail=%q", inst, det)
}
