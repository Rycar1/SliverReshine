package sliver

import (
	"strings"
	"testing"
)

// validateHost guards the one operator-supplied value that reaches a delivery
// command as a URL component. Before it existed, the host field went from an
// HTTP body straight into fmt.Sprintf("http://%s:%d%s", ...) and then into a
// template that -- for curl -- does not quote at all, so a host containing a
// shell metacharacter became a second command on the target.
func TestValidateHostRejectsInjection(t *testing.T) {
	for _, host := range []string{
		`127.0.0.1; echo INJECTED`,
		`127.0.0.1 -o /tmp/OVERRIDE`,
		`127.0.0.1$(id)`,
		"127.0.0.1`id`",
		`a'; Write-Output PWNED; '`,
		`a'); print('PWNED'); os.system('id'); ('`,
		`evil.example/x" & echo INJECTED & rem "`,
		`host|whoami`,
		`host&whoami`,
		`host>file`,
		`host<file`,
		`host^whoami`,
		"host\nX-Injected: 1",
		"host\tfoo",
		`host name`,
		`http://host`,
		`host/path`,
		`user@host`,
		`[::1`,
		`host:`,
		`host:0`,
		`host:99999`,
		`host:8o80`,
		// A port belongs in the port field: the callers format it into the URL
		// themselves, so one here would be applied twice.
		`c2.example.com:8443`,
		`127.0.0.1:8080`,
		`[::1]:8443`,
		`.host`,
		`host.`,
		`-host`,
		`host-`,
		`a..b`,
		``,
	} {
		if err := validateHost(host); err == nil {
			t.Errorf("validateHost(%q) accepted a value that reaches a command template", host)
		}
	}
}

// Everything legitimate has to keep working, or the fix is a regression.
func TestValidateHostAcceptsRealAddresses(t *testing.T) {
	for _, host := range []string{
		`127.0.0.1`,
		`10.0.0.5`,
		`0.0.0.0`,
		`c2.example.com`,
		`sub.domain.example.com`,
		`HOSTNAME`,
		`win-22ugj5v1hu6`,
		`my_host`,
		`a-b.c-d.example`,
		`192.168.190.142`,
		`::1`,
		`fe80::1`,
		`[::1]`,
		`[fe80::1]`,
	} {
		if err := validateHost(host); err != nil {
			t.Errorf("validateHost(%q) rejected a usable address: %v", host, err)
		}
	}
}

// The error has to name the character, because the operator is the one who has
// to fix their input.
func TestValidateHostNamesTheOffendingCharacter(t *testing.T) {
	err := validateHost(`127.0.0.1; id`)
	if err == nil {
		t.Fatal("expected a rejection")
	}
	if !strings.Contains(err.Error(), ";") {
		t.Errorf("error does not name the offending character: %v", err)
	}
}

// The callback address is built from the same field and written into the implant
// profile, so it is validated there too -- and a listener's own domain is checked
// even though the console did not create it.
func TestOneLinerRejectsAHostileHost(t *testing.T) {
	job := &JobView{ID: 1, Name: "http", Port: 8443, Domains: []string{"c2.example.com"}}

	for _, bad := range []string{`1.2.3.4; id`, `1.2.3.4&whoami`, "1.2.3.4 -o /tmp/x"} {
		if _, err := callbackHostForJob(job, bad, ""); err == nil {
			t.Errorf("callbackHostForJob accepted %q", bad)
		}
	}

	// A hostile domain on the listener itself is skipped, not used.
	hostile := &JobView{ID: 1, Name: "http", Port: 8443, Domains: []string{`x; id`, `c2.example.com`}}
	got, err := callbackHostForJob(hostile, "", "")
	if err != nil {
		t.Fatalf("callbackHostForJob: %v", err)
	}
	if got != "c2.example.com" {
		t.Errorf("= %q, want the usable domain rather than the hostile one", got)
	}
}

// A bare IPv6 literal has to be bracketed when it is formatted into a URL, or the
// colons are read as a port separator.
func TestHostForURLBracketsIPv6(t *testing.T) {
	tests := map[string]string{
		`127.0.0.1`:      `127.0.0.1`,
		`c2.example.com`: `c2.example.com`,
		`::1`:            `[::1]`,
		`fe80::1`:        `[fe80::1]`,
		`[::1]`:          `[::1]`,
	}
	for host, want := range tests {
		if got := hostForURL(host); got != want {
			t.Errorf("hostForURL(%q) = %q, want %q", host, got, want)
		}
	}
}

// The address that reaches the target has to be a URL, not just a host.
func TestC2AddressIsAUsableURL(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string
	}{
		{`c2.example.com`, `http://c2.example.com:8443`},
		{`127.0.0.1`, `http://127.0.0.1:8443`},
		{`::1`, `http://[::1]:8443`},
	} {
		job := &JobView{ID: 1, Name: "http", Port: 8443}
		got, err := c2AddressForJob(job, tc.host)
		if err != nil {
			t.Errorf("c2AddressForJob(%q): %v", tc.host, err)
			continue
		}
		if got != tc.want {
			t.Errorf("c2AddressForJob(%q) = %q, want %q", tc.host, got, tc.want)
		}
		// A URL with an unbracketed IPv6 literal does not parse at all.
		if strings.Count(got, ":") > 2 && !strings.Contains(got, "[") {
			t.Errorf("address %q has unbracketed IPv6 colons", got)
		}
	}
}

// validatePortString is the second half of the same guard as validateHost: the
// port is interpolated into the same URL, so a non-digit there is the same
// injection with a different shape.
func TestValidatePortString(t *testing.T) {
	for _, p := range []string{"1", "80", "443", "8080", "65535", "00080"} {
		if err := validatePortString(p); err != nil {
			t.Errorf("validatePortString(%q) rejected a valid port: %v", p, err)
		}
	}

	rejected := map[string]string{
		"":       "a trailing colon with no port",
		"0":      "zero",
		"00":     "zero written with padding",
		"65536":  "one past the top of the range",
		"99999":  "well past the range",
		"80a":    "a non-digit",
		"8 0":    "a space",
		"-80":    "a sign",
		"80; id": "a shell separator",
		"$PORT":  "a shell expansion",
		"8.0":    "a dot",
	}
	for p, why := range rejected {
		if err := validatePortString(p); err == nil {
			t.Errorf("validatePortString(%q) accepted it (%s)", p, why)
		}
	}
}
