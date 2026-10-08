package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConfigEndpoint(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", "https://api.openai.com/v1/chat/completions"},
		{"http://127.0.0.1:11434/v1", "http://127.0.0.1:11434/v1/chat/completions"},
		{"https://gw.example.com/v1/chat/completions", "https://gw.example.com/v1/chat/completions"},
		{"https://gw.example.com/v1/chat/completions/", "https://gw.example.com/v1/chat/completions"},
		{"  https://api.openai.com/v1  ", "https://api.openai.com/v1/chat/completions"},
	}
	for _, tc := range cases {
		got := Config{BaseURL: tc.base}.Endpoint()
		if got != tc.want {
			t.Errorf("Config{BaseURL:%q}.Endpoint() = %q, want %q", tc.base, got, tc.want)
		}
	}
}

func TestConfigConfigured(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"both set", Config{BaseURL: "http://x/v1", Model: "m"}, true},
		{"no base", Config{Model: "m"}, false},
		{"no model", Config{BaseURL: "http://x/v1"}, false},
		{"whitespace base", Config{BaseURL: "  ", Model: "m"}, false},
		{"whitespace model", Config{BaseURL: "http://x/v1", Model: " \t "}, false},
	}
	for _, tc := range cases {
		if got := tc.cfg.Configured(); got != tc.want {
			t.Errorf("%s: Configured() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Info must describe the endpoint without ever disclosing the key.
func TestConfigInfoHidesKey(t *testing.T) {
	cfg := Config{BaseURL: " http://x/v1 ", Model: " m ", APIKey: "super-secret"}
	info := cfg.Info()
	if info.BaseURL != "http://x/v1" || info.Model != "m" {
		t.Fatalf("Info() = %+v, want trimmed base and model", info)
	}
	if !info.HasKey {
		t.Error("Info().HasKey = false, want true when a key is set")
	}
	blob, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal Info: %v", err)
	}
	if strings.Contains(string(blob), "super-secret") {
		t.Fatalf("Info JSON leaked the API key: %s", blob)
	}
}

func TestClientChatSuccess(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello there"}}]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL + "/v1", Model: "test-model", APIKey: "k123"})
	reply, err := c.Chat(context.Background(), []Message{UserMessage("hi")})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if reply != "hello there" {
		t.Errorf("Chat reply = %q, want %q", reply, "hello there")
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("request path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer k123" {
		t.Errorf("Authorization = %q, want Bearer k123", gotAuth)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotBody.Model != "test-model" {
		t.Errorf("request model = %q, want test-model", gotBody.Model)
	}
}

func TestClientChatOmitsAuthWithoutKey(t *testing.T) {
	var hadAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, hadAuth = r.Header["Authorization"]
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{UserMessage("hi")}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if hadAuth {
		t.Error("Authorization header was sent even though no key was configured")
	}
}

func TestClientChatProviderErrorObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid api key","type":"auth"}}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), []Message{UserMessage("hi")})
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want *ProviderError", err)
	}
	if pe.Message != "invalid api key" {
		t.Errorf("ProviderError.Message = %q, want the provider message", pe.Message)
	}
}

func TestClientChatNonJSONNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>bad gateway</html>")
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), []Message{UserMessage("hi")})
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want *ProviderError", err)
	}
	if pe.Status != http.StatusBadGateway {
		t.Errorf("ProviderError.Status = %d, want 502", pe.Status)
	}
}

func TestClientChatNonJSON2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not json at all")
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), []Message{UserMessage("hi")})
	if err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Fatalf("error = %v, want a not-JSON error", err)
	}
}

func TestClientChatNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	_, err := c.Chat(context.Background(), []Message{UserMessage("hi")})
	if !errors.Is(err, ErrNoChoices) {
		t.Fatalf("error = %v, want ErrNoChoices", err)
	}
}

func TestClientChatNotConfigured(t *testing.T) {
	c := NewClient(Config{Model: "m"})
	if _, err := c.Chat(context.Background(), []Message{UserMessage("hi")}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
}

func TestClientChatNoMessages(t *testing.T) {
	c := NewClient(Config{BaseURL: "http://x/v1", Model: "m"})
	if _, err := c.Chat(context.Background(), nil); err == nil {
		t.Fatal("Chat with no messages returned no error")
	}
}

func TestClientChatContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	if _, err := c.Chat(ctx, []Message{UserMessage("hi")}); err == nil {
		t.Fatal("Chat with a cancelled context returned no error")
	}
}

// The collector's extraction call reads a transcript that grows with the number
// of steps the run was allowed, so its budget cannot be a constant that only
// fits short runs. This pins the two halves of the fix: the default is large
// enough for a long collection, and an operator's setting overrides it.
func TestClientTimeoutIsConfigurable(t *testing.T) {
	if DefaultTimeout < 3*time.Minute {
		t.Errorf("DefaultTimeout = %v; a long collection's extraction call outran the old 60s budget", DefaultTimeout)
	}
	if got := NewClient(Config{BaseURL: "http://x/v1", Model: "m"}).http.Timeout; got != DefaultTimeout {
		t.Errorf("an unset Timeout produced %v, want the default %v", got, DefaultTimeout)
	}
	if got := NewClient(Config{BaseURL: "http://x/v1", Model: "m", Timeout: 7 * time.Minute}).http.Timeout; got != 7*time.Minute {
		t.Errorf("Timeout = %v, want the configured 7m", got)
	}
	if got := NewClient(Config{BaseURL: "http://x/v1", Model: "m", Timeout: -time.Second}).http.Timeout; got != DefaultTimeout {
		t.Errorf("a negative Timeout produced %v, want the default %v", got, DefaultTimeout)
	}
}

