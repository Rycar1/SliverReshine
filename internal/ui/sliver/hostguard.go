package sliver

import (
	"fmt"
	"net"
	"strings"
)

// This file holds the validator for the one operator-supplied value that reaches
// a command template as a URL component: the host.
//
// It exists because there was no validation at all. The host field went from an
// HTTP request body straight into
//
//	fmt.Sprintf("http://%s:%d%s", host, port, path)
//
// and that URL was then interpolated into a delivery template. Only one of the
// templates quotes it -- curl's does not quote at all -- so a host containing a
// shell metacharacter became a second command on the target:
//
//	127.0.0.1; echo INJECTED            ran the echo
//	127.0.0.1 -o /tmp/OVERRIDE          argument injection into curl
//	a'; Write-Output PWNED; '           injected into the PowerShell template
//	a'); print('PWNED'); os.system('id')  compiled as live Python
//
// The same field also becomes the C2 callback address, so a typo was silently a
// different command as well as a wrong callback. Both halves are why this is a
// validator rather than a quoting change: the value has to be a host, and only a
// host can be trusted in any of those positions.
//
// The allowlist is the same shape as nameguard.go's: the strictest set that still
// accepts everything legitimate. A DNS name, an IPv4 literal, an IPv6 literal and
// an optional :port all pass. Anything containing a character that means
// something to a shell, to cmd.exe, to a URL parser, or to a filesystem does not.

// maxHostLen bounds the host. The longest legal DNS name is 253 characters, and
// a bracketed IPv6 literal plus a port is well under this.
const maxHostLen = 255

// validateHost checks that s can be used as the host component of a URL.
//
// It returns a sentence naming the offending character, because the operator is
// the one who has to fix it and "invalid host" alone does not say what to change.
func validateHost(s string) error {
	if s == "" {
		return fmt.Errorf("a host reachable from the target is required")
	}
	if len(s) > maxHostLen {
		return fmt.Errorf("host is too long (%d characters, limit %d)", len(s), maxHostLen)
	}
	// A bracketed value is an IPv6 literal. It is handled first and strictly,
	// because net.ParseIP does not accept brackets, and a loose fall-through would
	// strip them and then accept a malformed value.
	if strings.HasPrefix(s, "[") {
		end := strings.IndexByte(s, ']')
		if end < 0 {
			return fmt.Errorf("host %q has an unclosed '['; write an IPv6 address as [::1]", s)
		}
		if net.ParseIP(s[1:end]) == nil {
			return fmt.Errorf("host %q is not a valid IPv6 address", s)
		}
		if s[end+1:] != "" {
			return fmt.Errorf("host %q has trailing text after the bracketed address", s)
		}
		return nil
	}

	// A port is deliberately rejected. The caller supplies the port separately
	// (WebDeliveryRequest.Port, JobView.Port) and formats it into the URL itself,
	// so a port here would be applied twice -- `host:443` would become
	// `http://host:443:8443/path`, a URL that cannot resolve. Rejecting it turns a
	// silent misconfiguration into a message that says what to do.
	if strings.Contains(s, ":") {
		if net.ParseIP(s) != nil {
			// A bare IPv6 literal such as ::1 or fe80::1. No port can be
			// represented without brackets, so the whole string is the host.
			return nil
		}
		return fmt.Errorf(
			"host %q must not contain a port: put the port in the port field, "+
				"or write an IPv6 address in brackets as in [::1]", s)
	}
	if s == "" {
		return fmt.Errorf("host may not be empty")
	}

	// An IP literal is checked as an IP, so an IPv6 address full of colons is not
	// rejected by the character rules below.
	if ip := net.ParseIP(s); ip != nil {
		return nil
	}

	if len(s) > maxHostLen {
		return fmt.Errorf("host is too long")
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			// Allowed. Underscore is not legal in a hostname but appears in real
			// internal names and in Windows machine names, which are commonly
			// used as the target-visible address here.
		default:
			return fmt.Errorf(
				"host may not contain %q: it is interpolated into the delivery command and "+
					"into the callback address, so only a name or IP address is accepted", r)
		}
	}

	// A leading or trailing dot or dash is not a host. Catching it here means a
	// typo is reported rather than producing a URL that cannot resolve.
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") ||
		strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
		return fmt.Errorf("host %q may not begin or end with a dot or dash", s)
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("host %q contains an empty label", s)
	}

	return nil
}

// hostForURL renders a validated host into the host part of a URL.
//
// A bare IPv6 literal has to be bracketed or the colons are read as a port
// separator: `http://::1:8443/` does not parse, while `http://[::1]:8443/` does.
// validateHost accepts both bracketed and bare literals, so the brackets are
// added here, at the one place that knows a URL is being built.
func hostForURL(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

// validatePortString checks the port half of a host:port value.
func validatePortString(p string) error {
	if p == "" {
		return fmt.Errorf("host has a trailing colon with no port")
	}
	n := 0
	for _, r := range p {
		if r < '0' || r > '9' {
			return fmt.Errorf("host port %q may only contain digits", p)
		}
		n = n*10 + int(r-'0')
		if n > 65535 {
			return fmt.Errorf("host port %q is out of range", p)
		}
	}
	if n == 0 {
		return fmt.Errorf("host port may not be zero")
	}
	return nil
}
