package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Structured-output helpers.
//
// A feature that wants data back rather than prose asks the model for JSON and
// then has to cope with what models actually return: a fenced code block, a
// sentence of preamble, a trailing apology, an explanation of the answer
// wrapped around it. Doing that coping here means every feature gets the same
// tolerance instead of each one growing its own slightly different parser.

// JSONInstruction is the sentence a prompt should end with when the reply is
// meant to be machine-readable. It is exported so features word the request the
// same way, and so a test can assert it was included.
const JSONInstruction = "Reply with ONLY a single JSON value and no other text, no code fences, no explanation."

// ExtractJSON returns the JSON value embedded in a model reply.
//
// It strips a Markdown code fence when present, then takes the span from the
// first opening brace or bracket to the matching last closing one. The match is
// textual rather than a full parser: a brace inside a string literal can still
// fool it, which is why DecodeJSON reports a decode error instead of guessing
// when the result does not parse.
func ExtractJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		// Drop the language tag line ("```json") but not a line that is already
		// the payload.
		if j := strings.IndexByte(rest, '\n'); j >= 0 {
			if first := strings.TrimSpace(rest[:j]); !strings.ContainsAny(first, "{}[]") {
				rest = rest[j+1:]
			}
		}
		if k := strings.Index(rest, "```"); k >= 0 {
			rest = rest[:k]
		}
		s = strings.TrimSpace(rest)
	}

	start := strings.IndexAny(s, "{[")
	if start < 0 {
		return s
	}
	closer := byte('}')
	if s[start] == '[' {
		closer = ']'
	}
	if end := strings.LastIndexByte(s, closer); end > start {
		return s[start : end+1]
	}
	return s[start:]
}

// DecodeJSON extracts a JSON value from a model reply and decodes it into out.
func DecodeJSON(raw string, out any) error {
	blob := ExtractJSON(raw)
	if blob == "" {
		return fmt.Errorf("AI reply contained no JSON")
	}
	if err := json.Unmarshal([]byte(blob), out); err != nil {
		return fmt.Errorf("AI reply was not the expected JSON: %w", err)
	}
	return nil
}

// ChatJSON asks the provider for a JSON value and decodes it into out.
//
// The caller owns the prompt, including JSONInstruction; this only performs the
// request and the decode, so a feature keeps control of what it asked for.
func ChatJSON(ctx context.Context, p Provider, messages []Message, out any) error {
	if p == nil || !p.Configured() {
		return ErrNotConfigured
	}
	reply, err := p.Chat(ctx, messages)
	if err != nil {
		return err
	}
	return DecodeJSON(reply, out)
}
