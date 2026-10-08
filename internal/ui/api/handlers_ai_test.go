package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"

	"sliverreshine/internal/ai"
	"sliverreshine/internal/config"
	"sliverreshine/internal/ui/sliver"
)

// The AI routes are the operator's window onto the assistant: whether it is on,
// what it may run, and a way to run it. These tests cover the three things that
// matter at the HTTP edge -- the key never leaves the process, a policy refusal
// is a successful check, and an unconfigured deployment says so instead of
// looking broken.

func TestAIStatusUnconfigured(t *testing.T) {
	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ai/status", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["configured"] != false {
		t.Errorf("configured = %v, want false", body["configured"])
	}
	if body["readOnly"] != true {
		t.Errorf("readOnly = %v, want true", body["readOnly"])
	}
	list, _ := body["allowlist"].([]any)
	if len(list) == 0 {
		t.Error("allowlist is empty; the UI cannot show what the assistant may run")
	}
}

func TestAIStatusNeverLeaksTheKey(t *testing.T) {
	s := New()
	s.SetAIConfig(ai.Config{BaseURL: "http://ai.example/v1", Model: "m", APIKey: "top-secret-key"})

	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/ai/status", nil))

	if strings.Contains(rec.Body.String(), "top-secret-key") {
		t.Fatalf("the status response disclosed the API key: %s", rec.Body.String())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["configured"] != true {
		t.Errorf("configured = %v, want true", body["configured"])
	}
	if body["hasKey"] != true {
		t.Errorf("hasKey = %v, want true when a key is set", body["hasKey"])
	}
	if body["model"] != "m" {
		t.Errorf("model = %v, want m", body["model"])
	}
}

func TestAIReadOnlyCheckAllowAndRefuse(t *testing.T) {
	cases := []struct {
		name        string
		command     string
		wantAllowed bool
	}{
		{"read-only", "id", true},
		{"destructive", "rm -rf /", false},
		{"chained", "id; rm -rf /", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"command": tc.command})
			req := httptest.NewRequest(http.MethodPost, "/api/ai/read-only/check", strings.NewReader(string(body)))
			prepareMutation(req)
			rec := httptest.NewRecorder()
			New().Routes().ServeHTTP(rec, req)

			// A refusal is a successful check, so it must not be an error status.
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if out["allowed"] != tc.wantAllowed {
				t.Errorf("allowed = %v, want %v (%s)", out["allowed"], tc.wantAllowed, rec.Body.String())
			}
			if !tc.wantAllowed && out["reason"] == "" {
				t.Error("a refusal carried no reason")
			}
		})
	}
}

// With no model configured the collect route answers 501, not 503 or 500: the
// request was understood, the feature is simply not enabled here.
func TestAICollectUnconfiguredIsNotImplemented(t *testing.T) {
	s := serverWithStub(&rpcStub{})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/ai-collect", strings.NewReader(`{}`))
	prepareMutation(req)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", rec.Code, rec.Body.String())
	}
}

// aiRPCStub answers the RPCs a full collection run reaches, so the HTTP route
// can be driven end to end against a scripted model.
type aiRPCStub struct {
	rpcpb.SliverRPCClient
	execCalls     int32
	credsAddCalls int32
}

func (s *aiRPCStub) GetSessions(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.Sessions, error) {
	return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1", Hostname: "lab", OS: "linux"}}}, nil
}

func (s *aiRPCStub) Execute(context.Context, *sliverpb.ExecuteReq, ...grpc.CallOption) (*sliverpb.Execute, error) {
	atomic.AddInt32(&s.execCalls, 1)
	return &sliverpb.Execute{Response: &commonpb.Response{}, Stdout: []byte("uid=0(root)")}, nil
}

func (s *aiRPCStub) Creds(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.Credentials, error) {
	return &clientpb.Credentials{}, nil
}

func (s *aiRPCStub) CredsAdd(context.Context, *clientpb.Credentials, ...grpc.CallOption) (*commonpb.Empty, error) {
	atomic.AddInt32(&s.credsAddCalls, 1)
	return &commonpb.Empty{}, nil
}

func (s *aiRPCStub) LootAll(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.AllLoot, error) {
	return &clientpb.AllLoot{}, nil
}

func (s *aiRPCStub) LootAdd(_ context.Context, in *clientpb.Loot, _ ...grpc.CallOption) (*clientpb.Loot, error) {
	return &clientpb.Loot{ID: "loot-1", Name: in.GetName()}, nil
}

// A scripted OpenAI-compatible endpoint: it answers each /chat/completions call
// with the next reply in order.
func scriptedAIServer(t *testing.T, replies []string) *httptest.Server {
	t.Helper()
	var n int32
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("unexpected AI path %q", r.URL.Path)
		}
		i := int(atomic.AddInt32(&n, 1)) - 1
		content := ""
		if i < len(replies) {
			content = replies[i]
		} else {
			content = `{"done":true}`
		}
		resp, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": content}}},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp)
	}))
}

