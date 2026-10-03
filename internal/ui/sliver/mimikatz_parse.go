package sliver

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParsedCredential is one secret recovered from a tool's console output.
type ParsedCredential struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Secret   string `json:"secret"`
	Kind     string `json:"kind"`   // "plaintext" | "ntlm" | "sha1"
	Source   string `json:"source"` // the command that produced it
}

// providerSections are the sub-blocks sekurlsa prints. Only these are treated
// as section headers: matching any "word :" line would swallow ordinary key and
// value pairs and split a credential block in half.
var providerSections = map[string]bool{
	"msv": true, "wdigest": true, "kerberos": true, "tspkg": true,
	"ssp": true, "credman": true, "livessp": true, "cloudap": true,
	"dpapi": true, "vault": true, "scecli": true, "kdcmail": true,
}

// placeholders are the values mimikatz prints when a field exists but holds
// nothing. Storing one would put a fake password in the vault that an operator
// might later spray across the estate.
var placeholders = map[string]bool{
	"": true, "(null)": true, "<null>": true, "null": true,
	"n.a.": true, "n.a": true, "(empty)": true, "none": true, "--": true,
}

func isPlaceholder(v string) bool {
	return placeholders[strings.ToLower(strings.TrimSpace(v))]
}

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// splitKV splits "Key : Value" on the first colon. The value may itself contain
// colons (vault target names do), so only the first one separates.
func splitKV(line string) (key, val string, ok bool) {
	i := strings.Index(line, ":")
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}

type parsedSecret struct {
	kind  string
	value string
}

// ParseMimikatz extracts credentials from mimikatz console output.
//
// It is deliberately tolerant. The tool prints several different shapes
// depending on the module, the Windows build, and whether a field was present
// at all, and an operator pasting output captured elsewhere cannot be asked to
// normalise it first. Anything unrecognised is skipped rather than guessed at.
func ParseMimikatz(text, source string) []ParsedCredential {
	// vault::cred prints a different shape from the sekurlsa dump: records are
	// delimited by "TargetName" and the secret sits in a hex "Credential"
	// blob rather than a named Password/NTLM field. It is parsed separately and
	// merged at the end, so neither shape has to compromise the other.
	vault := parseVaultCreds(text, source)

	var (
		out  []ParsedCredential
		seen = map[string]bool{}

		blockUser, blockDomain string

		secUser, secDomain string
		secrets            []parsedSecret
	)

	flush := func() {
		if len(secrets) == 0 {
			return
		}
		user := secUser
		if user == "" {
			// msv and friends routinely omit "* Username" for the machine
			// account and rely on the block header above.
			user = blockUser
		}
		domain := secDomain
		if domain == "" {
			domain = blockDomain
		}
		for _, s := range secrets {
			if user == "" {
				continue
			}
			key := strings.ToLower(user) + "|" + strings.ToLower(domain) + "|" + s.value
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ParsedCredential{
				Username: user,
				Domain:   domain,
				Secret:   s.value,
				Kind:     s.kind,
				Source:   source,
			})
		}
		secUser, secDomain, secrets = "", "", nil
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}

		// "* Key : Value" is a field inside the current section.
		if strings.HasPrefix(line, "*") {
			key, val, ok := splitKV(strings.TrimSpace(strings.TrimPrefix(line, "*")))
			if !ok || isPlaceholder(val) {
				continue
			}
			switch strings.ToLower(key) {
			case "username":
				secUser = val
			case "domain":
				secDomain = val
			case "password", "pass":
				secrets = append(secrets, parsedSecret{kind: "plaintext", value: val})
			case "ntlm":
				if isHex(val, 32) {
					secrets = append(secrets, parsedSecret{kind: "ntlm", value: val})
				}
			case "sha1":
				if isHex(val, 40) {
					secrets = append(secrets, parsedSecret{kind: "sha1", value: val})
				}
			}
			continue
		}

		key, val, ok := splitKV(line)
		if !ok {
			continue
		}

		// "msv :" opens a new provider section.
		if val == "" && providerSections[strings.ToLower(key)] {
			flush()
			continue
		}

		switch strings.ToLower(key) {
		case "authentication id", "rid", "target name":
			// Each of these starts a fresh record.
			flush()
		case "user name", "username":
			// sekurlsa::logonpasswords writes "User Name" in the record header and
			// "* Username" inside a provider section. sekurlsa::msv and other
			// modules emit the unspaced "Username". All three name the same thing.
			flush()
			// Reset the block context: a stale domain from the previous record
			// would be attached to every credential in this one.
			blockUser, blockDomain = val, ""
		case "domain":
			blockDomain = val
		case "user":
			// lsadump::sam writes a bare "User :" rather than "User Name :".
			secUser = val
		case "hash ntlm":
			if isHex(val, 32) {
				secrets = append(secrets, parsedSecret{kind: "ntlm", value: val})
			}
		}
	}
	flush()

	// vault::cred shares no field names with the sekurlsa shape, so it is added
	// without going through the block scanner. Deduplicate against what the main
	// pass already produced: one pasted capture can legitimately contain both.
	for _, v := range vault {
		key := strings.ToLower(v.Username) + "|" + strings.ToLower(v.Domain) + "|" + v.Secret
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}

	return out
}

