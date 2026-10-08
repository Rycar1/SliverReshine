// Package ai is the console's model-access layer.
//
// The console is growing several features that want a language model -- reading
// a target's filesystem to pull out credentials, summarising a session, writing
// a report, drafting a phishing lure -- and each of them needs the same three
// things: somewhere to send a chat request, a way to get structured data back
// instead of prose, and one place that knows whether a model is configured at
// all. This package is that place.
//
// What lives here:
//
//   - Config and Client: the transport. Any OpenAI-compatible
//     /chat/completions endpoint, which covers hosted providers, self-hosted
//     gateways and local runtimes alike.
//   - Provider: the interface features depend on, so a feature can be tested
//     with a scripted model instead of a network call.
//   - Service: the process-wide handle. Features ask the service for a
//     provider rather than building one, so the deployment is configured once
//     and every feature sees the same endpoint, model and key.
//   - Conversation and the JSON helpers: the parts every feature was otherwise
//     going to re-implement, slightly differently.
//
// What does not live here: any knowledge of sessions, commands, loot or the
// read-only policy. Those belong to the feature that uses the model, and
// keeping them out is what lets the policy be tested without a model and the
// transport without a session.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// ErrNotConfigured is returned when a feature asks for the model but the
// deployment has not configured one. It is a sentinel so a handler can answer
// "this feature is not enabled here" rather than "the console broke".
var ErrNotConfigured = errors.New("no AI provider is configured")

// ErrNoChoices is returned when a provider answers 200 with an empty choice
// list. It happens with gateways that swallow an upstream failure, and it is a
// response problem rather than a caller mistake.
var ErrNoChoices = errors.New("AI response contained no choices")

// ProviderError is a non-2xx answer from the provider, carrying the status and
// the provider's own message so an operator can act on it.
type ProviderError struct {
	Status  int
	Message string
}

func (e *ProviderError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("AI provider returned HTTP %d", e.Status)
	}
	return fmt.Sprintf("AI provider returned HTTP %d: %s", e.Status, e.Message)
}

// Message is one turn in a chat.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Roles accepted by the chat API.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// SystemMessage, UserMessage and AssistantMessage build turns without the
// caller having to remember the role strings.
func SystemMessage(text string) Message    { return Message{Role: RoleSystem, Content: text} }
func UserMessage(text string) Message      { return Message{Role: RoleUser, Content: text} }
func AssistantMessage(text string) Message { return Message{Role: RoleAssistant, Content: text} }

// Provider is what a feature depends on. Client implements it, and tests use a
// scripted implementation, so no feature test needs a network.
type Provider interface {
	// Chat sends the conversation and returns the model's reply.
	Chat(ctx context.Context, messages []Message) (string, error)
	// Configured reports whether a request could be attempted.
	Configured() bool
	// Info describes the endpoint for display. It never contains the key.
	Info() Info
}

// ReasoningProvider is implemented by providers that can also return the
// model's own reasoning. It is separate from Provider so a feature can show
// the model's working when the endpoint offers it and keep working unchanged
// when it does not -- a scripted provider in a test implements Provider only.
type ReasoningProvider interface {
	// ChatReasoning behaves like Chat and additionally returns the model's
	// reasoning, which is empty when the endpoint does not return any.
	ChatReasoning(ctx context.Context, messages []Message) (content, reasoning string, err error)
}

// Info is the display-safe description of a provider.
type Info struct {
	BaseURL string `json:"baseURL"`
	Model   string `json:"model"`
	// HasKey reports whether a key is set, without disclosing it.
	HasKey bool `json:"hasKey"`
}

