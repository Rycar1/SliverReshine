package api

import (
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
)

// DefaultBasicAuthRealm is shown by browsers in the credential prompt. It is
// deliberately generic: the realm string is echoed back in the 401 response and
// therefore ends up in scanner fingerprints and asset-engine records, so it must
// not name the product.
const DefaultBasicAuthRealm = "Restricted"

// BasicAuth holds the credentials guarding the console.
//
// The console has exactly one account. The browser's login prompt, the Settings
// panel, and the credential file on disk all read and write this one record, so
// they cannot drift apart: whatever the operator logs in with is what the
// console stores and what the next restart asks for again. Keeping a second
// copy anywhere would eventually disagree with this one, and the failure mode
// of that disagreement is being locked out of your own console.
type BasicAuth struct {
	User   string
	Pass   string
	Realm  string
	Exempt []string

	// Persist, when set, is called with the new credentials after a runtime
	// change so the caller can write them somewhere durable. A failure is
	// surfaced to the operator but does not roll back the in-memory change:
	// reverting would leave the console on the old password immediately after
	// telling the operator the new one took effect.
	Persist func(user, pass string) error

	// mu guards User and Pass. They are read on every request rather than
	// captured when the middleware is built, so a change made from the Settings
	// panel applies to the very next request with no restart and no need to
	// rewrap the whole route tree.
	mu sync.RWMutex
}

// Enabled reports whether credentials were configured.
//
// An empty username means "not configured" rather than "the username is empty",
// so a missing flag can never silently become an authentication bypass that
// accepts a blank credential.
func (b *BasicAuth) Enabled() bool {
	if b == nil {
		return false
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.User != ""
}

// Credentials returns a consistent snapshot of the current account.
func (b *BasicAuth) Credentials() (user, pass string) {
	if b == nil {
		return "", ""
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.User, b.Pass
}

// SetCredentials replaces the console account and persists it when a hook is
// configured. The in-memory change is applied first so the operator can never
// be left on a password the console has already rejected.
func (b *BasicAuth) SetCredentials(user, pass string) error {
	if b == nil {
		return errors.New("basic auth is not configured")
	}
	b.mu.Lock()
	b.User, b.Pass = user, pass
	persist := b.Persist
	b.mu.Unlock()

	// Called without the lock held: the hook writes to disk, and holding the
	// lock across I/O would stall every in-flight request.
	if persist != nil {
		return persist(user, pass)
	}
	return nil
}

// basicAuth wraps next so that every request must carry valid credentials,
// except for exempt prefixes.
//
// The response is intentionally indistinguishable from any other password-
// protected service: a bare 401 with a generic realm and no server banner. Asset
// engines and internet-wide scanners key off distinctive headers and error
// bodies, so nothing here identifies the software behind it. In particular the
// terminal WebSocket endpoint is covered too, which matters because upgrading a
// socket is the usual way to slip past a front-end-only check.
func basicAuth(cfg *BasicAuth, next http.Handler) http.Handler {
	if cfg == nil {
		return next
	}
	realm := cfg.Realm
	if realm == "" {
		realm = DefaultBasicAuthRealm
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass := cfg.Credentials()
		if user == "" {
			// Checked per request rather than when the middleware was built, so
			// enabling auth on a running console takes effect immediately.
			next.ServeHTTP(w, r)
			return
		}

		for _, p := range cfg.Exempt {
			if p != "" && strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}

		// Compare against the encoded form so the comparison length is fixed by
		// the header we received, not by the secret. subtle.ConstantTimeCompare
		// only guarantees constant time for equal-length inputs.
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))

		ok := false
		if got := r.Header.Get("Authorization"); strings.HasPrefix(got, "Basic ") {
			ok = subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
		}
		if !ok {
			// Suppress anything that could advertise the stack. Go sets no
			// Server header by default, but be explicit so a reverse proxy or
			// future handler cannot leak one through this path.
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Del("Server")
			w.Header().Del("X-Powered-By")
			w.WriteHeader(http.StatusUnauthorized)
			// Plain text, no HTML: an empty body would look like a broken
			// service, and a styled error page is a fingerprint.
			_, _ = w.Write([]byte("401 Unauthorized\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
