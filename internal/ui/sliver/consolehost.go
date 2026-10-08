package sliver

import (
	"net"
	"strings"
)

// ConsoleHostFromHeader derives the address the operator used to reach this
// console, from the HTTP Host header of the request that asked for a command.
//
// It exists for one failure mode: a listener bound to 0.0.0.0 reports 0.0.0.0 as
// its only domain, and 0.0.0.0 is not an address any target can dial. A payload
// built from it builds fine, fetches fine and never checks in, and the console
// has nothing better to say than the wildcard it was given.
//
// The console does know one address that demonstrably routes to this host: the
// one in the browser's URL, which arrives here as the Host header. Using it as a
// fallback turns a silently dead one-liner into a working one.
//
// The result is deliberately conservative. "" means "no better guess than the
// listener's own address", and is returned for anything that says nothing about
// how a *target* reaches this host:
//
//   - an empty header;
//   - a wildcard (0.0.0.0, ::), which is the problem this solves, not an answer;
//   - a loopback address (127.0.0.1, ::1, localhost), which routes only from the
//     operator's own machine. Emitting it would hide the problem behind a value
//     that looks usable, which is worse than the visible wildcard.
//
// The value is validated with the same allowlist as operator input before it is
// returned, because a Host header is attacker-controlled: a request forged with
// "Host: evil; id" must not reach a delivery template. A value that fails
// validation is dropped rather than reported -- this is a derived default, not
// something the operator typed, so there is no input to correct.
func ConsoleHostFromHeader(hostHeader string) string {
	h := strings.TrimSpace(hostHeader)
	if h == "" {
		return ""
	}

	// Strip the port. SplitHostPort handles "example.com:8080" and "[::1]:8080";
	// a bare hostname or a bare IPv6 literal fails it and is used as-is.
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	h = strings.TrimSpace(h)
	if h == "" {
		return ""
	}

	// A wildcard or loopback address is not a better answer than the listener's
	// own address, for the reasons in the doc comment above.
	if h == "0.0.0.0" || h == "::" {
		return ""
	}
	// Shared with the one-liner's own host resolution, so a spelling that counts
	// as loopback in one place cannot count as a destination in the other.
	if isLoopbackHost(h) {
		return ""
	}

	// The same guard the operator's own host field goes through. See hostguard.go.
	if err := validateHost(h); err != nil {
		return ""
	}
	return h
}