// parseVaultCreds extracts Credential Manager entries from `vault::cred` output.
//
// The shape is one record per TargetName:
//
//	TargetName : MicrosoftAccount:user=a@b.com / <NULL>
//	UserName   : a@b.com
//	Type       : 1 - generic
//	Credential : 65 30 51 76 ...        <- hex, not text
//
// The secret is a hex-encoded byte string, so it is decoded rather than stored
// as-is: "65 30 51 76" left verbatim would put an unusable blob in the vault in
// place of the password.
func parseVaultCreds(text, source string) []ParsedCredential {
	var (
		out []ParsedCredential

		user string
		// hexSeen guards against a second UserName line in the same record
		// stealing ownership of the secret.
		hexSeen bool
	)

	emit := func(credit string, blob bool) {
		if credit == "" {
			return
		}
		// A secret with no username still matters -- an unnamed GitHub token is
		// usable. Rather than drop it, it is filed under the target it came
		// from, so it is still addressable and still dedupes.
		name := user
		if name == "" {
			name = "(vault)"
		}
		kind := "plaintext"
		if blob {
			kind = "vault-blob"
		}
		out = append(out, ParsedCredential{
			Username: name,
			Secret:   credit,
			Kind:     kind,
			Source:   source,
		})
	}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}

		key, val, ok := splitKV(line)
		if !ok {
			continue
		}

		switch strings.ToLower(key) {
		case "targetname":
			// A new record; the previous one is complete.
			user, hexSeen = "", false

		case "username":
			// Only meaningful before the Credential line, which is the last
			// field of a record: a later UserName belongs to the next record.
			//
			// mimikatz prints the literal "<NULL>" when the entry has no
			// associated account. Filing a live token under the username
			// "<NULL>" buries it: nobody looks up that account, and it
			// collides with every other unnamed entry in the vault.
			if !hexSeen && !isPlaceholder(val) {
				user = val
			}

		case "credential":
			hexSeen = true
			if isPlaceholder(val) {
				// "Credential :" with nothing after it is a stored entry whose
				// blob is empty -- common for persisted Microsoft accounts. The
				// username is still worth keeping as recon, with no secret.
				if user != "" {
					out = append(out, ParsedCredential{
						Username: user,
						Secret:   "",
						Kind:     "vault-reference",
						Source:   source,
					})
				}
				continue
			}
			if raw, ok := decodeHexBlob(val); ok {
				// Not every stored secret is a password. Credential Manager also
				// holds DPAPI blobs, certificate material and tokens, and those
				// decode to arbitrary bytes -- often not valid UTF-8. gRPC refuses
				// to marshal an invalid UTF-8 string and fails the WHOLE batch, so
				// a single blob would silently cost every other credential in the
				// run. Binary stays lossless by being stored hex-encoded instead.
				if utf8.Valid(raw) {
					emit(string(raw), false)
				} else {
					emit(hex.EncodeToString(raw), true)
				}
				continue
			}
			// Not a hex blob (some builds print it plainly); keep the text.
			emit(val, false)
		}
	}

	return out
}

// decodeHexBlob decodes a space-separated hex byte string such as "65 30 51 76".
// Trailing NULs are dropped, because mimikatz prints the whole fixed-size buffer
// and the padding is not part of the secret.
//
// The bytes are returned as-is rather than as a string: a vault entry can hold
// arbitrary binary, and the caller has to decide how to render it.
func decodeHexBlob(s string) ([]byte, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, false
	}
	b := make([]byte, 0, len(fields))
	for _, f := range fields {
		if !isHex(f, 2) {
			return nil, false
		}
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			return nil, false
		}
		b = append(b, byte(v))
	}
	b = bytes.TrimRight(b, "\x00")
	if len(b) == 0 {
		return nil, false
	}
	return b, true
}

// storeUsername folds the domain into the username.
//
// The credential vault has no domain column, so a bare "Administrator" would
// silently collide across every domain and machine on the estate. DOMAIN\user
// is the conventional rendering and keeps the two apart.
func storeUsername(domain, user string) string {
	if domain == "" || strings.Contains(user, `\`) {
		return user
	}
	return domain + `\` + user
}

func credDedupeKey(username, plaintext, hash string) string {
	secret := plaintext
	if secret == "" {
		secret = hash
	}
	return strings.ToLower(username) + "|" + secret
}
