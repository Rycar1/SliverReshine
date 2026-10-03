package api

import (
	"net/http"
	"strings"
)

// This file closes the CSRF hole the audit found.
//
// The console authenticates with HTTP Basic, and the browser replays those
// credentials on every request to the origin automatically. That makes plain
// "credentials: include" cross-origin requests dangerous here in a way the
// browser's same-origin policy does not cover: the policy stops Mallory from
// *reading* the response, but the request has already been delivered, and every
// mutating endpoint acts on it. A page on any origin could therefore generate a
// payload, kill a session, or run a command on a target.
//
// Two checks close it, and both have to pass:
//
//  1. Content-Type must be application/json. This is the load-bearing one. A
//     cross-origin <form> or fetch() can only send text/plain,
//     application/x-www-form-urlencoded or multipart/form-data without first
//     earning a CORS preflight, and the console answers no preflight for a
//     mutating method. Requiring a type that simple requests cannot set means
//     the crossing request never reaches a handler.
//
//  2. Origin, when present, must match the request's own host. This catches the
//     request that does earn a preflight, and it is what actually rejects a
//     cross-site form post rather than merely failing to parse it.
//
// Origin is only *required* to match when it is present. A missing Origin is
// normal and must stay allowed: curl, the PowerShell client used in this repo's
// own docs, and the Go tests send no Origin at all, and refusing them would
// break every scripted deployment to close a browser-only hole.

// csrfExemptPrefixes lists the path prefixes that may be reached without the
// checks above.
//
// The terminal WebSocket is the only one, and only because the WebSocket
// handshake performs its own origin check before the connection is upgraded --
// see handleTerminalWS. Everything under /api is covered, including the reads:
// a cross-origin read leaks the session list, and there is no cost to protecting
// it.
//
// This returns a fresh slice on every call instead of reading a package-level
// map. The allowlist is a security boundary, and a map in package scope is one
// stray assignment away from being widened by any code in this package; a
// function can only be changed by editing it.
func csrfExemptPrefixes() []string {
	return []string{"/ws/sessions/"}
}

// withSecurityHeaders sets the response headers a browser needs to treat this
// console as an application rather than as a document.
//
// None of these change what the console can do; they close the ways a browser
// can be talked into doing something on the operator's behalf. The console had
// none of them, while basicAuth already went out of its way to remove the
// Server banner -- so the intent was there and the set was incomplete.
//
// The CSP is deliberately narrow rather than a full policy: the frontend is a
// Vite bundle with inline styles and a WebSocket terminal, and a policy that
// broke either would be turned off rather than fixed. frame-ancestors and
// form-action are the two that matter here and neither can break the app.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Stops a browser from re-interpreting a JSON body as HTML, which is how
		// a reflected value becomes script.
		h.Set("X-Content-Type-Options", "nosniff")
		// A console is not a document anyone should be able to frame. This is the
		// clickjacking control, and it is the reason the header is not optional.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'; form-action 'self'")
		// Do not leak the console's URL to any third party the page touches.
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// withCSRF guards state-changing requests against cross-site forgery.
func withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Safe methods do not change state, so they are not the attack. The
		// mutating methods are what a forged request would use.
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		for _, prefix := range csrfExemptPrefixes() {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
		}

		if !hasJSONContentType(r.Header.Get("Content-Type")) {
			writeErr(w, http.StatusUnsupportedMediaType,
				"Content-Type must be application/json; the console refuses cross-site form posts")
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			if !sameOrigin(origin, r.Host) {
				writeErr(w, http.StatusForbidden,
					"cross-origin request refused")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// isSafeMethod reports whether a method is defined as safe by RFC 9110.
//
// TRACE is deliberately not treated as safe: it is a diagnostic method no client
// of this console uses, and treating an unused method as safe is how a
// future endpoint ends up unguarded.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// hasJSONContentType reports whether the header names application/json as the
// media type.
//
// Parsing is by hand and on the media type only. A browser cannot be stopped
// from adding a charset parameter, and the parameters are not part of the check.
// The comparison is case-insensitive because media types are.
func hasJSONContentType(header string) bool {
	if header == "" {
		return false
	}
	mediaType := header
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = mediaType[:i]
	}
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/json")
}

// sameOrigin reports whether an Origin header value names the host the request
// was addressed to.
//
// The comparison is against r.Host rather than a configured allowlist: the
// console is deployed as a single binary on an address the operator chooses, so
// there is no fixed origin to list. Matching the request's own Host is what
// makes this correct behind a reverse proxy too, where the proxy sets Host to
// the public name.
//
// A scheme mismatch (http Origin against an https deployment) is refused, which
// is the intent: the two are different origins even on the same host.
func sameOrigin(origin, host string) bool {
	// Origin is "scheme://host[:port]"; everything after the authority is not
	// part of the comparison.
	rest := origin
	i := strings.Index(rest, "://")
	if i < 0 {
		// A malformed or relative Origin cannot be verified, so it is refused.
		return false
	}
	rest = rest[i+3:]

	// Strip any path. A well-formed Origin has none, but a hand-crafted header
	// might, and comparing the authority is what was asked for.
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}

	// Case-insensitive: host names are, and browsers do not normalise the case
	// of the header.
	return strings.EqualFold(rest, host)
}
