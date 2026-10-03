package sliver

import (
	"errors"
	"fmt"
	"strings"
)

// nameModules are the modules whose artifact name is interpolated into an
// unquoted token or a filesystem path, so it must be a real, plain identifier.
var nameModules = map[string]bool{
	"win-run-key":           true,
	"win-run-key-hklm":      true,
	"win-startup-folder":    true,
	"win-schtask":           true,
	"win-schtask-boot":      true,
	"win-watchdog":          true,
	"win-service":           true,
	"win-local-account":     true,
	"win-office-test":       true,
	"win-winlogon-userinit": true,
	// win-logon-script is deliberately absent: its value name is fixed, so the
	// inventory can answer that row without being told which artifact to look for.
	"linux-systemd":      true,
	"linux-systemd-user": true,
	"linux-watchdog":     true,
}

// validateName enforces the artifact-name charset. Everything outside it would
// escape whichever token it lands in, so this is the one gate every entry point
// -- install, remove and the inventory lookup -- has to pass through.
func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("name is too long")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("name may only contain letters, digits, dot, underscore and dash")
		}
	}
	return nil
}

// requireName validates the artifact name for a module, but only for modules
// that actually interpolate one. A module that ignores the field accepts an
// empty value, which is what the inventory's unnamed pass relies on.
func requireName(module, name string) error {
	if !nameModules[module] {
		return nil
	}
	if err := validateName(name); err != nil {
		return fmt.Errorf("module %q: %w", module, err)
	}
	return nil
}

// shellQuotePOSIX single-quotes s for a POSIX shell, escaping embedded single
// quotes as '\” so the child shell re-parses the original bytes exactly.
func shellQuotePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// quoteEachPOSIX quotes a list so each element survives as one argv word.
func quoteEachPOSIX(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, it := range items {
		quoted = append(quoted, shellQuotePOSIX(it))
	}
	return strings.Join(quoted, " ")
}

// shellQuoteWindows double-quotes s for cmd.exe.
//
// A double quote has no safe representation inside a quoted argument, so it is
// rejected rather than mangled. CR and LF are rejected too, and that is
// load-bearing: shellArgv hands cmd.exe a token list, and Go quotes an argument
// containing a newline into a single "...\n..." argument. cmd.exe reads an
// unterminated line and waits on stdin, so the spawn never returns and the
// operator sees the request hang until it times out.
//
// # Why this function alone is not enough
//
// The quotes added here are not reliably present by the time cmd.exe sees the
// value. shellArgv splits the script back into a token list, which drops them,
// and Go's syscall.EscapeArg re-quotes an argument only when it contains a space,
// tab or quote. A payload with a metacharacter and no space therefore reached
// cmd.exe bare, and cmd.exe treated the character as syntax:
//
//	&  splits the command; the text after it ran as a second command
//	|  pipes; the text after it ran
//	<  redirection -- "The system cannot find the file specified."
//	>  redirection -- silent, and the value was truncated at the character
//	^  cmd's escape, consumed: `a^b.exe` became `ab.exe`
//
// The actual protection is protectCmdToken, applied per token after the split,
// because that is the layer at which it is still possible to control whether Go
// will quote the argument. See its comment.
//
// `%` is deliberately allowed: a payload is a path on the target, and
// `%APPDATA%\agent.exe` is a legitimate value that has to reach the target
// un-expanded by us and expand there. It is the one metacharacter whose
// expansion is a feature rather than the bug.
func shellQuoteWindows(s string) (string, error) {
	if strings.Contains(s, `"`) {
		return "", errors.New(`value may not contain a double quote`)
	}
	if strings.ContainsAny(s, "\r\n") {
		return "", errors.New(`value may not contain a newline`)
	}
	return `"` + s + `"`, nil
}

// isCmdOperator reports whether tok is meant to be interpreted by cmd.exe rather
// than passed through as data. Those tokens are what makes scripts such as
// `net user x p /add && net localgroup Administrators x /add` work, so they must
// not be protected.
func isCmdOperator(tok string) bool {
	switch tok {
	case "&", "&&", "|", "||", "<", ">", ">>":
		return true
	}
	return false
}

// protectCmdToken makes one argv element survive cmd.exe's parsing intact.
//
// This exists because "the argument is quoted" and "cmd.exe sees a quoted
// argument" are not the same claim. Go's syscall.EscapeArg wraps an argument in
// quotes only when it contains a space, a tab or a quote. Anything else is handed
// over bare, and cmd.exe then reads `&`, `|`, `<`, `>` and `^` inside it as
// syntax rather than as text:
//
//	C:\Temp\a.exe&whoami    ran whoami as a second command
//	C:\Temp\a.exe>pwned.txt  wrote the file and truncated the value
//
// # Why this escapes with caret instead of adding quotes
//
// The obvious fix -- wrap the token in quotes here so Go has to quote it -- was
// tried and measured, and it corrupts the value. Go renders a quote inside an
// argument as \" (a C-runtime convention), and cmd.exe has no backslash escape,
// so it reads that as a literal backslash followed by a quote toggle. The value
// the target program then receives is `\"C:\Temp\a.exe&whoami\"` -- inert, but
// wrong, so the persistence entry points at a path that does not exist. Inert and
// corrupt is not a fix.
//
// Caret is cmd.exe's own escape character, and the only one that survives the
// trip: cmd consumes `^&` and passes a literal `&` to the program.
//
// It is applied only when Go will hand the token over bare -- no space, tab or
// quote. That is load-bearing in both directions: inside the quotes Go adds, cmd
// does not process caret at all, so escaping a token with a space would leave a
// stray `^` in the value; and a token with a space is already safe, because cmd
// treats metacharacters inside quotes as literal.
//
// Operator tokens are returned untouched, so a script that needs a shell keeps
// working: `net user x p /add && net localgroup Administrators x /add` depends on
// that `&&` reaching cmd as an operator. A token containing none of the
// metacharacters is untouched too, so an ordinary path is byte-identical to what
// it was before.
func protectCmdToken(tok string) string {
	if tok == "" || isCmdOperator(tok) {
		return tok
	}
	if !strings.ContainsAny(tok, `&|<>^`) {
		return tok
	}
	// Go quotes an argument containing a space, tab or quote, and inside those
	// quotes cmd does not process caret. Such a token needs no escaping.
	if strings.ContainsAny(tok, " \t\"") {
		return tok
	}
	var b strings.Builder
	b.Grow(len(tok) + 4)
	for i := 0; i < len(tok); i++ {
		if strings.IndexByte(`&|<>^`, tok[i]) >= 0 {
			b.WriteByte('^')
		}
		b.WriteByte(tok[i])
	}
	return b.String()
}

// sedDeleteScript builds the `sed -i` invocation that drops the line matching
// text. A `\#...#` address keeps path slashes literal, where a `/`-delimited one
// would end at the first slash inside the pattern and silently match nothing.
func sedDeleteScript(path, text string) string {
	return fmt.Sprintf("sed -i %s %s", shellQuotePOSIX(`\#`+escapeSedBRE(text)+`#d`), path)
}

// escapeSedBRE escapes the characters that would otherwise change the meaning of
// a BRE address. `#` is escaped because it delimits the address, and `|` is not,
// because `\|` inside a pattern means alternation rather than a literal bar.
func escapeSedBRE(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '.', '*', '[', ']', '^', '$', '\\', '#':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
