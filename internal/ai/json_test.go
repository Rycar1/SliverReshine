package ai

import (
	"errors"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"bare object", `{"a":1}`, `{"a":1}`},
		{"bare array", `[1,2,3]`, `[1,2,3]`},
		{"fenced json", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"fenced no tag", "```\n{\"a\":1}\n```", `{"a":1}`},
		{"preamble", `Sure, here it is: {"a":1}`, `{"a":1}`},
		{"trailing text", `{"a":1} -- let me know if you need more`, `{"a":1}`},
		{"fenced with preamble", "Here you go:\n```json\n{\"a\":1}\n```\nDone.", `{"a":1}`},
		{"array with trailing", `[{"a":1}] done`, `[{"a":1}]`},
		{"empty", ``, ``},
		{"no json", `I cannot help with that.`, `I cannot help with that.`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractJSON(tc.raw); got != tc.want {
				t.Errorf("ExtractJSON(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name  string   `json:"name"`
		Items []string `json:"items"`
	}
	var got payload
	if err := DecodeJSON("```json\n{\"name\":\"x\",\"items\":[\"a\"]}\n```", &got); err != nil {
		t.Fatalf("DecodeJSON returned error: %v", err)
	}
	if got.Name != "x" || len(got.Items) != 1 || got.Items[0] != "a" {
		t.Fatalf("decoded = %+v, want name=x items=[a]", got)
	}
}

func TestDecodeJSONErrors(t *testing.T) {
	var out map[string]any
	if err := DecodeJSON("no json here", &out); err == nil {
		t.Error("DecodeJSON with no JSON returned no error")
	}
	if err := DecodeJSON("", &out); err == nil {
		t.Error("DecodeJSON with an empty reply returned no error")
	}
	if err := DecodeJSON(`{"a":`, &out); err == nil {
		t.Error("DecodeJSON with malformed JSON returned no error")
	}
}

func TestChatJSONNotConfigured(t *testing.T) {
	var out map[string]any
	if err := ChatJSON(nil, nil, []Message{UserMessage("hi")}, &out); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ChatJSON with a nil provider = %v, want ErrNotConfigured", err)
	}
	if err := ChatJSON(nil, NewClient(Config{}), []Message{UserMessage("hi")}, &out); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ChatJSON with an unconfigured client = %v, want ErrNotConfigured", err)
	}
}

func TestJSONInstructionMentionsJSON(t *testing.T) {
	if JSONInstruction == "" {
		t.Fatal("JSONInstruction is empty")
	}
}
