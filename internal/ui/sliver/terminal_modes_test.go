package sliver

import (
	"reflect"
	"strings"
	"testing"
)

// exec mode has no shell, so the console has to split a command line itself.
// That parser is the whole of the mode's syntax, which makes it worth testing
// properly: a wrong split does not fail loudly, it runs a different program with
// different arguments than the operator typed.

func TestSplitCommandLineBasic(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"ls", []string{"ls"}},
		{"ls -la", []string{"ls", "-la"}},
		{"ls  -la   /tmp", []string{"ls", "-la", "/tmp"}},
		{"  spaced  out  ", []string{"spaced", "out"}},
		{"\tls\t-la", []string{"ls", "-la"}},
		{"", nil},
		{"   ", nil},
	}
	for _, tc := range cases {
		got, err := SplitCommandLine(tc.in)
		if err != nil {
			t.Errorf("SplitCommandLine(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCommandLine(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// A path with a space is the reason this parser exists at all -- without quote
// handling, a Windows Program Files path cannot be passed.
func TestSplitCommandLineQuoting(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`"a b" c`, []string{"a b", "c"}},
		{`'a b' c`, []string{"a b", "c"}},
		{`cmd /c "echo hello"`, []string{"cmd", "/c", "echo hello"}},
		{`"C:\Program Files\app.exe" -x`, []string{`C:\Program Files\app.exe`, "-x"}},
		// An empty quoted argument is a real argument, not nothing.
		{`a "" b`, []string{"a", "", "b"}},
		// Adjacent quoted and bare text form one word, as in a shell.
		{`pre"mid"post`, []string{"premidpost"}},
	}
	for _, tc := range cases {
		got, err := SplitCommandLine(tc.in)
		if err != nil {
			t.Errorf("SplitCommandLine(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCommandLine(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// A single quote inside double quotes is literal, and vice versa. Getting this
// wrong mangles paths like C:\Users\O'Brien\.
func TestSplitCommandLineQuoteNesting(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`"it's"`, []string{"it's"}},
		{`'say "hi"'`, []string{`say "hi"`}},
		{`"C:\Users\O'Brien\x"`, []string{`C:\Users\O'Brien\x`}},
	}
	for _, tc := range cases {
		got, err := SplitCommandLine(tc.in)
		if err != nil {
			t.Errorf("SplitCommandLine(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCommandLine(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

// An unbalanced quote is reported rather than guessed at. Silently treating the
// rest of the line as literal would run something the operator did not type.
func TestSplitCommandLineRejectsUnbalancedQuotes(t *testing.T) {
	for _, in := range []string{`"unterminated`, `'unterminated`, `a "b`, `a 'b`} {
		if _, err := SplitCommandLine(in); err == nil {
			t.Errorf("SplitCommandLine(%q) accepted an unbalanced quote", in)
		}
	}
}

// The constructs this mode cannot honour are named individually, so the operator
// learns what is unsupported instead of watching a program receive odd
// arguments. Passing "|" through as a literal argument is the failure this
// prevents.
func TestUnsupportedShellSyntaxIsNamed(t *testing.T) {
	cases := map[string]string{
		`ls | grep x`:    "|",
		`a || b`:         "||",
		`echo x > f`:     ">",
		`echo x >> f`:    ">>",
		`cat < f`:        "<",
		`sleep 1 &`:      "&",
		`a && b`:         "&&",
		`a ; b`:          ";",
		"echo `whoami`":  "backtick",
		`echo $(whoami)`: "$(",
		`ls *.txt`:       "glob",
		`ls file?.txt`:   "glob",
	}
	for in, want := range cases {
		got := unsupportedShellSyntax(in)
		if got == "" {
			t.Errorf("unsupportedShellSyntax(%q) = nothing, want it to flag %s", in, want)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("unsupportedShellSyntax(%q) = %q, want it to mention %q", in, got, want)
		}
	}
}

// A quoted operator is data, not syntax. This is what makes it possible to pass
// an argument that happens to contain one.
func TestUnsupportedShellSyntaxIgnoresQuotedOperators(t *testing.T) {
	ok := []string{
		`echo "a|b"`,
		`echo 'a > b'`,
		`grep "x|y" file`,
		`echo "*.txt"`,
		`program --pattern='a|b'`,
		`echo "it's fine"`,
	}
	for _, in := range ok {
		if got := unsupportedShellSyntax(in); got != "" {
			t.Errorf("unsupportedShellSyntax(%q) flagged %s, but it is quoted and therefore data", in, got)
		}
	}
}

// Ordinary commands must not trip the syntax check, or the mode is useless.
func TestUnsupportedShellSyntaxAllowsPlainCommands(t *testing.T) {
	ok := []string{
		"ls", "ls -la /tmp", "cat /etc/passwd", "id",
		`C:\Windows\System32\whoami.exe`, "ps aux", "ip addr show",
		"find / -name x", "echo hello world",
	}
	for _, in := range ok {
		if got := unsupportedShellSyntax(in); got != "" {
			t.Errorf("unsupportedShellSyntax(%q) flagged %s, want nothing", in, got)
		}
	}
}

func TestNormalizeTerminalMode(t *testing.T) {
	cases := map[string]string{
		"":           TerminalModeShell,
		"shell":      TerminalModeShell,
		"SHELL":      TerminalModeShell,
		" shell ":    TerminalModeShell,
		"copy":       TerminalModeShellCopy,
		"shell-copy": TerminalModeShellCopy,
		"shell_copy": TerminalModeShellCopy,
		"exec":       TerminalModeExec,
		"raw":        TerminalModeExec,
		"noshell":    TerminalModeExec,
		"EXEC":       TerminalModeExec,
		// Unknown falls back to the behaviour that always existed rather than
		// refusing, because the value arrives from a page that may be older than
		// this build.
		"nonsense": TerminalModeShell,
		"../etc":   TerminalModeShell,
	}
	for in, want := range cases {
		if got := NormalizeTerminalMode(in); got != want {
			t.Errorf("NormalizeTerminalMode(%q) = %q, want %q", in, got, want)
		}
	}
}

// joinRemote has to use the TARGET's separator. Using the console's would
// produce "C:\Windows\Temp/cmd.exe" whenever the operator runs Windows and the
// target is Linux, or the reverse.
func TestJoinRemoteUsesTheTargetSeparator(t *testing.T) {
	if got := joinRemote(`C:\Windows\Temp`, "cmd.exe"); got != `C:\Windows\Temp\cmd.exe` {
		t.Errorf("windows join = %q", got)
	}
	if got := joinRemote(`C:\Windows\Temp\`, "cmd.exe"); got != `C:\Windows\Temp\cmd.exe` {
		t.Errorf("trailing separator not handled: %q", got)
	}
	if got := joinRemote("/tmp", "sh"); got != "/tmp/sh" {
		t.Errorf("unix join = %q", got)
	}
	if got := joinRemote("/tmp/", "sh"); got != "/tmp/sh" {
		t.Errorf("trailing separator not handled: %q", got)
	}
	// A drive-relative path is still a Windows one.
	if got := joinRemote("C:", "cmd.exe"); got != `C:\cmd.exe` {
		t.Errorf("drive-relative join = %q", got)
	}
}

// The candidate list decides what gets copied. An explicit request has to win,
// or the operator's choice is silently ignored.
func TestShellCandidatesForRespectsAnExplicitRequest(t *testing.T) {
	got := shellCandidatesFor("windows", `D:\custom\shell.exe`)
	if len(got) != 1 || got[0] != `D:\custom\shell.exe` {
		t.Errorf("explicit path was not honoured: %#v", got)
	}
	got = shellCandidatesFor("linux", "/opt/myshell")
	if len(got) != 1 || got[0] != "/opt/myshell" {
		t.Errorf("explicit path was not honoured on linux: %#v", got)
	}
}

// With no explicit request, cmd.exe leads on Windows: PowerShell is the shell
// that most often fails to start on an older or locked-down host, so copying it
// first would be choosing the likelier failure.
func TestShellCandidatesPreferWhatIsLikelyToWork(t *testing.T) {
	win := shellCandidatesFor("windows", "")
	if len(win) == 0 || !strings.EqualFold(win[0], `C:\Windows\System32\cmd.exe`) {
		t.Errorf("windows candidates should lead with cmd.exe, got %#v", win)
	}
	nix := shellCandidatesFor("linux", "")
	if len(nix) == 0 || nix[0] != "/bin/sh" {
		t.Errorf("unix candidates should lead with /bin/sh, got %#v", nix)
	}
	// A busierbox image may only have busybox, so it has to be reachable.
	found := false
	for _, c := range nix {
		if c == "/busybox" {
			found = true
		}
	}
	if !found {
		t.Error("busybox is not among the unix candidates, so a minimal image cannot be served")
	}
}

func TestIsRootedPath(t *testing.T) {
	rooted := []string{"/bin", `/bin`, `C:\x`, "C:/x", `\\srv\share`}
	rel := []string{"", "bin", "./x", `..\x`, "sub/dir", `C:x`}
	for _, p := range rooted {
		if !isRootedPath(p) {
			t.Errorf("isRootedPath(%q) = false, want true", p)
		}
	}
	for _, p := range rel {
		if isRootedPath(p) {
			t.Errorf("isRootedPath(%q) = true, want false", p)
		}
	}
}
