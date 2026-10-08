package sliver

import (
	"context"
	"strings"

	"sliverreshine/internal/ai"
)

// This file carries the progress channel for the model-backed runs.
//
// A collection or privesc pass can spend a minute and dozens of commands
// before it has anything to show. Waiting for the final JSON means the
// operator stares at a spinner with no idea whether the model is working,
// looping, or stuck. The runs therefore report what they are doing as they do
// it: the model's reasoning for a step, the command it chose, and what came
// back. The transport (SSE) is a detail of the HTTP layer; this file is the
// vocabulary both runs speak.

// AIEventType names one kind of progress event.
type AIEventType string

const (
	// AIEventStatus is a human-readable line about the run itself, such as
	// "downloading linpeas" or "analysing enumeration output".
	AIEventStatus AIEventType = "status"
	// AIEventThinking carries the model's own reasoning for the step it is
	// about to take, before anything has run.
	AIEventThinking AIEventType = "thinking"
	// AIEventThinkingDelta carries one increment of the model's reasoning as
	// it is written. The panel appends it to the turn's reasoning, so the
	// operator reads the model's working while the model is still producing
	// it rather than after the turn closes.
	AIEventThinkingDelta AIEventType = "thinking_delta"
	// AIEventAnswerDelta carries one increment of the model's reply text, the
	// decision the run is about to act on.
	AIEventAnswerDelta AIEventType = "answer_delta"
	// AIEventCommand announces the command the model chose, before it runs.
	AIEventCommand AIEventType = "command"
	// AIEventStep reports a finished step: the command plus its output, a
	// refusal, or an execution error.
	AIEventStep AIEventType = "step"
	// AIEventSummary carries the model's closing explanation.
	AIEventSummary AIEventType = "summary"
	// AIEventDone is the last event. Result holds the run's final value.
	AIEventDone AIEventType = "done"
)

// AIEvent is one progress event.
//
// A single struct rather than a type per event keeps the SSE payload shape
// stable: the browser switches on Type and reads the field that event
// populates, and a new event kind does not invalidate the ones already sent.
type AIEvent struct {
	Type AIEventType `json:"type"`
	// Index is the zero-based step the event belongs to, for events that
	// belong to one.
	Index int `json:"index,omitempty"`
	// Text is the payload for status, thinking and summary events.
	Text string `json:"text,omitempty"`
	// Step is the payload for command and step events. For a command event
	// only Command, Reason and Thinking are set; a step event fills in the
	// outcome.
	Step *AICollectStep `json:"step,omitempty"`
	// Result is the payload for the done event.
	Result any `json:"result,omitempty"`
}

// AIProgress receives events as a run proceeds. A nil AIProgress is valid and
// discards every event, so a caller that only wants the final result passes
// nil rather than a no-op closure.
type AIProgress func(AIEvent)

// emitAI delivers one event, tolerating a nil sink.
func emitAI(progress AIProgress, ev AIEvent) {
	if progress != nil {
		progress(ev)
	}
}

// aiRunConfig is the shared shape of a bounded, policy-gated command loop.
//
// Collection and privilege escalation differ in what they ask the model to do
// and how they seed the conversation, not in how they drive it: propose a
// command, put it through the policy, run it, feed the output back. Keeping
// one loop means a fix to the policy handling or the step accounting lands in
// both runs rather than in whichever one was edited.
type aiRunConfig struct {
	// sessionID is the live session every command runs against.
	sessionID string
	// steps bounds how many commands the model may propose.
	steps int
	// readOnly routes commands through the allowlist instead of the target's
	// own shell.
	readOnly bool
	// dryRun plans without executing.
	dryRun bool
	// progress receives events as the loop proceeds. It may be nil.
	progress AIProgress
}

// aiRunOutcome is what one bounded loop produced.
type aiRunOutcome struct {
	steps      []AICollectStep
	transcript string
	stopped    string
	summary    string
}

