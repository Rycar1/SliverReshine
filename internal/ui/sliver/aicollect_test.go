package sliver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"sliverreshine/internal/ai"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// scriptedProvider answers each Chat with the next reply in a fixed list, so a
// collection run is deterministic and needs no network. It records the
// conversations it was given, which is how a test checks that a refusal was fed
// back to the model instead of the command being retried blindly.
type scriptedProvider struct {
	replies []string
	// repeat is answered when replies runs out, for a run that must keep
	// proposing a command until the step ceiling stops it.
	repeat     string
	i          int
	configured bool
	seen       [][]ai.Message
}

func (s *scriptedProvider) Chat(_ context.Context, messages []ai.Message) (string, error) {
	cp := make([]ai.Message, len(messages))
	copy(cp, messages)
	s.seen = append(s.seen, cp)
	if s.i >= len(s.replies) {
		if s.repeat != "" {
			return s.repeat, nil
		}
		return `{"done":true,"reason":"no more scripted replies"}`, nil
	}
	r := s.replies[s.i]
	s.i++
	return r, nil
}

func (s *scriptedProvider) Configured() bool { return s.configured }
func (s *scriptedProvider) Info() ai.Info {
	return ai.Info{BaseURL: "http://stub/v1", Model: "stub-model"}
}

// aiStub drives the RPCs AICollect touches. Unimplemented RPCs panic through the
// embedded nil interface, so a run that starts calling something new fails
// loudly instead of silently succeeding.
type aiStub struct {
	rpcpb.SliverRPCClient

	execOut   string
	execErr   error
	execCalls int32

	creds []*clientpb.Credential
	loot  []*clientpb.Loot

	credsAddCalls int32
	lootAddCalls  int32
}

func (s *aiStub) Execute(_ context.Context, _ *sliverpb.ExecuteReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	atomic.AddInt32(&s.execCalls, 1)
	if s.execErr != nil {
		return nil, s.execErr
	}
	return &sliverpb.Execute{Response: &commonpb.Response{}, Stdout: []byte(s.execOut)}, nil
}

func (s *aiStub) ExecuteWindows(_ context.Context, _ *sliverpb.ExecuteWindowsReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	return nil, errors.New("ExecuteWindows must not be used for a linux session")
}

// GetSessions fails on purpose: describeSession must degrade to the session id
// rather than refusing to start.
func (s *aiStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	return nil, errors.New("session list unavailable")
}

func (s *aiStub) Creds(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Credentials, error) {
	return &clientpb.Credentials{Credentials: s.creds}, nil
}

func (s *aiStub) CredsAdd(_ context.Context, in *clientpb.Credentials, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	atomic.AddInt32(&s.credsAddCalls, 1)
	s.creds = append(s.creds, in.GetCredentials()...)
	return &commonpb.Empty{}, nil
}

func (s *aiStub) LootAll(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.AllLoot, error) {
	return &clientpb.AllLoot{Loot: s.loot}, nil
}

func (s *aiStub) LootAdd(_ context.Context, in *clientpb.Loot, _ ...grpc.CallOption) (*clientpb.Loot, error) {
	atomic.AddInt32(&s.lootAddCalls, 1)
	s.loot = append(s.loot, in)
	return &clientpb.Loot{ID: "loot-1", Name: in.GetName()}, nil
}

func collectClient(stub *aiStub) *Client {
	return &Client{RPC: stub, osCache: map[string]string{"s-1": "linux"}}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(blob)
}

