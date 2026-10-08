package sliver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"sliverreshine/internal/ai"
)

// The AI collection assistant.
//
// This is the first feature built on internal/ai, and it is the one that most
// needs the read-only policy: it runs commands on a live target, and the model
// proposing them is not trusted. While the policy is on the arrangement is
//
//	model proposes one command -> CheckReadOnlyCommand approves it -> Execute
//
// with the approval step in the middle and nothing able to skip it. A model
// that invents "rm -rf /", or that is prompted into it by something it read on
// the target, gets a refusal recorded in the transcript and no spawn. Findings
// are written to the console's own vaults, never to the target.
//
// The policy can be turned off, deliberately, in the settings file (see
// config.AIConfig.ReadOnly). When it is off the check is skipped and the
// model's command runs as written; that is an operator's decision, not a
// default, and every run reports whether it was enforced.
//
// The run is bounded in three ways: a step ceiling, an output ceiling per step,
// and a transcript budget (see ai.Conversation). The ceiling is a backstop
// against a run that never terminates rather than a knob an operator tunes: a
// normal run ends when the model reports it is done.

// AICollectRequest is one collection run against a session.
type AICollectRequest struct {
	// SessionID is the live session to probe.
	SessionID string `json:"session_id"`
	// Objective narrows the run, e.g. "credential files under /home".
	Objective string `json:"objective"`
	// MaxSteps bounds how many commands the model may propose. Zero uses the
	// default; values above the ceiling are clamped rather than refused, so a
	// client cannot talk the console into an unbounded run.
	MaxSteps int `json:"max_steps"`
	// ReadOnly carries the deployment's read-only policy into the run. It is
	// a pointer so a caller that does not mention it keeps the policy rather
	// than silently turning it off; nil means on. The HTTP handler always
	// sets it from the server's setting, so a request body cannot use it to
	// bypass the policy.
	ReadOnly *bool `json:"read_only"`
	// DryRun plans without running anything on the target. The commands are
	// still policy-checked, so a plan the policy would refuse is visible before
	// it is ever attempted for real.
	DryRun bool `json:"dry_run"`
	// NoStore skips writing findings into the loot and credential vaults, for
	// an operator who wants the transcript but not the artefacts.
	NoStore bool `json:"no_store"`
	// FilterFindings runs a second model pass over the extraction phase's
	// findings before anything is filed, dropping entries that repeat an
	// earlier one and entries with no value. The second pass answers with
	// indices only, so it can remove a finding but never rewrite one.
	FilterFindings bool `json:"filter_findings"`
	// Progress receives events as the run proceeds, so a caller can show the
	// model's reasoning and each command as they happen rather than waiting
	// for the whole result. It is not part of the wire format: the HTTP
	// handler attaches it after decoding, and a body cannot set it.
	Progress AIProgress `json:"-"`
}

const (
	// DefaultAICollectSteps is what a run does when the caller does not say.
	// It is deliberately generous: a run ends when the model reports it is
	// done, and a low ceiling cuts a productive run short mid-harvest.
	DefaultAICollectSteps = 40
	// MaxAICollectSteps is the hard ceiling on one run. It is a backstop
	// against a run that never terminates rather than a knob to tune; the
	// read-only policy is what constrains what the model may do.
	MaxAICollectSteps = 100
	// maxAIStepOutput bounds how much of one command's output is kept in the
	// result and fed back to the model.
	maxAIStepOutput = 8 << 10
	// aiCollectCollection is the last-resort vault label, used only for a
	// finding the model did not name. Findings are normally filed under the
	// model's own label for them (see aiCollectLabel): the loot list shows a
	// credential's collection as its name, and a shared collection made every
	// harvest look alike.
	aiCollectCollection = "ai-collect"
)

