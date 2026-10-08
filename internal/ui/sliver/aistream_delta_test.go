package sliver

import (
	"context"
	"errors"
	"testing"

	"sliverreshine/internal/ai"
)

// streamingStubProvider is a provider that can stream, so a test can watch the
// deltas chatWithReasoningStreamed publishes. It records whether the blocking
// Chat was reached, which is how a test tells a fallback from a retry.
type streamingStubProvider struct {
	content   string
	reasoning string
	streamErr error
	chatErr   error
	// emit lists the deltas to publish, as {reasoning, content}.
	emit [][2]string

	streamCalls int
	chatCalls   int
}

func (s *streamingStubProvider) Configured() bool { return true }

func (s *streamingStubProvider) Info() ai.Info {
	return ai.Info{BaseURL: "http://stub/v1", Model: "stub-model"}
}

func (s *streamingStubProvider) Chat(_ context.Context, _ []ai.Message) (string, error) {
	s.chatCalls++
	return s.content, s.chatErr
}

func (s *streamingStubProvider) ChatReasoningStream(_ context.Context, _ []ai.Message, onDelta ai.StreamDelta) (string, string, error) {
	s.streamCalls++
	for _, d := range s.emit {
		if onDelta != nil {
			onDelta(d[1], d[0])
		}
	}
	if s.streamErr != nil {
		return "", "", s.streamErr
	}
	return s.content, s.reasoning, nil
}

// A streaming provider's deltas must reach the operator as thinking_delta and
// answer_delta events, tagged with the step they belong to, and the blocking
// path must stay untouched when the stream succeeds.
func TestChatWithReasoningStreamedEmitsDeltas(t *testing.T) {
	p := &streamingStubProvider{
		content:   `{"done":true}`,
		reasoning: "because",
		emit:      [][2]string{{"be", ""}, {"cause", ""}, {"", `{"done"`}, {"", `:true}`}},
	}
	var events []AIEvent
	content, reasoning, err := chatWithReasoningStreamed(context.Background(), p, []ai.Message{ai.UserMessage("hi")}, func(ev AIEvent) {
		events = append(events, ev)
	}, 2)
	if err != nil {
		t.Fatalf("chatWithReasoningStreamed: %v", err)
	}
	if content != `{"done":true}` || reasoning != "because" {
		t.Fatalf("content/reasoning = %q/%q, want the accumulation", content, reasoning)
	}
	want := []AIEvent{
		{Type: AIEventThinkingDelta, Index: 2, Text: "be"},
		{Type: AIEventThinkingDelta, Index: 2, Text: "cause"},
		{Type: AIEventAnswerDelta, Index: 2, Text: `{"done"`},
		{Type: AIEventAnswerDelta, Index: 2, Text: `:true}`},
	}
	if len(events) != len(want) {
		t.Fatalf("emitted %d events, want %d: %+v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].Type != want[i].Type || events[i].Index != want[i].Index || events[i].Text != want[i].Text {
			t.Errorf("event %d = %+v, want %+v", i, events[i], want[i])
		}
	}
	if p.chatCalls != 0 {
		t.Errorf("blocking Chat was called %d times during a successful stream", p.chatCalls)
	}
}

// A provider that cannot stream must keep working: the call falls back to the
// blocking path instead of failing.
func TestChatWithReasoningStreamedFallsBackWithoutStreaming(t *testing.T) {
	p := &scriptedProvider{configured: true, replies: []string{`{"done":true}`}}
	var events []AIEvent
	content, _, err := chatWithReasoningStreamed(context.Background(), p, []ai.Message{ai.UserMessage("hi")}, func(ev AIEvent) {
		events = append(events, ev)
	}, 0)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	if content != `{"done":true}` {
		t.Errorf("content = %q, want the blocking reply", content)
	}
	if len(events) != 0 {
		t.Errorf("emitted %d events for a non-streaming provider, want 0", len(events))
	}
}

// A failure before any delta falls back to the blocking call, so a flaky
// stream endpoint still produces an answer.
func TestChatWithReasoningStreamedFallsBackOnEarlyError(t *testing.T) {
	p := &streamingStubProvider{content: "recovered", streamErr: errors.New("stream refused")}
	content, _, err := chatWithReasoningStreamed(context.Background(), p, []ai.Message{ai.UserMessage("hi")}, nil, 0)
	if err != nil {
		t.Fatalf("early-error fallback: %v", err)
	}
	if content != "recovered" {
		t.Errorf("content = %q, want the blocking reply", content)
	}
	if p.chatCalls != 1 {
		t.Errorf("blocking Chat called %d times, want 1", p.chatCalls)
	}
}

// A turn that failed after publishing deltas is not retried through the
// blocking path: the reasoning already rendered would be rendered a second
// time, and the operator would read the same working twice.
func TestChatWithReasoningStreamedDoesNotRetryAfterDeltas(t *testing.T) {
	p := &streamingStubProvider{streamErr: errors.New("connection reset"), emit: [][2]string{{"partial", ""}}}
	_, _, err := chatWithReasoningStreamed(context.Background(), p, []ai.Message{ai.UserMessage("hi")}, nil, 0)
	if err == nil {
		t.Fatal("a stream that failed after publishing returned no error")
	}
	if p.chatCalls != 0 {
		t.Errorf("blocking Chat called %d times after a partial stream, want 0", p.chatCalls)
	}
}

// The command loop must wire the stream through: a collection run's live panel
// is fed by thinking_delta and answer_delta events as the model writes, not by
// one blob once the turn closes.
func TestAICollectStreamsDeltaEvents(t *testing.T) {
	stub := &aiStub{}
	p := &streamingStubProvider{
		content:   `{"command":"id","reason":"who am i"}`,
		reasoning: "check the user first",
		emit: [][2]string{
			{"check the ", ""},
			{"user first", ""},
			{"", `{"command":"id"`},
			{"", `,"reason":"who am i"}`},
		},
	}
	var events []AIEvent
	_, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{
		SessionID: "s-1",
		NoStore:   true,
		DryRun:    true,
		MaxSteps:  1,
		Progress:  func(ev AIEvent) { events = append(events, ev) },
	})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	var sawThinking, sawAnswer bool
	for _, ev := range events {
		if ev.Type == AIEventThinkingDelta && ev.Index == 0 && ev.Text == "check the " {
			sawThinking = true
		}
		if ev.Type == AIEventAnswerDelta && ev.Index == 0 && ev.Text == `,"reason":"who am i"}` {
			sawAnswer = true
		}
	}
	if !sawThinking {
		t.Errorf("no thinking_delta event in %+v", events)
	}
	if !sawAnswer {
		t.Errorf("no answer_delta event in %+v", events)
	}
}
