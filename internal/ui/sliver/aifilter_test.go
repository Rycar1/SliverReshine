package sliver

import (
	"context"
	"strings"
	"testing"
)

// aiFilterFindings answers with indices, so a test can hand it a fixed review
// and check that only the entries it named are removed.
func TestAIFilterFindingsDropsNamedEntries(t *testing.T) {
	findings := []AICollectFinding{
		{Kind: "credential", Name: "PostgreSQL superuser", Username: "bob", Secret: "hunter2"},
		{Kind: "credential", Name: "duplicate", Username: "bob", Secret: "hunter2"},
		{Kind: "credential", Name: "placeholder", Username: "svc", Secret: "changeme"},
		{Kind: "apikey", Name: "aws", Secret: "AKIAEXAMPLE"},
	}
	p := &scriptedProvider{configured: true, replies: []string{
		`{"keep":[0,3],"dropped":[{"index":1,"reason":"same bob/hunter2 as entry 0"},{"index":2,"reason":"changeme is a placeholder"}]}`,
	}}

	kept, dropped, err := aiFilterFindings(context.Background(), p, findings)
	if err != nil {
		t.Fatalf("aiFilterFindings: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %+v, want 2 entries", kept)
	}
	if kept[0].Secret != "hunter2" || kept[1].Secret != "AKIAEXAMPLE" {
		t.Errorf("kept = %+v, want the entries at index 0 and 3", kept)
	}
	if len(dropped) != 2 {
		t.Fatalf("dropped = %+v, want 2 entries", dropped)
	}
	if dropped[0].Index != 1 || !strings.Contains(dropped[0].Reason, "hunter2") {
		t.Errorf("dropped[0] = %+v, want index 1 with the model's reason", dropped[0])
	}
	if dropped[1].Index != 2 || dropped[1].Kind != "credential" {
		t.Errorf("dropped[1] = %+v, want index 2 of kind credential", dropped[1])
	}
	// The review is a second model call, and it must carry the findings the
	// first pass produced rather than an empty list.
	if len(p.seen) != 1 {
		t.Fatalf("the review made %d model calls, want 1", len(p.seen))
	}
	task := p.seen[0][len(p.seen[0])-1].Content
	if !strings.Contains(task, "hunter2") || !strings.Contains(task, "AKIAEXAMPLE") {
		t.Errorf("the review prompt did not carry the findings: %q", task)
	}
}

// An answer with no keep list at all is unusable, so the caller keeps every
// finding rather than silently discarding the run.
func TestAIFilterFindingsKeepsAllWithoutKeepList(t *testing.T) {
	findings := []AICollectFinding{
		{Kind: "credential", Name: "a", Username: "bob", Secret: "hunter2"},
		{Kind: "credential", Name: "b", Username: "alice", Secret: "s3cret"},
	}
	p := &scriptedProvider{configured: true, replies: []string{`{}`}}

	kept, dropped, err := aiFilterFindings(context.Background(), p, findings)
	if err == nil {
		t.Fatal("a review with no keep list was accepted")
	}
	if len(kept) != len(findings) || len(dropped) != 0 {
		t.Fatalf("kept = %+v dropped = %+v, want every finding kept and none dropped", kept, dropped)
	}
}

// The model's indices are advisory: a repeated index keeps one entry and an
// index past the end is ignored rather than panicking.
func TestAIFilterFindingsIgnoresOutOfRangeIndices(t *testing.T) {
	findings := []AICollectFinding{
		{Kind: "credential", Name: "a", Username: "bob", Secret: "hunter2"},
		{Kind: "credential", Name: "b", Username: "alice", Secret: "s3cret"},
	}
	p := &scriptedProvider{configured: true, replies: []string{`{"keep":[0,0,99,-3]}`}}

	kept, dropped, err := aiFilterFindings(context.Background(), p, findings)
	if err != nil {
		t.Fatalf("aiFilterFindings: %v", err)
	}
	if len(kept) != 1 || kept[0].Username != "bob" {
		t.Fatalf("kept = %+v, want only the entry at index 0", kept)
	}
	if len(dropped) != 1 || dropped[0].Index != 1 {
		t.Fatalf("dropped = %+v, want the entry at index 1", dropped)
	}
}