// AICollectStep is one command the assistant proposed and what happened to it.
type AICollectStep struct {
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
	// Thinking is the model's own reasoning for this step, when the endpoint
	// returned any. It is shown beside the command so an operator can see the
	// model's working, not only its conclusion.
	Thinking string `json:"thinking,omitempty"`
	// Refused is set when the read-only policy rejected the command, with the
	// policy's reason in Refusal. Nothing was spawned.
	Refused bool   `json:"refused,omitempty"`
	Refusal string `json:"refusal,omitempty"`
	// Output is the combined stdout/stderr, truncated.
	Output string `json:"output,omitempty"`
	// Error is set when the command passed the policy but the target failed to
	// run it.
	Error  string `json:"error,omitempty"`
	Status uint32 `json:"status,omitempty"`
}

// AICollectFinding is one credential or loot candidate the assistant extracted.
type AICollectFinding struct {
	// Kind is "credential", "apikey" or "loot".
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	Username string `json:"username,omitempty"`
	Secret   string `json:"secret,omitempty"`
	Content  string `json:"content,omitempty"`
	Source   string `json:"source,omitempty"`
}

// AICollectStored records one artefact that was written to a vault.
type AICollectStored struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

// AICollectFiltered records one finding the second pass removed, so the
// operator can see what was dropped and why rather than only that the list
// shrank. Index is the finding's position in the extraction phase's output.
type AICollectFiltered struct {
	Index  int    `json:"index"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// AICollectResult is the whole run.
type AICollectResult struct {
	Steps    []AICollectStep    `json:"steps"`
	Findings []AICollectFinding `json:"findings"`
	Stored   []AICollectStored  `json:"stored"`
	// Skipped records findings that were not stored, and why. It is separate
	// from an error because one unwritable finding must not discard the rest.
	Skipped []string `json:"skipped"`
	// Filtered records the findings the second pass removed and why. It is
	// empty unless the caller asked for filtering.
	Filtered []AICollectFiltered `json:"filtered"`
	// Summary is the model's closing sentence when it chose to stop.
	Summary string `json:"summary,omitempty"`
	// Stopped says why the loop ended, in the operator's words.
	Stopped string `json:"stopped"`
	Model   string `json:"model"`
	DryRun  bool   `json:"dry_run"`
}

// aiDecision is the model's per-turn reply.
type aiDecision struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
	Done    bool   `json:"done"`
}

// aiFindings is the model's extraction reply.
type aiFindings struct {
	Credentials []struct {
		Name     string `json:"name"`
		Username string `json:"username"`
		Password string `json:"password"`
		Source   string `json:"source"`
	} `json:"credentials"`
	APIKeys []struct {
		Name   string `json:"name"`
		Key    string `json:"key"`
		Source string `json:"source"`
	} `json:"api_keys"`
	Loot []struct {
		Name    string `json:"name"`
		Content string `json:"content"`
		Source  string `json:"source"`
	} `json:"loot"`
}

// AICollect runs a bounded information-collection loop against a session and
// files what it finds into the console's loot and credential vaults. The
// read-only policy is enforced unless the caller turned it off; see
// AICollectRequest.ReadOnly.
func (c *Client) AICollect(ctx context.Context, p ai.Provider, req AICollectRequest) (*AICollectResult, error) {
	if p == nil || !p.Configured() {
		return nil, ai.ErrNotConfigured
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return nil, errors.New("missing session id")
	}
	steps := req.MaxSteps
	if steps <= 0 {
		steps = DefaultAICollectSteps
	}
	if steps > MaxAICollectSteps {
		steps = MaxAICollectSteps
	}

	// The read-only policy is on unless the operator turned it off, either
	// in the settings file or, when the caller sets the field, for this one
	// run. It is resolved once here so the policy cannot change mid-run.
	readOnly := req.ReadOnly == nil || *req.ReadOnly

	result := &AICollectResult{
		Steps:  make([]AICollectStep, 0, steps),
		Model:  p.Info().Model,
		DryRun: req.DryRun,
	}

	conversation := ai.NewConversation(aiCollectSystemPrompt(readOnly))
	conversation.User(aiCollectTaskPrompt(c.describeSession(req.SessionID), req.Objective, steps, readOnly))

	run := c.runAICommandLoop(ctx, p, conversation, aiRunConfig{
		sessionID: req.SessionID,
		steps:     steps,
		readOnly:  readOnly,
		dryRun:    req.DryRun,
		progress:  req.Progress,
	})
	result.Steps = run.steps
	result.Stopped = run.stopped
	result.Summary = run.summary
	transcript := run.transcript
	if run.summary != "" {
		emitAI(req.Progress, AIEvent{Type: AIEventSummary, Text: run.summary})
	}

	if !req.DryRun && transcript != "" {
		findings, err := aiExtractFindings(ctx, p, transcript)
		if err != nil {
			result.Skipped = append(result.Skipped, "extraction failed: "+err.Error())
		} else {
			if req.FilterFindings {
				emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: "reviewing findings"})
				kept, dropped, ferr := aiFilterFindings(ctx, p, findings)
				if ferr != nil {
					// Filtering is a convenience; losing a real credential
					// because the review call failed would be worse than
					// filing a duplicate, so the unfiltered list stands.
					result.Skipped = append(result.Skipped, "filter pass failed, kept every finding: "+ferr.Error())
				} else {
					findings = kept
					result.Filtered = dropped
					emitAI(req.Progress, AIEvent{Type: AIEventStatus, Text: fmt.Sprintf("filtered %d of %d findings", len(dropped), len(dropped)+len(kept))})
				}
			}
			result.Findings = findings
			if !req.NoStore {
				c.storeAIFindings(req.SessionID, findings, result)
			}
		}
	}
	if result.Findings == nil {
		result.Findings = []AICollectFinding{}
	}
	if result.Stored == nil {
		result.Stored = []AICollectStored{}
	}
	if result.Filtered == nil {
		result.Filtered = []AICollectFiltered{}
	}
	if result.Skipped == nil {
		result.Skipped = []string{}
	}
	return result, nil
}

// reasoningProvider is the optional half of ai.Provider: a provider that can
// also hand back the model's reasoning. It is an interface here rather than in
// ai.Provider so a provider that only implements Chat -- a scripted one in a
// test, say -- keeps working unchanged.
type reasoningProvider interface {
	ChatReasoning(ctx context.Context, messages []ai.Message) (string, string, error)
}

// chatWithReasoning calls the provider and returns the model's reasoning when
// the provider can supply it, so the transcript can show the model's working.
func chatWithReasoning(ctx context.Context, p ai.Provider, messages []ai.Message) (string, string, error) {
	if rp, ok := p.(reasoningProvider); ok {
		return rp.ChatReasoning(ctx, messages)
	}
	reply, err := p.Chat(ctx, messages)
	return reply, "", err
}

// streamingProvider is the optional streaming half of ai.Provider: a provider
// that can publish the model's reply while it is still being written.
//
// Like reasoningProvider it lives here rather than in ai.Provider so a provider
// that only implements Chat -- a scripted one in a test, say -- keeps working.
type streamingProvider interface {
	ChatReasoningStream(ctx context.Context, messages []ai.Message, onDelta ai.StreamDelta) (string, string, error)
}

// chatWithReasoningStreamed is chatWithReasoning with the model's output
// published as it arrives.
//
// The operator asked to watch the model think, not to watch an empty panel
// fill in at the end of a turn, so with a streaming endpoint every increment of
// reasoning and reply text is emitted the moment the model writes it. A
// provider that cannot stream, or one that fails before producing anything,
// falls back to the blocking call: trying is never worse than not trying, but a
// half-published turn is not retried, because that would render the same
// reasoning twice.
func chatWithReasoningStreamed(ctx context.Context, p ai.Provider, messages []ai.Message, progress AIProgress, index int) (string, string, error) {
	sp, ok := p.(streamingProvider)
	if !ok {
		return chatWithReasoning(ctx, p, messages)
	}
	streamed := false
	content, reasoning, err := sp.ChatReasoningStream(ctx, messages, func(contentDelta, reasoningDelta string) {
		streamed = true
		if reasoningDelta != "" {
			emitAI(progress, AIEvent{Type: AIEventThinkingDelta, Index: index, Text: reasoningDelta})
		}
		if contentDelta != "" {
			emitAI(progress, AIEvent{Type: AIEventAnswerDelta, Index: index, Text: contentDelta})
		}
	})
	if err == nil {
		return content, reasoning, nil
	}
	if streamed {
		return "", "", err
	}
	return chatWithReasoning(ctx, p, messages)
}

// describeSession renders the target context the model is given. A lookup
// failure is not fatal: the run is still useful with just the ID, and refusing
// to start because the session list was momentarily unavailable would be worse
// than probing without the hostname.
func (c *Client) describeSession(id string) string {
	sessions, err := c.Sessions()
	if err != nil {
		return "session " + id
	}
	for _, s := range sessions {
		if s.ID == id {
			return fmt.Sprintf("host=%s user=%s os=%s/%s transport=%s remote=%s",
				s.Hostname, s.Username, s.OS, s.Arch, s.Transport, s.RemoteAddress)
		}
	}
	return "session " + id
}

// aiExtractFindings runs the second phase: one call that turns the transcript
// into structured findings.
func aiExtractFindings(ctx context.Context, p ai.Provider, transcript string) ([]AICollectFinding, error) {
	var parsed aiFindings
	err := ai.ChatJSON(ctx, p, []ai.Message{
		ai.SystemMessage(aiExtractSystemPrompt()),
		ai.UserMessage("Reconnaissance transcript:\n\n" + transcript),
	}, &parsed)
	if err != nil {
		return nil, err
	}

	out := make([]AICollectFinding, 0, len(parsed.Credentials)+len(parsed.APIKeys)+len(parsed.Loot))
	for _, c := range parsed.Credentials {
		if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
			continue
		}
		out = append(out, AICollectFinding{
			Kind: "credential", Name: firstNonEmpty(c.Name, c.Username, c.Source),
			Username: strings.TrimSpace(c.Username), Secret: c.Password, Source: c.Source,
		})
	}
	for _, k := range parsed.APIKeys {
		if strings.TrimSpace(k.Key) == "" {
			continue
		}
		out = append(out, AICollectFinding{
			Kind: "apikey", Name: firstNonEmpty(k.Name, "api key"), Secret: k.Key, Source: k.Source,
		})
	}
	for _, l := range parsed.Loot {
		if strings.TrimSpace(l.Content) == "" {
			continue
		}
		out = append(out, AICollectFinding{
			Kind: "loot", Name: firstNonEmpty(l.Name, "collected file"), Content: l.Content, Source: l.Source,
		})
	}
	return out, nil
}

// aiFilterFindings runs the optional second phase: one call that reviews the
// extraction phase's findings and reports which of them are worth keeping.
//
// It answers with indices rather than with the findings themselves. A model
// asked to copy a credential back is a model that can mistype it, and the
// console already holds the value it was handed, so the review can remove an
// entry but never rewrite one.
//
// A failure here is not fatal and loses nothing: the caller keeps the
// unfiltered findings and records the error. A credential that is missing
// costs the operator more than a duplicate they have to skim past.
func aiFilterFindings(ctx context.Context, p ai.Provider, findings []AICollectFinding) ([]AICollectFinding, []AICollectFiltered, error) {
	if len(findings) == 0 {
		return findings, nil, nil
	}

	type reviewItem struct {
		Index   int    `json:"index"`
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Source  string `json:"source,omitempty"`
		Secret  string `json:"secret,omitempty"`
		Content string `json:"content,omitempty"`
	}
	items := make([]reviewItem, 0, len(findings))
	for i, f := range findings {
		item := reviewItem{Index: i, Kind: f.Kind, Name: f.Name, Source: f.Source, Secret: f.Secret}
		if f.Kind == "loot" {
			// The transcript budget already caps loot, but a review that ships
			// megabytes back to the provider is worth avoiding.
			item.Content = truncateAIOutput(f.Content)
		}
		items = append(items, item)
	}
	payload, err := json.Marshal(items)
	if err != nil {
		return findings, nil, err
	}

	// Keep is a pointer so "the model returned no keep list at all" is
	// distinguishable from "the model listed nothing worth keeping". The first
	// is an unusable answer and keeps everything; the second is a decision and
	// is honoured.
	var review struct {
		Keep    *[]int `json:"keep"`
		Dropped []struct {
			Index  int    `json:"index"`
			Reason string `json:"reason"`
		} `json:"dropped"`
	}
	if err := ai.ChatJSON(ctx, p, []ai.Message{
		ai.SystemMessage(aiFilterSystemPrompt()),
		ai.UserMessage(aiFilterTaskPrompt(string(payload))),
	}, &review); err != nil {
		return findings, nil, err
	}
	if review.Keep == nil {
		return findings, nil, errors.New("filter pass returned no keep list")
	}

	// The answer is advisory: an index that names no entry is ignored, and a
	// repeated index keeps its entry once.
	keep := make([]bool, len(findings))
	for _, i := range *review.Keep {
		if i >= 0 && i < len(findings) {
			keep[i] = true
		}
	}
	reason := make(map[int]string, len(review.Dropped))
	for _, d := range review.Dropped {
		if r := strings.TrimSpace(d.Reason); r != "" {
			reason[d.Index] = r
		}
	}

	kept := make([]AICollectFinding, 0, len(findings))
	dropped := make([]AICollectFiltered, 0)
	for i, f := range findings {
		if keep[i] {
			kept = append(kept, f)
			continue
		}
		why := reason[i]
		if why == "" {
			why = "removed by the second pass"
		}
		dropped = append(dropped, AICollectFiltered{Index: i, Kind: f.Kind, Name: f.Name, Reason: why})
	}
	return kept, dropped, nil
}

// storeAIFindings writes findings to the vaults. A failure on one finding is
// recorded and the rest are attempted: a duplicate or an oversized file must
// not discard a credential the operator would otherwise have. seenCreds is
// keyed on the vault's own (username, plaintext) pair, which covers API keys
// too because the vault stores them as credentials named credAPIKeyUsername.
// aiCollectLabel picks the vault label for a finding. The Credential model has
// no name field -- the loot list renders a credential's collection as its name
// -- so the model's own label for the finding goes there, with the identifier
// the operator would recognise as the fallback.
func aiCollectLabel(name, fallback string) string {
	if s := strings.TrimSpace(name); s != "" {
		return s
	}
	if s := strings.TrimSpace(fallback); s != "" {
		return s
	}
	return aiCollectCollection
}

func (c *Client) storeAIFindings(sessionID string, findings []AICollectFinding, result *AICollectResult) {
	seenCreds := map[string]bool{}
	seenNames := map[string]bool{}
	if existing, err := c.Creds(); err == nil {
		for _, e := range existing {
			seenCreds[strings.ToLower(e.Username)+"\x00"+e.Plaintext] = true
		}
	}
	if existing, err := c.LootAllOf("file"); err == nil {
		for _, e := range existing {
			seenNames[strings.ToLower(e.Name)] = true
		}
	}

	for _, f := range findings {
		switch f.Kind {
		case "credential":
			key := strings.ToLower(strings.TrimSpace(f.Username)) + "\x00" + f.Secret
			if seenCreds[key] {
				result.Skipped = append(result.Skipped, "credential already in the vault: "+f.Username)
				continue
			}
			err := c.CredsAdd([]CredentialView{{
				Username:       strings.TrimSpace(f.Username),
				Plaintext:      f.Secret,
				OriginHostUUID: sessionID,
				Collection:     aiCollectLabel(f.Name, f.Username),
			}})
			if err != nil {
				result.Skipped = append(result.Skipped, "credential "+f.Username+": "+err.Error())
				continue
			}
			seenCreds[key] = true
			result.Stored = append(result.Stored, AICollectStored{Kind: "credential", Name: f.Username})
		case "apikey":
			// The vault files an API key as a credential with the reserved
			// username (see credAPIKeyUsername), so the same (username, value) key
			// that guards credentials also identifies one here. Without this the
			// same key was written again on every run.
			secret := strings.TrimSpace(f.Secret)
			if secret == "" {
				result.Skipped = append(result.Skipped, "api key "+f.Name+": empty value")
				continue
			}
			key := credAPIKeyUsername + "\x00" + secret
			if seenCreds[key] {
				result.Skipped = append(result.Skipped, "api key already in the vault: "+f.Name)
				continue
			}
			// The vault has no name field: the loot list renders a credential's
			// collection as its name, so a key filed under a bare collection was
			// indistinguishable from every other key ever harvested. The model's
			// label for the key goes there instead.
			_, err := c.LootAdd(&LootAddRequest{
				Type:       "credential",
				Name:       aiCollectLabel(f.Name, "api key"),
				CredAPIKey: secret,
			})
			if err != nil {
				result.Skipped = append(result.Skipped, "api key "+f.Name+": "+err.Error())
				continue
			}
			seenCreds[key] = true
			result.Stored = append(result.Stored, AICollectStored{Kind: "apikey", Name: f.Name})
		case "loot":
			name := strings.TrimSpace(f.Name)
			if seenNames[strings.ToLower(name)] {
				result.Skipped = append(result.Skipped, "loot already stored: "+name)
				continue
			}
			id, err := c.LootAdd(&LootAddRequest{
				Type:        "file",
				Name:        name,
				FileName:    name,
				FileType:    "text",
				FileDataB64: base64.StdEncoding.EncodeToString([]byte(f.Content)),
			})
			if err != nil {
				result.Skipped = append(result.Skipped, "loot "+name+": "+err.Error())
				continue
			}
			seenNames[strings.ToLower(name)] = true
			result.Stored = append(result.Stored, AICollectStored{Kind: "loot", Name: name, ID: id})
		}
	}
}

// joinExecOutput merges stdout and stderr the way a terminal would, so the
// model sees one stream in the order the command produced it.
func joinExecOutput(out *ExecResult) string {
	if out == nil {
		return ""
	}
	combined := out.Stdout
	if strings.TrimSpace(out.Stderr) != "" {
		if combined != "" && !strings.HasSuffix(combined, "\n") {
			combined += "\n"
		}
		combined += out.Stderr
	}
	return combined
}

// truncateAIOutput caps one command's output and says so, so the model knows
// the view is partial rather than concluding the target is empty.
func truncateAIOutput(s string) string {
	if len(s) <= maxAIStepOutput {
		return s
	}
	return s[:maxAIStepOutput] + "\n[output truncated]"
}

func aiRefusalFeedback(command string, err error) string {
	return fmt.Sprintf("The console refused %q before running it: %s\nPropose a different command from the allowlist.", command, err)
}

func aiExecErrorFeedback(command string, err error) string {
	return fmt.Sprintf("Running %q on the target failed: %s\nPropose a different command.", command, err)
}

func aiOutputFeedback(command, output string) string {
	if strings.TrimSpace(output) == "" {
		return fmt.Sprintf("Output of %q was empty. Propose the next command.", command)
	}
	return fmt.Sprintf("Output of %q:\n\n%s\n\nPropose the next command.", command, output)
}
