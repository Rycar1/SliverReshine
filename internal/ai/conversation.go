package ai

// Conversation is a chat transcript a feature appends to as it works.
//
// Every multi-step feature -- the read-only collector, a report writer, anything
// that asks a follow-up -- ends up doing the same bookkeeping: keep the system
// prompt first, add the model's reply, add what happened next, ask again. This
// is that bookkeeping, and it also enforces the rule those features keep getting
// wrong: a long run must not grow the prompt past what the model will accept, so
// the transcript is trimmed from the middle, never from the system prompt.
type Conversation struct {
	messages []Message
	// maxBytes bounds the total content of the transcript. Zero means
	// DefaultMaxBytes.
	maxBytes int
}

// DefaultMaxBytes bounds a transcript when the caller does not set a limit. It
// is a rough stand-in for a context window: characters, not tokens, but the
// ratio is stable enough to keep a run well inside any modern model's limit.
const DefaultMaxBytes = 64 << 10

// NewConversation starts a transcript with an optional system prompt.
func NewConversation(system string) *Conversation {
	c := &Conversation{maxBytes: DefaultMaxBytes}
	if system != "" {
		c.messages = append(c.messages, SystemMessage(system))
	}
	return c
}

// SetMaxBytes changes the transcript budget. It returns the conversation so
// calls can chain.
func (c *Conversation) SetMaxBytes(n int) *Conversation {
	c.maxBytes = n
	return c
}

// Add appends one turn.
func (c *Conversation) Add(m Message) *Conversation {
	c.messages = append(c.messages, m)
	return c
}

// System, User and Assistant append a turn with that role.
func (c *Conversation) System(text string) *Conversation    { return c.Add(SystemMessage(text)) }
func (c *Conversation) User(text string) *Conversation      { return c.Add(UserMessage(text)) }
func (c *Conversation) Assistant(text string) *Conversation { return c.Add(AssistantMessage(text)) }

// Len is the number of turns held, before trimming.
func (c *Conversation) Len() int { return len(c.messages) }

// Messages returns the turns to send, trimmed to the byte budget.
//
// The system prompt is always kept. When the transcript is over budget the
// oldest of the remaining turns are dropped, because the recent turns are the
// ones that describe what the feature is doing right now; a dropped early turn
// is history the model no longer needs. The result is a copy, so a caller
// cannot mutate the conversation by editing what it was handed.
func (c *Conversation) Messages() []Message {
	out := make([]Message, len(c.messages))
	copy(out, c.messages)
	if c.maxBytes <= 0 || transcriptBytes(out) <= c.maxBytes {
		return out
	}

	// Keep the system prompt (first turn) unconditionally, then walk backwards
	// from the newest turn, keeping each one that still fits.
	keep := make([]Message, 0, len(out))
	keep = append(keep, out[0])
	budget := c.maxBytes - len(out[0].Content)
	for i := len(out) - 1; i >= 1; i-- {
		if len(out[i].Content) > budget {
			break
		}
		budget -= len(out[i].Content)
		keep = append(keep, out[i])
	}
	// The walk collected newest-first; reverse everything after the system
	// prompt so the transcript reads in order.
	for i, j := 1, len(keep)-1; i < j; i, j = i+1, j-1 {
		keep[i], keep[j] = keep[j], keep[i]
	}
	return keep
}

func transcriptBytes(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content)
	}
	return total
}