// The central safety property: a command the read-only policy refuses must
// never reach Execute, no matter what the model proposes.
func TestAICollectRefusesAndNeverExecutes(t *testing.T) {
	stub := &aiStub{}
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"rm -rf /","reason":"clean the target"}`,
		`{"done":true,"reason":"finished"}`,
	}}

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{SessionID: "s-1", NoStore: true})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 0 {
		t.Fatalf("Execute was called %d times for a refused command; it must never be called", n)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("recorded %d steps, want 1", len(res.Steps))
	}
	if !res.Steps[0].Refused {
		t.Error("the destructive command was not recorded as refused")
	}
	if res.Steps[0].Refusal == "" {
		t.Error("the refusal carries no reason for the operator")
	}
	if res.Stopped != "model finished" {
		t.Errorf("Stopped = %q, want the model-finished reason", res.Stopped)
	}
	// The refusal must be reported back to the model so it can adapt.
	if len(p.seen) < 2 {
		t.Fatal("the model was not asked again after the refusal")
	}
	last := p.seen[1]
	if !strings.Contains(last[len(last)-1].Content, "refused") {
		t.Errorf("the refusal was not fed back to the model: %q", last[len(last)-1].Content)
	}
}

func TestAICollectExecutesAllowedCommand(t *testing.T) {
	stub := &aiStub{execOut: "uid=0(root) gid=0(root)"}
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"id","reason":"who am I"}`,
		`{"done":true}`,
	}}

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{SessionID: "s-1", NoStore: true})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 1 {
		t.Fatalf("Execute was called %d times, want 1", n)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("recorded %d steps, want 1", len(res.Steps))
	}
	if res.Steps[0].Refused {
		t.Errorf("a read-only command was refused: %s", res.Steps[0].Refusal)
	}
	if !strings.Contains(res.Steps[0].Output, "uid=0") {
		t.Errorf("Output = %q, want the command output", res.Steps[0].Output)
	}
}