func TestConfigModelsEndpoint(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"https://api.openai.com/v1", "https://api.openai.com/v1/models"},
		{"https://api.openai.com/v1/", "https://api.openai.com/v1/models"},
		{"https://gw.example.com/v1/chat/completions", "https://gw.example.com/v1/models"},
		{"https://gw.example.com/v1/models", "https://gw.example.com/v1/models"},
	}
	for _, tc := range cases {
		if got := (Config{BaseURL: tc.base}).ModelsEndpoint(); got != tc.want {
			t.Errorf("Config{BaseURL:%q}.ModelsEndpoint() = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// ListModels must sort and de-duplicate, send the bearer token, and surface a
// provider error rather than an empty list.
func TestListModels(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-4o-mini"},{"id":"gpt-4o"},{"id":"gpt-4o-mini"},{"id":""}]}`)
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), Config{BaseURL: srv.URL + "/v1", APIKey: "k"})
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer k")
	}
	if want := []string{"gpt-4o", "gpt-4o-mini"}; !equalStrings(models, want) {
		t.Errorf("models = %v, want %v", models, want)
	}

	if _, err := ListModels(context.Background(), Config{}); err == nil {
		t.Error("ListModels with no base URL: want error, got nil")
	}
}

func TestListModelsProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key"}}`)
	}))
	defer srv.Close()

	_, err := ListModels(context.Background(), Config{BaseURL: srv.URL + "/v1"})
	var perr *ProviderError
	if !errors.As(err, &perr) {
		t.Fatalf("ListModels error = %v, want *ProviderError", err)
	}
	if perr.Status != http.StatusUnauthorized || perr.Message != "invalid key" {
		t.Errorf("ProviderError = %+v", perr)
	}
}

// ChatReasoningStream must publish the reply as the model writes it: the deltas
// arrive in order, the accumulated content and reasoning are the concatenation
// of those deltas, and the request asks the endpoint for a stream. Without the
// stream flag the endpoint answers with one whole message and the operator sees
// nothing until the turn is over, which is the behaviour this replaces.
func TestClientChatReasoningStreamDeltas(t *testing.T) {
	var gotStream bool
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body chatRequest
		_ = json.Unmarshal(raw, &body)
		gotStream = body.Stream
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		frames := []string{
			`data: {"choices":[{"delta":{"reasoning_content":"think "}}]}`,
			`data: {"choices":[{"delta":{"reasoning_content":"more"}}]}`,
			`data: {"choices":[{"delta":{"content":"hello"}}]}`,
			`: keep-alive`,
			`data: {"choices":[{"delta":{"content":" there"}}]}`,
			`data: [DONE]`,
		}
		for _, f := range frames {
			_, _ = io.WriteString(w, f+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	var deltas []string
	content, reasoning, err := c.ChatReasoningStream(context.Background(), []Message{UserMessage("hi")}, func(contentDelta, reasoningDelta string) {
		deltas = append(deltas, reasoningDelta+"|"+contentDelta)
	})
	if err != nil {
		t.Fatalf("ChatReasoningStream: %v", err)
	}
	if !gotStream {
		t.Error("request did not set stream=true; the endpoint would reply with one whole message")
	}
	if gotAccept != "text/event-stream" {
		t.Errorf("Accept = %q, want text/event-stream", gotAccept)
	}
	if want := []string{"think |", "more|", "|hello", "| there"}; !equalStrings(deltas, want) {
		t.Errorf("deltas = %q, want %q", deltas, want)
	}
	if content != "hello there" {
		t.Errorf("content = %q, want the concatenation of the content deltas", content)
	}
	if reasoning != "think more" {
		t.Errorf("reasoning = %q, want the concatenation of the reasoning deltas", reasoning)
	}
}

// A stream that ends without a single delta means the endpoint ignored the
// stream flag; the caller must be able to tell that from an empty reply.
func TestClientChatReasoningStreamNoDeltas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	if _, _, err := c.ChatReasoningStream(context.Background(), []Message{UserMessage("hi")}, nil); !errors.Is(err, ErrNoChoices) {
		t.Fatalf("error = %v, want ErrNoChoices", err)
	}
}

// A failure before the stream starts is still an HTTP failure, reported with
// the provider status rather than as an empty stream.
func TestClientChatReasoningStreamProviderError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	_, _, err := c.ChatReasoningStream(context.Background(), []Message{UserMessage("hi")}, nil)
	var pe *ProviderError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v, want *ProviderError", err)
	}
	if pe.Status != http.StatusUnauthorized || pe.Message != "bad key" {
		t.Errorf("ProviderError = %+v", pe)
	}
}

// An error frame part-way through the stream is surfaced instead of being
// mistaken for a short but successful reply.
func TestClientChatReasoningStreamErrorFrame(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"error":{"message":"rate limited"}}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(Config{BaseURL: srv.URL, Model: "m"})
	_, _, err := c.ChatReasoningStream(context.Background(), []Message{UserMessage("hi")}, nil)
	var pe *ProviderError
	if !errors.As(err, &pe) || pe.Message != "rate limited" {
		t.Fatalf("error = %v, want a ProviderError carrying the frame message", err)
	}
}

// The streaming call guards the same way the blocking one does.
func TestClientChatReasoningStreamGuards(t *testing.T) {
	unconfigured := NewClient(Config{Model: "m"})
	if _, _, err := unconfigured.ChatReasoningStream(context.Background(), []Message{UserMessage("hi")}, nil); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("unconfigured client error = %v, want ErrNotConfigured", err)
	}
	configured := NewClient(Config{BaseURL: "http://x/v1", Model: "m"})
	if _, _, err := configured.ChatReasoningStream(context.Background(), nil, nil); err == nil {
		t.Error("stream with no messages returned no error")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
