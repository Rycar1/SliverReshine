package sliver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// elevationStub drives ElevateToSystem: it answers GetPrivs, GetSystem and the
// session list, and can be told to start failing after a set number of calls.
type elevationStub struct {
	rpcStub

	integrity   string
	privsErr    error
	getSystemFn func() error
	sessionsFn  func() (*clientpb.Sessions, error)
}

func (s *elevationStub) GetPrivs(_ context.Context, _ *sliverpb.GetPrivsReq, _ ...grpc.CallOption) (*sliverpb.GetPrivs, error) {
	if s.privsErr != nil {
		return nil, s.privsErr
	}
	return &sliverpb.GetPrivs{
		ProcessIntegrity: s.integrity,
		Response:         &commonpb.Response{},
	}, nil
}

func (s *elevationStub) GetSystem(_ context.Context, _ *clientpb.GetSystemReq, _ ...grpc.CallOption) (*sliverpb.GetSystem, error) {
	if s.getSystemFn != nil {
		if err := s.getSystemFn(); err != nil {
			return nil, err
		}
	}
	return &sliverpb.GetSystem{Response: &commonpb.Response{}}, nil
}

func (s *elevationStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	return s.sessionsFn()
}

func TestIsElevatedIntegrity(t *testing.T) {
	cases := map[string]bool{
		"High":      true,
		"high":      true,
		" High ":    true,
		"Medium":    false,
		"Low":       false,
		"Untrusted": false,
		"Unknown":   false,
		"":          false,
	}
	for level, want := range cases {
		if got := IsElevatedIntegrity(level); got != want {
			t.Errorf("IsElevatedIntegrity(%q) = %v, want %v", level, got, want)
		}
	}
}

func TestSessionIntegrityReportsTheTokenLevel(t *testing.T) {
	c := &Client{RPC: &elevationStub{integrity: "Medium"}}
	got, err := c.SessionIntegrity("s-1")
	if err != nil {
		t.Fatalf("SessionIntegrity: %v", err)
	}
	if got != "Medium" {
		t.Errorf("integrity = %q, want Medium", got)
	}
}

func TestSessionIntegrityPropagatesFailure(t *testing.T) {
	c := &Client{RPC: &elevationStub{privsErr: errors.New("unknown message type")}}
	if _, err := c.SessionIntegrity("s-1"); err == nil {
		t.Fatal("a failing GetPrivs was reported as success")
	}
}

// GetSystem spawns a NEW session rather than elevating the current one, so the
// caller has to be handed the new ID. Returning the original would make the
// caller re-run the same failing command.
func TestElevateToSystemReturnsTheNewSession(t *testing.T) {
	created := false
	stub := &elevationStub{}
	stub.getSystemFn = func() error { created = true; return nil }
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		sessions := []*clientpb.Session{{ID: "s-1", Hostname: "web01"}}
		if created {
			sessions = append(sessions, &clientpb.Session{ID: "s-system", Hostname: "web01"})
		}
		return &clientpb.Sessions{Sessions: sessions}, nil
	}

	c := &Client{RPC: stub}
	got, err := c.ElevateToSystem("s-1", "", 5*elevationPollInterval)
	if err != nil {
		t.Fatalf("ElevateToSystem: %v", err)
	}
	if got != "s-system" {
		t.Errorf("session = %q, want the newly registered SYSTEM session", got)
	}
}

// A dead session appearing after the call must not be mistaken for the new one:
// GetSystem can produce a session that immediately drops.
func TestElevateToSystemIgnoresDeadSessions(t *testing.T) {
	created := false
	stub := &elevationStub{}
	stub.getSystemFn = func() error { created = true; return nil }
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		sessions := []*clientpb.Session{{ID: "s-1"}}
		if created {
			sessions = append(sessions, &clientpb.Session{ID: "s-dead", IsDead: true})
		}
		return &clientpb.Sessions{Sessions: sessions}, nil
	}

	c := &Client{RPC: stub}
	if _, err := c.ElevateToSystem("s-1", "", 3*elevationPollInterval); err == nil {
		t.Fatal("a dead session was accepted as the elevated one")
	}
}

// A GetSystem failure has to surface: the caller reports it rather than silently
// running the credential command on a token that cannot work.
func TestElevateToSystemPropagatesGetSystemFailure(t *testing.T) {
	stub := &elevationStub{}
	stub.getSystemFn = func() error { return errors.New("no suitable hosting process") }
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1"}}}, nil
	}

	c := &Client{RPC: stub}
	_, err := c.ElevateToSystem("s-1", "", elevationPollInterval)
	if err == nil {
		t.Fatal("a GetSystem failure was swallowed")
	}
	if !contains(err.Error(), "no suitable hosting process") {
		t.Errorf("error = %q, want the underlying cause", err)
	}
}