// Config is the model endpoint. It is plain values rather than a pointer into
// the settings struct, so the transport can be built in a test without a
// config file.
type Config struct {
	// BaseURL is an OpenAI-compatible API root, e.g.
	// "https://api.openai.com/v1" or "http://127.0.0.1:11434/v1".
	BaseURL string
	// Model is the chat model name, e.g. "gpt-4o-mini".
	Model string
	// APIKey is sent as a bearer token. It is empty for endpoints that do not
	// authenticate, which is normal for a loopback runtime.
	APIKey string
	// Timeout bounds one chat round-trip. Zero means DefaultTimeout.
	Timeout time.Duration
	// Temperature is sent with every request. Zero is the deterministic
	// default, which is what a task that produces loot wants; a creative
	// feature can raise it.
	Temperature float64
	// MaxTokens caps the reply when non-zero. It is omitted when zero, because
	// some providers reject the field rather than ignoring it.
	MaxTokens int
	// Thinking asks a reasoning-capable endpoint to return the model's
	// reasoning as well as its answer. It is off by default because it is an
	// extension rather than part of the base API: a gateway that rejects
	// reasoning_effort would fail every call.
	Thinking bool
}

// DefaultTimeout bounds one chat round-trip. It is generous because a reasoning
// model on a cold cache can take a while, and none of these calls is on a
// latency-sensitive path.
//
// It was a minute, and a minute turned out to be too short for the credential
// collector. Its extraction call reads the entire reconnaissance transcript, so
// the request grows with the number of steps the run was allowed: a measured
// 14-step run answered every step inside the budget and then timed out on the
// extraction, which discarded the findings the run had already collected. Three
// minutes covers the collector's documented ceiling with room to spare. A
// deployment on a slower endpoint raises it with the settings file's
// timeoutSeconds rather than waiting for a run to fail at the last step.
const DefaultTimeout = 180 * time.Second

// Configured reports whether a request could be attempted at all.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.BaseURL) != "" && strings.TrimSpace(c.Model) != ""
}

// Endpoint returns the chat-completions URL for the configured base.
//
// A base that already names the path is used as-is, so an operator can point at
// a full endpoint if their gateway does not live under /v1.
func (c Config) Endpoint() string {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return ""
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base
	}
	return base + "/chat/completions"
}

// ModelsEndpoint returns the model-listing URL for the configured base.
//
// The listing lives beside chat/completions under the same API root, so a base
// that already names a full endpoint is trimmed back to its root first.
func (c Config) ModelsEndpoint() string {
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		return ""
	}
	base = strings.TrimSuffix(base, "/chat/completions")
	if strings.HasSuffix(base, "/models") {
		return base
	}
	return base + "/models"
}

// Info returns the display-safe description of this configuration.
func (c Config) Info() Info {
	return Info{
		BaseURL: strings.TrimSpace(c.BaseURL),
		Model:   strings.TrimSpace(c.Model),
		HasKey:  strings.TrimSpace(c.APIKey) != "",
	}
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	// ReasoningEffort is sent only when Thinking is on. It is the
	// OpenAI-compatible way to ask a reasoning model to show its working, and
	// it is omitted otherwise so a gateway that does not know the field never
	// sees it.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Stream          bool   `json:"stream"`
}

// replyMessage is one assistant turn in a reply. ReasoningContent is a
// non-standard extension that reasoning models, and the gateways in front of
// them, add beside content; it is empty for every other model.
type replyMessage struct {
	Message
	ReasoningContent string `json:"reasoning_content"`
}

type chatResponse struct {
	Choices []struct {
		Message replyMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// Client is a minimal OpenAI-compatible chat client.
type Client struct {
	cfg  Config
	http *http.Client
}

// NewClient builds a client for cfg. A zero Timeout gets DefaultTimeout.
func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: timeout}}
}

// Config returns the configuration the client was built with.
func (c *Client) Config() Config { return c.cfg }

// Configured reports whether the client has an endpoint and a model.
func (c *Client) Configured() bool { return c.cfg.Configured() }

// Info returns the display-safe description of the endpoint.
func (c *Client) Info() Info { return c.cfg.Info() }

// maxResponseBytes bounds the response body. A model reply is text; a body
// larger than this is not one, and reading it into memory unbounded would let a
// misconfigured URL hand the console a memory problem.
const maxResponseBytes = 4 << 20

