package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every download handler that puts a name into Content-Disposition must sanitise
// it. The name is not trusted input at these sites: two of them take the
// target's own path, and the hive handler takes a value straight from the
// request body.
//
// The regression this guards: handleRegistryHive concatenated req.RequestedHive
// into the header raw, while handlers_ops.go ran its two names through
// headerSafeFilename. A value containing CR or LF therefore put those bytes on
// the wire inside the header value, and a value containing a double quote closed
// the filename early and produced a second filename= parameter.
func TestDownloadNamesInHeadersAreSanitised(t *testing.T) {
	// Values that would break out of `filename="..."` if concatenated raw.
	hostile := []string{
		`a"; filename="evil.exe`,
		"a\r\nX-Injected: 1",
		"a\nX-Injected: 1",
		"nul\x00byte",
		`back\slash`,
		"tab\there",
	}

	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			// The hive handler's shape, after the fix.
			hiveName := headerSafeFilename(name, "SYSTEM.hive")
			if hiveName == "" {
				t.Fatal("the sanitised name is empty")
			}

			rec := httptest.NewRecorder()
			rec.Header().Set("Content-Disposition", `attachment; filename="`+hiveName+`"`)
			rec.WriteHeader(http.StatusOK)

			res := rec.Result()
			value := res.Header.Get("Content-Disposition")

			if strings.ContainsAny(value, "\r\n") {
				t.Errorf("a line break reached the header value: %q", value)
			}
			// Tab is deliberately not in this set. It is legal inside an HTTP
			// quoted-string and cannot end a header field, so filtering it would be
			// cosmetic rather than a fix. The characters that matter are the ones
			// that end the quoted string (" and \\) or start a new line (CR, LF).
			if strings.ContainsAny(hiveName, "\"\\\r\n\x00") {
				t.Errorf("the sanitised name still contains a header metacharacter: %q", hiveName)
			}
			// Exactly the two quotes of the quoted-string; a third means the name
			// closed it early and opened another parameter.
			if got := strings.Count(value, `"`); got != 2 {
				t.Errorf("the header value has %d quotes, want 2: %q", got, value)
			}
			if res.Header.Get("X-Injected") != "" {
				t.Errorf("the injected header appeared: %q", res.Header.Get("X-Injected"))
			}
		})
	}
}

// The hive name falls back to the root hive when nothing usable is left, so a
// download is still nameable.
func TestHiveNameFallsBackToTheRootHive(t *testing.T) {
	for _, requested := range []string{`"`, `\`, "\r\n", ""} {
		got := headerSafeFilename(requested, "SYSTEM.hive")
		if got == "" {
			t.Errorf("requestedHive=%q produced an empty filename", requested)
		}
	}
}

// A path in requestedHive must not survive as a path in the download name.
func TestHiveNameIsOnePathElement(t *testing.T) {
	for _, in := range []string{
		`../../etc/passwd`,
		`..\..\Windows\win.ini`,
		`/etc/shadow`,
		`C:\Windows\System32\config\SAM`,
	} {
		got := headerSafeFilename(in, "SYSTEM.hive")
		if strings.ContainsAny(got, `/\`) {
			t.Errorf("headerSafeFilename(%q) = %q still contains a path separator", in, got)
		}
	}
}