// An empty list needs no review: spending a model call on nothing is waste.
func TestAIFilterFindingsSkipsEmptyInput(t *testing.T) {
	p := &scriptedProvider{configured: true}
	kept, dropped, err := aiFilterFindings(context.Background(), p, nil)
	if err != nil || len(kept) != 0 || len(dropped) != 0 {
		t.Fatalf("kept = %+v dropped = %+v err = %v, want a no-op", kept, dropped, err)
	}
	if len(p.seen) != 0 {
		t.Fatalf("the review made %d model calls for an empty list, want 0", len(p.seen))
	}
}

// The filter runs between extraction and storing, so what reaches the vault is
// the reviewed list and the operator can see what was dropped and why.
func TestAICollectFiltersBeforeStoring(t *testing.T) {
	stub := &aiStub{execOut: "uid=0(root)"}
	extraction := mustJSON(t, map[string]any{
		"credentials": []map[string]string{
			{"name": "PostgreSQL superuser", "username": "bob", "password": "hunter2", "source": "/etc/shadow"},
			{"name": "duplicate", "username": "bob", "password": "hunter2", "source": "/tmp/x"},
			{"name": "placeholder", "username": "svc", "password": "changeme", "source": "/opt/.env"},
			{"name": "Alice netrc", "username": "alice", "password": "s3cret", "source": "/home/alice/.netrc"},
		},
	})
	review := `{"keep":[0,3],"dropped":[{"index":1,"reason":"same bob/hunter2 as entry 0"},{"index":2,"reason":"changeme is a placeholder"}]}`
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"id","reason":"who am I"}`,
		`{"done":true,"reason":"finished"}`,
		extraction,
		review,
	}}

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{
		SessionID:      "s-1",
		FilterFindings: true,
	})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if len(res.Findings) != 2 {
		t.Fatalf("Findings = %+v, want the two reviewed entries", res.Findings)
	}
	if len(res.Filtered) != 2 {
		t.Fatalf("Filtered = %+v, want the two dropped entries", res.Filtered)
	}
	if res.Filtered[0].Reason == "" || res.Filtered[1].Reason == "" {
		t.Errorf("Filtered = %+v, want a reason on every dropped entry", res.Filtered)
	}
	// Only the reviewed entries reach the vault: the duplicate and the
	// placeholder never become credentials.
	if len(res.Stored) != 2 {
		t.Fatalf("Stored = %+v, want the two reviewed credentials", res.Stored)
	}
	for _, s := range res.Stored {
		if s.Name == "duplicate" || s.Name == "placeholder" {
			t.Errorf("a dropped finding was stored: %+v", s)
		}
	}
}

// Filtering is a convenience: when the review call fails the run still files
// what extraction found, and says so.
func TestAICollectKeepsFindingsWhenFilterFails(t *testing.T) {
	stub := &aiStub{execOut: "uid=0(root)"}
	extraction := mustJSON(t, map[string]any{
		"credentials": []map[string]string{
			{"name": "PostgreSQL superuser", "username": "bob", "password": "hunter2", "source": "/etc/shadow"},
		},
	})
	p := &scriptedProvider{configured: true, replies: []string{
		`{"command":"id","reason":"who am I"}`,
		`{"done":true,"reason":"finished"}`,
		extraction,
		`not json at all`,
	}}

	res, err := collectClient(stub).AICollect(context.Background(), p, AICollectRequest{
		SessionID:      "s-1",
		FilterFindings: true,
	})
	if err != nil {
		t.Fatalf("AICollect: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("Findings = %+v, want the extracted entry kept after a failed review", res.Findings)
	}
	if len(res.Filtered) != 0 {
		t.Errorf("Filtered = %+v, want nothing dropped after a failed review", res.Filtered)
	}
	found := false
	for _, s := range res.Skipped {
		if strings.Contains(s, "filter pass failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("Skipped = %+v, want a note that the filter pass failed", res.Skipped)
	}
	if len(res.Stored) != 1 {
		t.Fatalf("Stored = %+v, want the extracted credential filed anyway", res.Stored)
	}
}
