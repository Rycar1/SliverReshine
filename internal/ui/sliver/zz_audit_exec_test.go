package sliver

import (
	"os"
	"os/exec"
	"testing"
)

// Runs the REAL argv produced by installCommand/removeCommand on this Windows
// host, to see whether a crafted payload actually executes extra commands.
//
// Opt-in only, via C2TOOL_AUDIT=1. The commands this runs write to the host's
// real registry and to C:\Temp, and one of them previously left cmd.exe waiting
// on stdin -- so as part of the default suite it did not just mutate the machine,
// it hung the run for the full 600s test timeout with no indication of which
// case was responsible. An audit that changes the host has to be something the
// operator asks for.
func TestZZRealExec(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	if os.Getenv("C2TOOL_AUDIT") == "" {
		t.Skip("set C2TOOL_AUDIT=1 to run the host-mutating argv audit")
	}
	marker := `C:\Users\Rycar\Documents\c2tool\zzout\PWNED.txt`
	os.Remove(marker)

	run := func(label string, argv []string) {
		t.Logf("--- %s\n    argv=%q", label, argv)
		c := exec.Command(argv[0], argv[1:]...)
		c.Env = append(os.Environ(), `ZZPWN=x" & echo INJECTED>%TEMP%\ZZPWN_MARKER & rem "`)
		out, err := c.CombinedOutput()
		t.Logf("    out=%q err=%v", string(out), err)
	}

	// 1. baseline: plain payload
	a, err := installCommand(platformWindows, "win-run-key", `C:\Temp\agent.exe`, "ZZAuditTmp")
	if err != nil {
		t.Fatal(err)
	}
	run("win-run-key plain", a)

	// 2. payload containing %VAR% that the target environment defines with a quote
	a, err = installCommand(platformWindows, "win-run-key", `C:\Temp\%ZZPWN%\agent.exe`, "ZZAuditTmp")
	if err != nil {
		t.Fatal(err)
	}
	run("win-run-key %ZZPWN% (quote in value)", a)

	// 3. does a newline in the payload break out?
	a, err = installCommand(platformWindows, "win-run-key", "C:\\Temp\\a.exe\necho NL_BROKE_OUT", "ZZAuditTmp")
	if err != nil {
		t.Fatal(err)
	}
	run("win-run-key LF payload", a)

	// 4. show what the registry actually ended up holding
	out, _ := exec.Command("cmd.exe", "/c", `reg query "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v ZZAuditTmp`).CombinedOutput()
	t.Logf("stored value: %q", string(out))

	exec.Command("cmd.exe", "/c", `reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Run" /v ZZAuditTmp /f`).Run()
	_ = marker
}
