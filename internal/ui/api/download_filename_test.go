package api

import (
	"strings"
	"testing"
)

// The filename in a Content-Disposition header comes from the target, and it is
// emitted inside a quoted string. A quote, a backslash, or a newline in it would
// end that string early and let the rest be read as another header -- which is a
// response-splitting primitive reachable by naming a file. These tests pin the
// reduction.

func TestHeaderSafeFilenameUsesOnlyTheLastElement(t *testing.T) {
	cases := map[string]string{
		`C:\Windows\System32\config\SAM`: "SAM",
		`/etc/shadow`:                    "shadow",
		`../../../etc/passwd`:            "passwd",
		`..\..\..\Windows\win.ini`:       "win.ini",
		`/var/log/../../etc/hosts`:       "hosts",
		`plain.txt`:                      "plain.txt",
	}
	for in, want := range cases {
		if got := headerSafeFilename(in, "unused"); got != want {
			t.Errorf("headerSafeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeaderSafeFilenameStripsHeaderMetacharacters(t *testing.T) {
	cases := []string{
		`evil".exe`,
		`back\slash.txt`,
		"new\rline.txt",
		"new\nline.txt",
		"nul\x00byte.txt",
		`a"; DROP: x.txt`,
	}
	for _, in := range cases {
		got := headerSafeFilename(in, "unused")
		if strings.ContainsAny(got, "\"\\\r\n\x00") {
			t.Errorf("headerSafeFilename(%q) = %q, which still contains a header metacharacter", in, got)
		}
	}
}

// A name made entirely of filtered characters would leave an empty header value,
// which some browsers reject; the fallback keeps the download nameable.
func TestHeaderSafeFilenameFallsBackWhenEverythingIsStripped(t *testing.T) {
	for _, in := range []string{`"`, `\`, "\r\n", `"""`, ""} {
		got := headerSafeFilename(in, "")
		if got == "" {
			t.Errorf("headerSafeFilename(%q) returned an empty name", in)
		}
	}
}

// The path is the fallback when the RPC reports no name of its own, which is the
// case for a download the server answered without a Path field.
func TestHeaderSafeFilenameFallsBackToPath(t *testing.T) {
	if got := headerSafeFilename("", `/tmp/report.pdf`); got != "report.pdf" {
		t.Errorf("= %q, want report.pdf", got)
	}
}

// The header value must be safe when it reaches a real response, not just when
// the helper is called directly.
func TestDownloadHeaderIsNotSplittable(t *testing.T) {
	h := make(map[string][]string)
	name := headerSafeFilename("evil\";\r\nX-Injected: 1", "/tmp/x")
	h["Content-Disposition"] = []string{`attachment; filename="` + name + `"`}

	value := h["Content-Disposition"][0]
	if strings.ContainsAny(value, "\r\n") {
		t.Fatalf("the header value contains a line break: %q", value)
	}
	// Exactly two quotes: the opening and closing of the filename.
	if got := strings.Count(value, `"`); got != 2 {
		t.Errorf("the header value has %d quotes, want 2: %q", got, value)
	}
	// The injected text may survive as inert characters inside the quoted name --
	// that is harmless, and stripping it would mean guessing at intent. What
	// matters is that it cannot become a header, which is the line-break check
	// above: without CR or LF there is no way to end the field, so the whole
	// thing stays one filename. Asserting its absence would be testing the wrong
	// property and would make this test pass for the wrong reason.
	if strings.ContainsAny(value, "\r\n") {
		t.Errorf("a header continuation survived: %q", value)
	}
}