// Without a session snapshot the new session cannot be identified, so the call
// must fail rather than guess.
func TestElevateToSystemNeedsASnapshot(t *testing.T) {
	stub := &elevationStub{}
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		return nil, errors.New("server unreachable")
	}
	c := &Client{RPC: stub}
	if _, err := c.ElevateToSystem("s-1", "", elevationPollInterval); err == nil {
		t.Fatal("escalation proceeded without being able to identify the new session")
	}
}

// The escalation is skipped when the token is already high, so an operator does
// not accumulate a new SYSTEM session on every credential run.
func TestEscalateForMimikatzSkipsWhenAlreadyElevated(t *testing.T) {
	stub := &elevationStub{integrity: "High"}
	stub.getSystemFn = func() error { t.Error("GetSystem was called on an elevated token"); return nil }
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		return &clientpb.Sessions{}, nil
	}

	c := &Client{RPC: stub}
	result := &MimikatzResult{}
	if note := c.escalateForMimikatz(harvestTarget{SessionID: "s-1"}, "", result); note != "" {
		t.Errorf("note = %q, want empty for an already-elevated token", note)
	}
	if result.Elevated {
		t.Error("result was marked elevated without escalating")
	}
	if result.Integrity != "High" {
		t.Errorf("Integrity = %q, want High", result.Integrity)
	}
}

// The failure path is what the operator reads when mimikatz prints an
// access-denied, so it has to name the level and say the run continued.
func TestEscalateForMimikatzExplainsAFailedEscalation(t *testing.T) {
	stub := &elevationStub{integrity: "Medium"}
	stub.getSystemFn = func() error { return errors.New("SeDebugPrivilege not held") }
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1"}}}, nil
	}

	c := &Client{RPC: stub}
	result := &MimikatzResult{}
	note := c.escalateForMimikatz(harvestTarget{SessionID: "s-1"}, "", result)
	if note == "" {
		t.Fatal("a failed escalation produced no explanation")
	}
	for _, want := range []string{"Medium", "SeDebugPrivilege not held", "vault::cred"} {
		if !contains(note, want) {
			t.Errorf("note %q does not mention %q", note, want)
		}
	}
	if result.Elevated {
		t.Error("result was marked elevated even though escalation failed")
	}
}

// When the integrity cannot be read, the run proceeds but says so.
//
// This test used to assert the opposite -- that the note stays empty, on the
// reasoning that an unreadable integrity is not the operator's problem. That
// reasoning is what hid a real defect for as long as it did: on a beacon the
// read fails every single time, and saying nothing left the operator to read
// mimikatz's "LSA access was denied" as a statement about their own token.
//
// The run is still best-effort, and that half of the old test is kept.
func TestEscalateForMimikatzReportsAnUnreadableIntegrity(t *testing.T) {
	c := &Client{RPC: &elevationStub{privsErr: errors.New("unknown message type")}}
	result := &MimikatzResult{}

	note := c.escalateForMimikatz(harvestTarget{SessionID: "s-1"}, "", result)

	if note == "" {
		t.Fatal("an unreadable integrity produced no note; the operator would be left guessing")
	}
	// The note has to name the cause, say what was skipped, and point at the
	// alternative that does work -- otherwise it is a second puzzle.
	for _, want := range []string{"unknown message type", "skipped", "vault::cred"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not mention %q: %s", want, note)
		}
	}
	if result.Elevated {
		t.Error("result was marked elevated without escalating")
	}
}

// A successful escalation reports the new session so the caller can switch to it.
func TestEscalateForMimikatzSwitchesToTheSystemSession(t *testing.T) {
	created := false
	stub := &elevationStub{integrity: "Medium"}
	stub.getSystemFn = func() error { created = true; return nil }
	stub.sessionsFn = func() (*clientpb.Sessions, error) {
		sessions := []*clientpb.Session{{ID: "s-1"}}
		if created {
			sessions = append(sessions, &clientpb.Session{ID: "s-system"})
		}
		return &clientpb.Sessions{Sessions: sessions}, nil
	}

	c := &Client{RPC: stub}
	result := &MimikatzResult{}
	note := c.escalateForMimikatz(harvestTarget{SessionID: "s-1"}, "", result)
	if !result.Elevated {
		t.Fatalf("escalation did not report success (note %q)", note)
	}
	if result.SessionID != "s-system" {
		t.Errorf("SessionID = %q, want s-system", result.SessionID)
	}
	if !contains(note, "Medium") {
		t.Errorf("note %q does not record the level escalated from", note)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