// The whole route, end to end: a read-only command is approved, run, and the
// credential the model extracts lands in the vault.
func TestAICollectRouteRunsAndStores(t *testing.T) {
	aiSrv := scriptedAIServer(t, []string{
		`{"command":"id","reason":"who am I"}`,
		`{"done":true,"reason":"collected"}`,
		`{"credentials":[{"username":"bob","password":"pw","source":"/etc/shadow"}],"api_keys":[],"loot":[]}`,
	})
	defer aiSrv.Close()

	stub := &aiRPCStub{}
	s := New()
	s.SetClient(&sliver.Client{RPC: stub})
	s.SetAIConfig(ai.Config{BaseURL: aiSrv.URL + "/v1", Model: "m"})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/ai-collect", strings.NewReader(`{"objective":"creds"}`))
	prepareMutation(req)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 1 {
		t.Errorf("Execute called %d times, want 1", n)
	}
	if n := atomic.LoadInt32(&stub.credsAddCalls); n != 1 {
		t.Errorf("CredsAdd called %d times, want 1", n)
	}
	var result sliver.AICollectResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Stored) != 1 || result.Stored[0].Kind != "credential" {
		t.Errorf("Stored = %+v, want one credential", result.Stored)
	}
	if result.Stopped != "model finished" {
		t.Errorf("Stopped = %q, want model finished", result.Stopped)
	}
}

// Turning the policy off through the settings endpoint takes effect on the
// running server at once: the live flag flips and the status route reports it,
// with no restart in between.
func TestAISettingsReadOnlyToggle(t *testing.T) {
	home := t.TempDir()
	if _, _, err := config.Ensure(home); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	s := New()
	s.SetSettingsHome(home)

	req := httptest.NewRequest(http.MethodPut, "/api/settings/ai", strings.NewReader(`{"readOnly":false}`))
	prepareMutation(req)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var put map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &put); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if put["readOnly"] != false {
		t.Errorf("PUT readOnly = %v, want false", put["readOnly"])
	}
	if s.AIReadOnly() {
		t.Error("the live server still reports the policy as on after the change")
	}

	rec2 := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/ai/status", nil))
	var status map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status["readOnly"] != false {
		t.Errorf("status readOnly = %v, want false", status["readOnly"])
	}

	// The policy check keeps judging commands even while the policy is off, so
	// the operator can still see what would have been refused.
	checkReq := httptest.NewRequest(http.MethodPost, "/api/ai/read-only/check", strings.NewReader(`{"command":"rm -rf /"}`))
	prepareMutation(checkReq)
	rec3 := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec3, checkReq)
	var check map[string]any
	if err := json.Unmarshal(rec3.Body.Bytes(), &check); err != nil {
		t.Fatalf("decode check: %v", err)
	}
	if check["allowed"] != false {
		t.Errorf("check allowed = %v, want false even with the policy off", check["allowed"])
	}
}

// The read-only toggle lives on its own control in the session tab and sends
// nothing but {readOnly}. The handler must treat the absent model fields as
// "leave alone": with plain string fields an earlier version decoded them as ""
// and blanked a working base URL and model every time the operator flipped the
// switch.
func TestAISettingsPartialUpdateKeepsModelConfig(t *testing.T) {
	home := t.TempDir()
	if _, _, err := config.Ensure(home); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	s := New()
	s.SetSettingsHome(home)

	put := func(body string) map[string]any {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/settings/ai", strings.NewReader(body))
		prepareMutation(req)
		rec := httptest.NewRecorder()
		s.Routes().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %s status = %d, want 200: %s", body, rec.Code, rec.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode PUT %s: %v", body, err)
		}
		return got
	}

	got := put(`{"baseURL":"http://ai.example/v1","model":"m1","thinking":true}`)
	if got["baseURL"] != "http://ai.example/v1" || got["model"] != "m1" {
		t.Fatalf("baseline save did not store the model config: %v", got)
	}

	// Exactly what the read-only checkbox sends.
	got = put(`{"readOnly":false}`)
	if got["baseURL"] != "http://ai.example/v1" {
		t.Errorf("a read-only-only update blanked baseURL: %v", got["baseURL"])
	}
	if got["model"] != "m1" {
		t.Errorf("a read-only-only update blanked model: %v", got["model"])
	}
	if got["readOnly"] != false {
		t.Errorf("readOnly = %v, want false", got["readOnly"])
	}

	// An explicit empty string still clears the field, so the panel can reset.
	got = put(`{"baseURL":"","model":""}`)
	if got["baseURL"] != "" || got["model"] != "" {
		t.Errorf("an explicit clear did not take effect: %v", got)
	}
}

// The policy is a deployment setting, so a body cannot turn it on: with the
// policy off, a request that asks for read_only still runs the command the
// model proposed, because the handler stamps the live value over the body.
func TestAICollectBodyCannotOverridePolicy(t *testing.T) {
	aiSrv := scriptedAIServer(t, []string{
		`{"command":"rm -rf /","reason":"clean the target"}`,
		`{"done":true,"reason":"collected"}`,
	})
	defer aiSrv.Close()

	stub := &aiRPCStub{}
	s := New()
	s.SetClient(&sliver.Client{RPC: stub})
	s.SetAIConfig(ai.Config{BaseURL: aiSrv.URL + "/v1", Model: "m"})
	s.SetAIReadOnly(false)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/ai-collect", strings.NewReader(`{"objective":"creds","read_only":true}`))
	prepareMutation(req)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 1 {
		t.Fatalf("Execute called %d times, want 1: the body must not re-enable the policy", n)
	}
	var result sliver.AICollectResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Steps) != 1 || result.Steps[0].Refused {
		t.Errorf("Steps = %+v, want one unrefused step", result.Steps)
	}
}
