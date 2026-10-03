package api

import (
	"bufio"
	"fmt"
	"net"
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
// headerSafeFilename.
//
// What was measured, by speaking raw HTTP to a real server rather than reading
// the value back from a ResponseRecorder:
//
//   - A double quote does break out. The wire carried FOUR quotes instead of
//     two, producing a second filename= parameter the browser can pick up.
//     That is filename spoofing inside one header, not a new header.
//
//   - CR or LF does NOT reach the wire. net/http replaces them with spaces when
//     it writes the response, so "X-Injected: 1" lands inside the filename value
//     rather than becoming a header of its own. Response-splitting was the
//     theory; it is not achievable through this path. An earlier revision of
//     this comment claimed otherwise, based on reading the value back from a
//     ResponseRecorder -- which stores whatever was Set() and does not sanitise,
//     so it cannot answer this question.
//
// The fix is still right: the handler was the only one of the three that skipped
// the helper, the quote breakout is real, and the sanitised name is what the
// operator should see either way. The severity is "filename spoofing", not
// "response splitting".
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

// The quote breakout is asserted against the bytes on the wire, not the value
// read back from a ResponseRecorder. The recorder stores whatever was Set(), so
// it cannot distinguish "the header was written" from "the header was written
// and then sanitised by the server" -- and net/http does sanitise CR and LF.
func TestHiveHeaderQuoteBreakoutOnTheWire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The shape the handler used before the fix: the name concatenated raw.
		raw := `a"; filename="evil.exe`
		w.Header().Set("Content-Disposition", `attachment; filename="`+raw+`.hive"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	got := readRawHeader(t, srv.URL, "Content-Disposition")
	if n := strings.Count(got, `"`); n != 4 {
		t.Fatalf("the probe did not reproduce the breakout: %q has %d quotes, expected 4", got, n)
	}

	// And the fixed shape must not.
	safe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := headerSafeFilename(`a"; filename="evil.exe`, "SYSTEM.hive")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer safe.Close()

	got = readRawHeader(t, safe.URL, "Content-Disposition")
	if n := strings.Count(got, `"`); n != 2 {
		t.Errorf("the sanitised header has %d quotes, want 2: %q", n, got)
	}
	// The inert text may survive inside the single quoted name -- stripping it
	// would mean guessing at intent, and it cannot become a parameter without a
	// quote. The property that matters is that the value is ONE quoted string, so
	// there is no second filename= for a browser to prefer.
	if got != `attachment; filename="a; filename=evil.exe"` {
		t.Errorf("the sanitised value is not a single quoted filename: %q", got)
	}
}

// readRawHeader fetches u over a raw TCP connection and returns the named header
// value exactly as it appeared on the wire.
func readRawHeader(t *testing.T, u, header string) string {
	t.Helper()
	addr := strings.TrimPrefix(u, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", addr)
	br := bufio.NewReader(conn)
	prefix := header + ": "
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return ""
		}
		l := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(l, prefix) {
			return strings.TrimPrefix(l, prefix)
		}
		if l == "" {
			return ""
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