// Chat sends the conversation and returns the model's reply, discarding any
// reasoning the endpoint returned. Callers that want the reasoning use
// ChatReasoning.
func (c *Client) Chat(ctx context.Context, messages []Message) (string, error) {
	content, _, err := c.ChatReasoning(ctx, messages)
	return content, err
}

// reasoningEffort is what a thinking-enabled request asks for. Medium is the
// middle of the range every OpenAI-compatible endpoint that understands the
// field accepts.
const reasoningEffort = "medium"

// ChatReasoning sends the conversation and returns the model's reply together
// with the model's reasoning, which is empty when Thinking is off or the
// endpoint does not return reasoning_content.
func (c *Client) ChatReasoning(ctx context.Context, messages []Message) (string, string, error) {
	if !c.Configured() {
		return "", "", ErrNotConfigured
	}
	if len(messages) == 0 {
		return "", "", errors.New("no messages supplied")
	}

	reqBody := chatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
	}
	if c.cfg.Thinking {
		reqBody.ReasoningEffort = reasoningEffort
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("encode AI request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build AI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("AI request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", "", fmt.Errorf("read AI response: %w", err)
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		// A non-JSON body is usually a proxy page. The status is the useful part.
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", "", &ProviderError{Status: resp.StatusCode, Message: snippet(raw)}
		}
		return "", "", fmt.Errorf("AI response was not JSON (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	// Providers disagree on whether a failure is an HTTP status, an "error"
	// object, or both. Checking the object first means the operator gets the
	// provider's own sentence instead of a bare status code.
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return "", "", &ProviderError{Status: resp.StatusCode, Message: parsed.Error.Message}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", &ProviderError{Status: resp.StatusCode, Message: snippet(raw)}
	}
	if len(parsed.Choices) == 0 {
		return "", "", ErrNoChoices
	}
	return parsed.Choices[0].Message.Content, parsed.Choices[0].Message.ReasoningContent, nil
}

// StreamDelta receives the text the model produced since the previous call.
// Either field may be empty: reasoning and content arrive on separate streams,
// and a chunk that only carries role metadata carries neither.
type StreamDelta func(content, reasoning string)

// StreamingProvider is implemented by providers that can publish a reply while
// the model is still writing it. It is separate from Provider so a feature can
// stream when the endpoint supports it and fall back to the blocking call when
// it does not -- a scripted provider in a test implements Provider only.
type StreamingProvider interface {
	// ChatReasoningStream behaves like ChatReasoning and additionally calls
	// onDelta with each increment of text as it arrives.
	ChatReasoningStream(ctx context.Context, messages []Message, onDelta StreamDelta) (content, reasoning string, err error)
}

