package api

import "testing"

// The ?shell= value ends up in an exec on a remote host, so what it accepts and
// what it refuses is a security-relevant decision, not a formatting detail.
// These tests pin both directions.

func TestShellPathForAcceptsTheAdvertisedNames(t *testing.T) {
	cases := map[string]string{
		"cmd":        `C:\Windows\System32\cmd.exe`,
		"CMD":        `C:\Windows\System32\cmd.exe`,
		"powershell": `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`,
		"pwsh":       `C:\Program Files\PowerShell\7\pwsh.exe`,
		"sh":         "/bin/sh",
		"bash":       "/bin/bash",
	}
	for in, want := range cases {
		if got := shellPathFor(in); got != want {
			t.Errorf("shellPathFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// An empty value means "let the implant choose", which is the behaviour every
// caller had before the parameter existed. It must not become an error.
func TestShellPathForEmptyMeansDefault(t *testing.T) {
	for _, in := range []string{"", "   ", "\t"} {
		if got := shellPathFor(in); got != "" {
			t.Errorf("shellPathFor(%q) = %q, want empty (implant chooses)", in, got)
		}
	}
}

// A name that is not advertised is refused. Passing it through would make the
// query string a general "run this program on the target" interface, which the
// console already exposes deliberately elsewhere; doing it by accident here
// would be worse than the convenience is worth.
func TestShellPathForRefusesUnknownNames(t *testing.T) {
	for _, in := range []string{
		"nc",
		"evil",
		"cmd.exe",         // the real name, but not one we advertise
		"../../../bin/sh", // traversal
		"cmd; rm -rf /",   // injection-shaped
		"cmd && whoami",   //
		"|powershell",     //
		"./relative",      // relative paths are ambiguous on the target
		"relative\\cmd.exe",
	} {
		if got := shellPathFor(in); got != "" && !isAbsolutePath(got) {
			t.Errorf("shellPathFor(%q) = %q, want refusal", in, got)
		}
	}
}

// An absolute path is allowed even when unlisted, because a shell outside the
// usual locations is a real case -- portable PowerShell, SysWOW64, a hardened
// image -- and refusing it would send the operator back to guessing.
func TestShellPathForAllowsAbsolutePaths(t *testing.T) {
	cases := []string{
		`C:\Tools\pwsh\pwsh.exe`,
		`C:\Windows\SysWOW64\cmd.exe`,
		`D:\portable\nushell.exe`,
		`/usr/local/bin/fish`,
		`/opt/shells/zsh`,
		`\\server\share\shell.exe`,
	}
	for _, in := range cases {
		if got := shellPathFor(in); got != in {
			t.Errorf("shellPathFor(%q) = %q, want it passed through", in, got)
		}
	}
}

func TestIsAbsolutePath(t *testing.T) {
	// The property that matters is not "does this look like a normal path" but
	// "can it be resolved against the implant's working directory". Anything
	// rooted cannot, which is the only thing this guard is for.
	//
	// "//" and "\" are here deliberately. They are degenerate rather than
	// useful, but both are rooted -- POSIX treats a leading "//" as
	// implementation-defined yet still absolute, and Windows reads a leading
	// backslash as rooted -- so refusing them would be refusing something the
	// guard has no reason to refuse. The first draft of this test expected them
	// to be relative and was wrong.
	abs := []string{
		"/bin/sh", `C:\x`, "C:/x", `\\srv\share`, "/",
		"//",
		// UNC and only UNC on the Windows side. A single leading backslash is
		// root-relative (rooted against the current *drive*), which the implant
		// never reports, so it is refused along with the other ambiguous forms
		// rather than listed here as absolute.
		`\\`,
	}
	rel := []string{
		"", "cmd", "./cmd", `..\cmd`, "bin/sh",
		// "C:cmd" is drive-relative, not rooted: Windows resolves it against the
		// current directory *on drive C*, which is exactly the ambiguity the
		// guard exists to avoid.
		`C:cmd`,
		// "\cmd" is root-relative -- rooted against the current *drive*, which
		// the implant does not report. Refusing it is deliberate rather than an
		// oversight: a path whose meaning depends on state we cannot see is not
		// one to accept from a query string.
		`\cmd`,
	}
	for _, p := range abs {
		if !isAbsolutePath(p) {
			t.Errorf("isAbsolutePath(%q) = false, want true", p)
		}
	}
	for _, p := range rel {
		if isAbsolutePath(p) {
			t.Errorf("isAbsolutePath(%q) = true, want false", p)
		}
	}
}