func TestAICollectDryRunDoesNotExecute(t *testing.T) {
	stub := &aiStub{}
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"id"}`,
		`{"done":true}`,
	}}

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{SessionID: "s-1", DryRun: true})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 0 {
		t.Fatalf("Execute was called %d times in a dry run; it must not run anything", n)
	}
	if !res.DryRun {
		t.Error("result does not record the dry run")
	}
	if len(res.Findings) != 0 {
		t.Errorf("a dry run produced findings: %+v", res.Findings)
	}
}

func TestAICollectClampsStepCeiling(t *testing.T) {
	stub := &aiStub{execOut: "x"}
	p := &scriptedProvider{configured: true, repeat: `{"command":"id"}`} // never finishes on its own

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{SessionID: "s-1", MaxSteps: 1000, NoStore: true})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if len(res.Steps) != MaxAICollectSteps {
		t.Fatalf("ran %d steps, want the ceiling %d", len(res.Steps), MaxAICollectSteps)
	}
	if res.Stopped != "step limit reached" {
		t.Errorf("Stopped = %q, want the step-limit reason", res.Stopped)
	}
}

func TestAICollectRequiresProviderAndSession(t *testing.T) {
	stub := &aiStub{}
	if _, err := collectClient(stub).AICollect(context.Background(), &scriptedProvider{}, AICollectRequest{SessionID: "s-1"}); !errors.Is(err, ai.ErrNotConfigured) {
		t.Errorf("unconfigured provider = %v, want ErrNotConfigured", err)
	}
	if _, err := collectClient(stub).AICollect(context.Background(), &scriptedProvider{configured: true}, AICollectRequest{}); err == nil {
		t.Error("AICollect with no session id returned no error")
	}
}

func TestAIExtractFindingsSkipsIncompleteEntries(t *testing.T) {
	reply := mustJSON(t, map[string]any{
		"credentials": []map[string]string{
			{"name": "PostgreSQL superuser", "username": "bob", "password": "hunter2", "source": "/etc/shadow"},
			{"username": "nopass", "password": "", "source": "x"},
			{"username": "", "password": "orphan", "source": "y"},
		},
		"api_keys": []map[string]string{
			{"name": "aws", "key": "AKIA...", "source": "env"},
			{"name": "empty", "key": "", "source": "z"},
		},
		"loot": []map[string]string{
			{"name": "notes.txt", "content": "hello", "source": "/home/bob"},
			{"name": "blank", "content": "", "source": "/tmp"},
		},
	})
	p := &scriptedProvider{configured: true, replies: []string{reply}}

	findings, err := aiExtractFindings(context.Background(), p, "some transcript")
	if err != nil {
		t.Fatalf("aiExtractFindings: %v", err)
	}
	kinds := map[string]int{}
	for _, f := range findings {
		kinds[f.Kind]++
	}
	if kinds["credential"] != 1 || kinds["apikey"] != 1 || kinds["loot"] != 1 {
		t.Fatalf("findings = %+v, want exactly one credential, one apikey and one loot", findings)
	}
	// The model's own label for a credential is what the vault files it under,
	// so it has to survive extraction.
	for _, f := range findings {
		if f.Kind == "credential" && f.Name != "PostgreSQL superuser" {
			t.Errorf("credential label = %q, want the model's name for it", f.Name)
		}
	}
}

func TestStoreAIFindingsDedupes(t *testing.T) {
	stub := &aiStub{
		creds: []*clientpb.Credential{{Username: "bob", Plaintext: "hunter2"}},
		loot:  []*clientpb.Loot{{ID: "l1", Name: "notes.txt"}},
	}
	c := collectClient(stub)

	result := &AICollectResult{}
	c.storeAIFindings("s-1", []AICollectFinding{
		{Kind: "credential", Username: "bob", Secret: "hunter2"},  // duplicate
		{Kind: "credential", Username: "alice", Secret: "s3cret"}, // new
		{Kind: "apikey", Name: "aws", Secret: "AKIA..."},          // new
		{Kind: "loot", Name: "notes.txt", Content: "hello"},       // duplicate
		{Kind: "loot", Name: "new.txt", Content: "world"},         // new
	}, result)

	if len(result.Stored) != 3 {
		t.Fatalf("Stored = %+v, want 3 entries (alice, aws, new.txt)", result.Stored)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("Skipped = %+v, want 2 duplicates", result.Skipped)
	}
	storedKinds := map[string]bool{}
	for _, s := range result.Stored {
		storedKinds[s.Kind] = true
	}
	for _, want := range []string{"credential", "apikey", "loot"} {
		if !storedKinds[want] {
			t.Errorf("nothing of kind %q was stored", want)
		}
	}
	// Two writes reach CredsAdd: alice's password credential, and the API key,
	// which LootAdd stores as a credential with the reserved "apikey" username.
	if n := atomic.LoadInt32(&stub.credsAddCalls); n != 2 {
		t.Errorf("CredsAdd called %d times, want 2", n)
	}
	// The loot list shows a credential's collection as its name, so the label
	// has to identify the credential -- filing everything under one shared
	// collection is what made the list unreadable.
	var aliceLabel string
	for _, cr := range stub.creds {
		if cr.GetUsername() == "alice" {
			aliceLabel = cr.GetCollection()
		}
	}
	if aliceLabel != "alice" {
		t.Errorf("alice's vault label = %q, want her username as the fallback", aliceLabel)
	}
	// An API key is stored as a credential with the reserved "apikey"
	// username, so it lands in creds, not loot, with the model's label as its
	// collection. Nothing may be filed under the bare fallback collection.
	sawKeyLabel := false
	for _, cr := range stub.creds {
		if cr.GetCollection() == aiCollectCollection {
			t.Errorf("a finding was filed under the bare collection %q", aiCollectCollection)
		}
		if cr.GetUsername() == credAPIKeyUsername && cr.GetCollection() == "aws" {
			sawKeyLabel = true
		}
	}
	for _, l := range stub.loot {
		if l.GetName() == aiCollectCollection {
			t.Errorf("a loot entry was filed under the bare collection %q", aiCollectCollection)
		}
	}
	if !sawKeyLabel {
		t.Errorf("the API key was not filed under the model's label; creds = %+v", stub.creds)
	}
}

// Every finding has to land under something the operator can recognise in the
// list: the model's label when it gave one, the username otherwise.
func TestStoreAIFindingsLabelsCredentials(t *testing.T) {
	stub := &aiStub{}
	c := collectClient(stub)

	result := &AICollectResult{}
	c.storeAIFindings("s-1", []AICollectFinding{
		{Kind: "credential", Name: "PostgreSQL superuser", Username: "postgres", Secret: "pw1"},
		{Kind: "credential", Username: "deploy", Secret: "pw2"}, // model gave no label
		{Kind: "apikey", Name: "AWS access key (deploy)", Secret: "AKIA-1"},
	}, result)

	if len(result.Stored) != 3 {
		t.Fatalf("Stored = %+v, want 3 entries", result.Stored)
	}
	// An API key is stored as a credential with the reserved "apikey" username,
	// so both password credentials and the key land in creds; each carries the
	// model's label as its collection.
	want := map[string]string{
		"postgres":         "PostgreSQL superuser",
		"deploy":           "deploy",
		credAPIKeyUsername: "AWS access key (deploy)",
	}
	if len(stub.creds) != len(want) {
		t.Fatalf("creds = %+v, want %d entries", stub.creds, len(want))
	}
	for _, cr := range stub.creds {
		w, ok := want[cr.GetUsername()]
		if !ok {
			t.Errorf("unexpected credential %q", cr.GetUsername())
			continue
		}
		if cr.GetCollection() != w {
			t.Errorf("credential %q filed under %q, want %q", cr.GetUsername(), cr.GetCollection(), w)
		}
	}
	if len(stub.loot) != 0 {
		t.Errorf("loot = %+v, want nothing (an API key is a credential)", stub.loot)
	}
}

// An API key is identified by its value. Before this, the apikey branch wrote
// to the vault unconditionally, so every run refiled the same key.
func TestStoreAIFindingsDedupesAPIKeys(t *testing.T) {
	stub := &aiStub{
		// Already harvested on an earlier run.
		creds: []*clientpb.Credential{{Username: credAPIKeyUsername, Plaintext: "AKIA-OLD"}},
	}
	c := collectClient(stub)

	result := &AICollectResult{}
	c.storeAIFindings("s-1", []AICollectFinding{
		{Kind: "apikey", Name: "already there", Secret: "AKIA-OLD"}, // in the vault
		{Kind: "apikey", Name: "fresh", Secret: "AKIA-NEW"},         // new
		{Kind: "apikey", Name: "fresh again", Secret: "AKIA-NEW"},   // duplicate in the run
		{Kind: "apikey", Name: "blank", Secret: "  "},               // nothing to store
	}, result)

	if len(result.Stored) != 1 || result.Stored[0].Name != "fresh" {
		t.Fatalf("Stored = %+v, want only the fresh key", result.Stored)
	}
	if len(result.Skipped) != 3 {
		t.Fatalf("Skipped = %+v, want 3", result.Skipped)
	}
	if n := atomic.LoadInt32(&stub.credsAddCalls); n != 1 {
		t.Errorf("CredsAdd called %d times, want 1", n)
	}
}

func TestJoinAndTruncateOutput(t *testing.T) {
	if got := joinExecOutput(nil); got != "" {
		t.Errorf("joinExecOutput(nil) = %q, want empty", got)
	}
	out := &ExecResult{Stdout: "a", Stderr: "b"}
	if got := joinExecOutput(out); got != "a\nb" {
		t.Errorf("joinExecOutput = %q, want \"a\\nb\"", got)
	}
	long := strings.Repeat("x", maxAIStepOutput*2)
	trunc := truncateAIOutput(long)
	if !strings.HasSuffix(trunc, "[output truncated]") {
		t.Errorf("truncateAIOutput did not mark the truncation: %q", trunc[len(trunc)-40:])
	}
	if len(trunc) > maxAIStepOutput+len("\n[output truncated]") {
		t.Errorf("truncateAIOutput kept %d bytes, want the output capped at %d plus the marker", len(trunc), maxAIStepOutput)
	}
}

// With the policy off, the collector runs exactly what the model proposed,
// through the target's own shell, and records an ordinary step. Turning it off
// is a deployment decision the collector does not second-guess.
func TestAICollectReadOnlyOffRunsUnchecked(t *testing.T) {
	stub := &aiStub{execOut: "boom"}
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"rm -rf /","reason":"clean the target"}`,
		`{"done":true,"reason":"finished"}`,
	}}
	off := false

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{SessionID: "s-1", NoStore: true, ReadOnly: &off})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 1 {
		t.Fatalf("Execute called %d times, want 1 with the policy off", n)
	}
	if len(res.Steps) != 1 {
		t.Fatalf("recorded %d steps, want 1", len(res.Steps))
	}
	if res.Steps[0].Refused {
		t.Errorf("the step was refused even though the policy is off: %+v", res.Steps[0])
	}
	if res.Steps[0].Refusal != "" {
		t.Errorf("a refusal reason was recorded with the policy off: %q", res.Steps[0].Refusal)
	}
}

// An explicit true is the same as leaving the field unset: the command is
// checked before it runs.
func TestAICollectReadOnlyExplicitTrueRefuses(t *testing.T) {
	stub := &aiStub{}
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"rm -rf /","reason":"clean the target"}`,
		`{"done":true,"reason":"finished"}`,
	}}
	on := true

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{SessionID: "s-1", NoStore: true, ReadOnly: &on})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if n := atomic.LoadInt32(&stub.execCalls); n != 0 {
		t.Fatalf("Execute called %d times, want 0 with the policy on", n)
	}
	if len(res.Steps) != 1 || !res.Steps[0].Refused {
		t.Fatalf("Steps = %+v, want one refused step", res.Steps)
	}
}