// chatStreamChunk is one frame of an OpenAI-compatible streaming reply. The
// payload is a delta rather than a whole message, which is the only shape
// difference from the blocking response.
type chatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ChatReasoningStream sends the conversation with streaming enabled and reports
// the reply as the model writes it.
//
// The deltas are for the operator, not for the caller: the returned content and
// reasoning are the full accumulation, so a caller that only wants the answer
// passes a nil onDelta and gets exactly what ChatReasoning returns. A stream
// that ends without a single delta is reported as ErrNoChoices, which is how a
// caller that asked for streaming tells "the endpoint ignored the flag" from
// "the model said nothing".
func (c *Client) ChatReasoningStream(ctx context.Context, messages []Message, onDelta StreamDelta) (string, string, error) {
	if !c.Configured() {
		return "", "", ErrNotConfigured
	}
	if len(messages) == 0 {
		return "", "", errors.New("no messages supplied")
	}

	reqBody := chatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
		Stream:      true,
	}
	if c.cfg.Thinking {
		reqBody.ReasoningEffort = reasoningEffort
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", "", fmt.Errorf("encode AI request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.Endpoint(), bytes.NewReader(body))
	if err != nil {
		return "", "", fmt.Errorf("build AI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if key := strings.TrimSpace(c.cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("AI request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// A failure before the stream starts is still an HTTP failure, and the body
	// is a normal JSON error rather than a frame. Providers disagree on whether
	// a failure is an HTTP status, an "error" object, or both; preferring the
	// object gives the operator the provider message, exactly as the blocking
	// call does.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		var parsed chatStreamChunk
		if err := json.Unmarshal(raw, &parsed); err == nil && parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return "", "", &ProviderError{Status: resp.StatusCode, Message: parsed.Error.Message}
		}
		return "", "", &ProviderError{Status: resp.StatusCode, Message: snippet(raw)}
	}

	// An endpoint may ignore the stream flag and answer with one complete JSON
	// reply. That is a valid response, not an empty stream: decoding it here
	// keeps a working endpoint on the streaming path instead of spending a
	// second request to discover the same thing on the blocking path.
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if readErr != nil {
			return "", "", fmt.Errorf("read AI response: %w", readErr)
		}
		var parsed chatResponse
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return "", "", fmt.Errorf("AI response was not JSON: %w", err)
		}
		if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
			return "", "", &ProviderError{Status: resp.StatusCode, Message: parsed.Error.Message}
		}
		if len(parsed.Choices) == 0 {
			return "", "", ErrNoChoices
		}
		return parsed.Choices[0].Message.Content, parsed.Choices[0].Message.ReasoningContent, nil
	}

	var content, reasoning strings.Builder
	reader := bufio.NewReader(io.LimitReader(resp.Body, maxResponseBytes))
	for {
		line, readErr := reader.ReadString('\n')
		trimmed := strings.TrimSpace(line)
		// Frames are `data: {...}` lines separated by blanks; a bare colon
		// line is a keep-alive comment and is ignored.
		if payload, ok := strings.CutPrefix(trimmed, "data:"); ok {
			payload = strings.TrimSpace(payload)
			if payload == "[DONE]" {
				break
			}
			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(payload), &chunk); err == nil {
				if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
					return "", "", &ProviderError{Status: resp.StatusCode, Message: chunk.Error.Message}
				}
				for _, choice := range chunk.Choices {
					if d := choice.Delta.ReasoningContent; d != "" {
						reasoning.WriteString(d)
						if onDelta != nil {
							onDelta("", d)
						}
					}
					if d := choice.Delta.Content; d != "" {
						content.WriteString(d)
						if onDelta != nil {
							onDelta(d, "")
						}
					}
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return "", "", fmt.Errorf("read AI stream: %w", readErr)
		}
	}
	if content.Len() == 0 && reasoning.Len() == 0 {
		return "", "", ErrNoChoices
	}
	return content.String(), reasoning.String(), nil
}

// modelsResponse is the OpenAI-compatible model listing.
type modelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ListModels asks an OpenAI-compatible endpoint which models it serves.
//
// It is a package-level function rather than a Client method because the
// configuration panel lists models for an endpoint the operator has typed but
// not yet saved, so it must work for a Config that is not the live one.
func ListModels(ctx context.Context, cfg Config) ([]string, error) {
	url := cfg.ModelsEndpoint()
	if url == "" {
		return nil, errors.New("no AI base URL supplied")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(cfg.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("models request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read models response: %w", err)
	}
	var parsed modelsResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, &ProviderError{Status: resp.StatusCode, Message: snippet(raw)}
		}
		return nil, fmt.Errorf("models response was not JSON (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return nil, &ProviderError{Status: resp.StatusCode, Message: parsed.Error.Message}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &ProviderError{Status: resp.StatusCode, Message: snippet(raw)}
	}
	seen := make(map[string]bool, len(parsed.Data))
	models := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, id)
	}
	sort.Strings(models)
	return models, nil
}

// snippet trims a body for an error message. The full body can be a megabyte of
// HTML from a proxy that never reached the model, and pasting that into an
// operator's console buries the useful part.
func snippet(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