// runAICommandLoop drives the model through at most cfg.steps commands against
// the session, enforcing the read-only policy when it is on.
//
// The conversation arrives already seeded with the run's system prompt and
// first user turn; the loop appends the model's replies and the feedback for
// each command, exactly as the earlier single-purpose collector did.
func (c *Client) runAICommandLoop(ctx context.Context, p ai.Provider, conversation *ai.Conversation, cfg aiRunConfig) aiRunOutcome {
	out := aiRunOutcome{
		steps:   make([]AICollectStep, 0, cfg.steps),
		stopped: "step limit reached",
	}
	var transcript strings.Builder

	for i := 0; i < cfg.steps; i++ {
		// The live panel is cleared before the model starts writing, so the
		// previous turn's text never sits above the text replacing it.
		emitAI(cfg.progress, AIEvent{Type: AIEventThinking, Index: i, Text: ""})
		reply, thinking, err := chatWithReasoningStreamed(ctx, p, conversation.Messages(), cfg.progress, i)
		if err != nil {
			out.stopped = "model call failed: " + err.Error()
			break
		}
		conversation.Assistant(reply)

		var decision aiDecision
		if err := ai.DecodeJSON(reply, &decision); err != nil {
			out.stopped = "model reply was not a usable decision"
			break
		}
		if decision.Done || strings.TrimSpace(decision.Command) == "" {
			out.stopped = "model finished"
			out.summary = strings.TrimSpace(decision.Reason)
			break
		}

		step := AICollectStep{
			Command:  strings.TrimSpace(decision.Command),
			Reason:   strings.TrimSpace(decision.Reason),
			Thinking: strings.TrimSpace(thinking),
		}

		// The model's reasoning is published before the command runs, so the
		// operator sees why the model is about to do something while it is
		// still a decision rather than after the fact.
		if step.Thinking != "" {
			emitAI(cfg.progress, AIEvent{Type: AIEventThinking, Index: i, Text: step.Thinking})
		}
		emitAI(cfg.progress, AIEvent{Type: AIEventCommand, Index: i, Step: &AICollectStep{
			Command:  step.Command,
			Reason:   step.Reason,
			Thinking: step.Thinking,
		}})

		var (
			path string
			args []string
		)
		if cfg.readOnly {
			// The policy check is not advisory. While the policy is on,
			// nothing reaches Execute without it.
			var checkErr error
			path, args, checkErr = CheckReadOnlyCommand(step.Command)
			if checkErr != nil {
				step.Refused = true
				step.Refusal = checkErr.Error()
				out.steps = append(out.steps, step)
				emitAI(cfg.progress, AIEvent{Type: AIEventStep, Index: i, Step: &out.steps[len(out.steps)-1]})
				conversation.User(aiRefusalFeedback(step.Command, checkErr))
				transcript.WriteString("$ " + step.Command + "\n[refused: " + checkErr.Error() + "]\n\n")
				continue
			}
		}
		if cfg.dryRun {
			out.steps = append(out.steps, step)
			emitAI(cfg.progress, AIEvent{Type: AIEventStep, Index: i, Step: &out.steps[len(out.steps)-1]})
			if cfg.readOnly {
				conversation.User("The command was policy-approved but this is a dry run, so nothing was executed. Propose the next command.")
			} else {
				conversation.User("This is a dry run, so nothing was executed. Propose the next command.")
			}
			transcript.WriteString("$ " + step.Command + "\n[dry run: not executed]\n\n")
			continue
		}

		var outRes *ExecResult
		if cfg.readOnly {
			outRes, err = c.Execute(cfg.sessionID, path, args)
		} else {
			// The operator turned the policy off, so the command runs exactly
			// as the model wrote it, through the target's own shell.
			outRes, err = c.ShellCommand(cfg.sessionID, step.Command)
		}
		if err != nil {
			step.Error = err.Error()
			out.steps = append(out.steps, step)
			emitAI(cfg.progress, AIEvent{Type: AIEventStep, Index: i, Step: &out.steps[len(out.steps)-1]})
			conversation.User(aiExecErrorFeedback(step.Command, err))
			transcript.WriteString("$ " + step.Command + "\n[execution failed: " + err.Error() + "]\n\n")
			continue
		}
		step.Status = outRes.Status
		step.Output = truncateAIOutput(joinExecOutput(outRes))
		out.steps = append(out.steps, step)
		emitAI(cfg.progress, AIEvent{Type: AIEventStep, Index: i, Step: &out.steps[len(out.steps)-1]})
		conversation.User(aiOutputFeedback(step.Command, step.Output))
		transcript.WriteString("$ " + step.Command + "\n" + step.Output + "\n\n")
	}

	out.transcript = transcript.String()
	return out
}
