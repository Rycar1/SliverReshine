package ai

import "testing"

func TestConversationKeepsAllWhenUnderBudget(t *testing.T) {
	c := NewConversation("sys").User("one").Assistant("two").User("three")
	got := c.Messages()
	if len(got) != 4 {
		t.Fatalf("Messages() returned %d turns, want 4", len(got))
	}
	if got[0].Role != RoleSystem || got[0].Content != "sys" {
		t.Errorf("first turn = %+v, want the system prompt", got[0])
	}
	if got[3].Content != "three" {
		t.Errorf("last turn = %+v, want the newest turn", got[3])
	}
}

// The system prompt is the policy the collector runs under, so trimming must
// never drop it even when it alone exceeds the budget.
func TestConversationTrimmingKeepsSystemPrompt(t *testing.T) {
	c := NewConversation("S").SetMaxBytes(10)
	c.User("aaaa").User("bbbb").User("cccc")

	got := c.Messages()
	if len(got) == 0 {
		t.Fatal("Messages() returned nothing")
	}
	if got[0].Role != RoleSystem || got[0].Content != "S" {
		t.Fatalf("first turn = %+v, want the system prompt kept", got[0])
	}
	// Budget after the system prompt is 9 bytes: the two newest 4-byte turns
	// fit, the oldest does not.
	want := []string{"bbbb", "cccc"}
	if len(got) != 1+len(want) {
		t.Fatalf("Messages() kept %d turns, want %d: %+v", len(got), 1+len(want), got)
	}
	for i, w := range want {
		if got[i+1].Content != w {
			t.Errorf("turn %d = %q, want %q (trim must drop the oldest)", i+1, got[i+1].Content, w)
		}
	}
}

func TestConversationTrimmingWithOversizedSystemPrompt(t *testing.T) {
	c := NewConversation("system prompt that is already over budget").SetMaxBytes(5)
	c.User("a").User("b")
	got := c.Messages()
	if len(got) != 1 || got[0].Role != RoleSystem {
		t.Fatalf("Messages() = %+v, want only the system prompt", got)
	}
}

func TestConversationMessagesIsACopy(t *testing.T) {
	c := NewConversation("sys").User("one")
	got := c.Messages()
	got[0].Content = "mutated"
	if c.Messages()[0].Content != "sys" {
		t.Fatal("editing the returned slice changed the conversation")
	}
}

func TestConversationDefaultBudget(t *testing.T) {
	c := NewConversation("sys")
	if c.maxBytes != DefaultMaxBytes {
		t.Fatalf("default maxBytes = %d, want %d", c.maxBytes, DefaultMaxBytes)
	}
	if c.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", c.Len())
	}
}
