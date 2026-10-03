package sliver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// These tests cover the cmd.exe quoting hole that made every Windows persistence
// module injectable.
//
// The defect: shellQuoteWindows wrapped a value in quotes, and shellArgv split
// the script back into a token list, which dropped them. Go's syscall.EscapeArg
// then wrapped an argument in quotes again only when it contained a space, tab or
// quote -- so a payload with a metacharacter and NO space reached cmd.exe bare,
// and cmd.exe read `&`, `|`, `<`, `>`, `^` as syntax:
//
//	payload C:\Temp\a.exe&whoami   ->  whoami ran as a second command
//	payload C:\Temp\a.exe>pwned.txt ->  the file was created
//
// The fix escapes each token with caret -- cmd.exe's own escape -- at the point
// after the split, and only when Go will hand the token over bare. Caret was
// chosen over adding quotes because adding quotes corrupts the value: Go renders
// a quote inside an argument as \" and cmd has no backslash escape, so the
// target program would receive \"C:\Temp\a.exe&whoami\".

// argPrinterPath builds and returns the path to a helper that prints its argv, so
// a test can observe exactly what a target program receives. `echo` cannot be
// used for this: it is a cmd builtin that prints the raw text, quotes and all, so
// it cannot distinguish "cmd stripped the quotes" from "cmd passed them through".
func argPrinterPath(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	src := filepath.Join(dir, "argprint.go")
	const program = `package main

import (
	"fmt"
	"os"
)

func main() {
	for i, a := range os.Args[1:] {
		fmt.Printf("%d:%s\n", i, a)
	}
}
`
	if err := os.WriteFile(src, []byte(program), 0o600); err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(dir, "argprint.exe")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the argv helper: %v (%s)", err, out)
	}
	return exe
}

// runViaCmd runs argv with the given program substituted for the command name,
// then returns what that program received as its arguments.
func runViaCmd(t *testing.T, argv []string, replace, with string) ([]string, string) {
	t.Helper()

	probe := make([]string, len(argv))
	copy(probe, argv)
	for i, a := range probe {
		if a == replace {
			probe[i] = with
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, probe[0], probe[1:]...).CombinedOutput()

	var got []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if _, v, ok := strings.Cut(line, ":"); ok {
			got = append(got, v)
		}
	}
	return got, string(out)
}

// Every metacharacter that cmd.exe interprets must be inert, and the value must
// still arrive byte-for-byte. Both halves are asserted together: escaping that
// makes cmd ignore the character but corrupts the value is not a fix -- the
// persistence entry would point at a path that does not exist.
//
// The working directory is a temp dir, so a regression that lets the payload run
// writes its litter there instead of into the repository. Running this test in
// the package directory is how `pwned.txt` and `b.exe` ended up checked into the
// tree while the defect was being confirmed.
func TestPersistencePayloadMetacharactersAreInert(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe quoting is only exercised on Windows")
	}
	t.Chdir(t.TempDir())
	helper := argPrinterPath(t)

	for _, payload := range []string{
		`C:\Temp\a.exe&whoami`,
		`C:\Temp\a.exe|whoami`,
		`C:\Temp\a.exe>pwned.txt`,
		`C:\Temp\a<b.exe`,
		`C:\Temp\a^b.exe`,
		`C:\Program Files\a.exe&whoami`,
		`C:\Users\John Smith\a.exe`,
	} {
		argv, err := installCommand(platformWindows, "win-run-key", payload, "QuotingTest")
		if err != nil {
			t.Errorf("installCommand rejected a usable payload %q: %v", payload, err)
			continue
		}

		got, raw := runViaCmd(t, argv, "reg", helper)

		// The value must be present, intact, exactly once.
		var found string
		for _, a := range got {
			if strings.Contains(a, ".exe") {
				found = a
			}
		}
		if found != payload {
			t.Errorf("payload %q reached the target program as %q (raw output %q)", payload, found, raw)
		}

		// And nothing from the payload may have been executed. The helper prints
		// only its own argv, so any line that is not `<n>:<arg>` came from cmd.exe
		// running something. Count the lines instead of matching on a username:
		// an operator name is machine-specific, and hardcoding one is how the
		// scratch test this replaces ended up failing on every other machine.
		lines := 0
		for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
			if line != "" {
				lines++
			}
		}
		if lines != len(got) {
			t.Errorf("payload %q produced output beyond its own argv: %q", payload, raw)
		}
	}
}

// A script that needs a shell must keep working. The `&&` in the local-account
// module is an operator on purpose, not data.
func TestPersistenceKeepsItsShellOperators(t *testing.T) {
	argv, err := installCommand(platformWindows, "win-local-account", `P@ssw0rd`, "svc")
	if err != nil {
		t.Fatalf("win-local-account: %v", err)
	}

	var sawAnd bool
	for _, a := range argv {
		if a == "&&" {
			sawAnd = true
		}
	}
	if !sawAnd {
		t.Fatalf("the && operator was escaped or merged into data: %q", argv)
	}
}

// The generated script is unchanged for ordinary values. The escaping must be
// invisible to a payload that contains no metacharacter.
func TestPersistenceOrdinaryPayloadIsUnchanged(t *testing.T) {
	for _, module := range []string{"win-run-key", "win-schtask", "win-service", "win-office-test"} {
		argv, err := installCommand(platformWindows, module, `C:\Temp\agent.exe`, "svc")
		if err != nil {
			t.Fatalf("%s: %v", module, err)
		}
		for _, a := range argv {
			if strings.Contains(a, "^") {
				t.Errorf("%s: an ordinary payload gained a caret: %q", module, argv)
			}
		}
	}
}

// The linux-cron module nested a single-quoted value inside a double-quoted echo,
// so a payload containing a double quote closed the argument and the rest of the
// line ran as a command. Measured before the fix:
//
//	payload /tmp/a" ; echo INJECTED_CRON ; "b  ->  INJECTED_CRON ran
//	payload /tmp/agent"v2                   ->  "Unterminated quoted string"
func TestLinuxCronCarriesQuotesLiterally(t *testing.T) {
	for _, payload := range []string{
		`/tmp/agent`,
		`/tmp/agent"v2`,
		`/tmp/a" ; echo INJECTED_CRON ; "b`,
		`/tmp/it's here/agent`,
	} {
		argv, err := installCommand(platformLinux, "linux-cron", payload, "svc")
		if err != nil {
			t.Fatalf("linux-cron rejected %q: %v", payload, err)
		}
		if len(argv) != 3 {
			t.Fatalf("unexpected argv shape for %q: %q", payload, argv)
		}

		script := argv[2]
		// The nested quoting must be gone.
		if strings.Contains(script, `echo "@reboot`) {
			t.Errorf("the double-quoted echo is back for %q: %s", payload, script)
		}
		// And the single-quoted unit must contain the payload verbatim, so what
		// crontab receives is the path and not a fragment of it.
		if !strings.Contains(script, `'@reboot `+strings.ReplaceAll(payload, `'`, `'\''`)+`'`) {
			t.Errorf("payload %q is not carried literally: %s", payload, script)
		}
	}
}
