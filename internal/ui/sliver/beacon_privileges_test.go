package sliver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// The credential harvest used to be unreachable on a beacon, and said nothing
// about it. These tests pin both halves: that a beacon is now asked the right
// way, and that any step which cannot be taken reports why.
//
// The bug was invisible in the test suite because every existing test drove a
// *session*. GetPrivs resolves a SessionID through the server's session table,
// a beacon ID is not in it, so on a beacon the call returned InvalidSessionID --
// and escalateForMimikatz treated any error as "nothing useful to say" and
// continued with the original token. The operator then saw mimikatz's own
// "LSA access was denied", which reads as a statement about their permissions.

// beaconStub answers the beacon-shaped calls and records how they were made.
type beaconStub struct {
	rpcpb.SliverRPCClient

	getPrivsReq  *sliverpb.GetPrivsReq
	getSystemReq *clientpb.GetSystemReq
	beaconTasks  []*clientpb.BeaconTask
	sessions     []*clientpb.Session
	beacons      []*clientpb.Beacon

	privsErr error
}

func (s *beaconStub) GetPrivs(_ context.Context, in *sliverpb.GetPrivsReq, _ ...grpc.CallOption) (*sliverpb.GetPrivs, error) {
	s.getPrivsReq = in
	if s.privsErr != nil {
		return nil, s.privsErr
	}
	return &sliverpb.GetPrivs{Response: &commonpb.Response{}}, nil
}

func (s *beaconStub) GetSystem(_ context.Context, in *clientpb.GetSystemReq, _ ...grpc.CallOption) (*sliverpb.GetSystem, error) {
	s.getSystemReq = in
	return &sliverpb.GetSystem{Response: &commonpb.Response{}}, nil
}

func (s *beaconStub) GetBeaconTasks(_ context.Context, _ *clientpb.Beacon, _ ...grpc.CallOption) (*clientpb.BeaconTasks, error) {
	return &clientpb.BeaconTasks{Tasks: s.beaconTasks}, nil
}

func (s *beaconStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	return &clientpb.Sessions{Sessions: s.sessions}, nil
}

func (s *beaconStub) GetBeacons(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Beacons, error) {
	return &clientpb.Beacons{Beacons: s.beacons}, nil
}

// The heart of the fix: a beacon's GetPrivs must carry Async and BeaconID. Sent
// as a plain session request it resolves against a table the beacon is not in.
func TestBeaconIntegrityUsesAsyncAndBeaconID(t *testing.T) {
	stub := &beaconStub{
		beaconTasks: []*clientpb.BeaconTask{{
			Description: "GetPrivs",
			State:       "completed",
			Response:    []byte("High"),
		}},
	}
	c := &Client{RPC: stub}

	level, err := c.BeaconIntegrity("beacon-123", 5*time.Second)
	if err != nil {
		t.Fatalf("BeaconIntegrity: %v", err)
	}
	if level != "High" {
		t.Errorf("integrity = %q, want High", level)
	}

	if stub.getPrivsReq == nil {
		t.Fatal("GetPrivs was never called")
	}
	req := stub.getPrivsReq.GetRequest()
	if !req.GetAsync() {
		t.Error("Async was not set; the server would look for a live session and fail")
	}
	if req.GetBeaconID() != "beacon-123" {
		t.Errorf("BeaconID = %q, want beacon-123", req.GetBeaconID())
	}
	if req.GetSessionID() != "" {
		t.Errorf("SessionID = %q, want empty: a beacon has none, and setting one is what broke this",
			req.GetSessionID())
	}
}

// A beacon does not answer until it checks in, so "not yet" must not be
// mistaken for "no".
func TestBeaconIntegrityWaitsForTheTaskToComplete(t *testing.T) {
	stub := &beaconStub{
		beaconTasks: []*clientpb.BeaconTask{{
			Description: "GetPrivs",
			State:       "pending",
		}},
	}
	c := &Client{RPC: stub}

	// A short wait so the test does not sit for the production interval.
	_, err := c.BeaconIntegrity("b1", 120*time.Millisecond)
	if err == nil {
		t.Fatal("a pending task was accepted as an answer")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("the error should say the beacon did not answer, got: %v", err)
	}
}

// GetSystem against a beacon is queued the same way, and targets a new session
// rather than the beacon.
func TestBeaconElevateQueuesGetSystemWithBeaconID(t *testing.T) {
	stub := &beaconStub{}
	c := &Client{RPC: stub}

	// Nothing new appears, so this times out -- which is the assertion: it timed
	// out waiting for a session, not failing on the request.
	_, err := c.BeaconElevateToSystem("beacon-9", "", 120*time.Millisecond)
	if err == nil {
		t.Fatal("expected a timeout when no SYSTEM session appears")
	}
	if !strings.Contains(err.Error(), "no SYSTEM session appeared") {
		t.Errorf("unexpected error: %v", err)
	}

	if stub.getSystemReq == nil {
		t.Fatal("GetSystem was never called")
	}
	req := stub.getSystemReq.GetRequest()
	if !req.GetAsync() || req.GetBeaconID() != "beacon-9" {
		t.Errorf("GetSystem request was not beacon-shaped: async=%v beaconID=%q",
			req.GetAsync(), req.GetBeaconID())
	}
	if name := stub.getSystemReq.GetConfig().GetHTTPC2ConfigName(); name == "" {
		t.Error("no HTTP C2 profile was named, so the SYSTEM implant would have nothing to call home to")
	}
}

// The silent failure that hid all of this. When the integrity cannot be read the
// message must say the escalation was skipped and why -- not leave the operator
// to conclude their token is fine.
func TestEscalationFailureIsReported(t *testing.T) {
	stub := &beaconStub{privsErr: errors.New("rpc error: code = InvalidArgument desc = Invalid beacon ID")}
	c := &Client{RPC: stub}

	result := &MimikatzResult{}
	note := c.escalateForMimikatz(harvestTarget{BeaconID: "b1"}, "", result)

	if note == "" {
		t.Fatal("the escalation failure was reported as an empty note, which is the original bug")
	}
	for _, want := range []string{"escalation", "skipped", "Invalid beacon ID"} {
		if !strings.Contains(note, want) {
			t.Errorf("the note does not mention %q: %s", want, note)
		}
	}
	if result.Elevated {
		t.Error("Elevated was set even though nothing was escalated")
	}
}

// An already-elevated target needs no escalation and no note.
func TestEscalationIsQuietWhenAlreadyElevated(t *testing.T) {
	stub := &beaconStub{
		beaconTasks: []*clientpb.BeaconTask{{
			Description: "GetPrivs",
			State:       "completed",
			Response:    []byte("High"),
		}},
	}
	c := &Client{RPC: stub}

	result := &MimikatzResult{}
	note := c.escalateForMimikatz(harvestTarget{BeaconID: "b1"}, "", result)

	if note != "" {
		t.Errorf("an elevated target produced a note: %s", note)
	}
	if result.Integrity != "High" {
		t.Errorf("integrity was not recorded: %q", result.Integrity)
	}
}

// The target type has to name itself correctly, because the message it produces
// is what the operator reads.
func TestHarvestTargetDescribesItself(t *testing.T) {
	if got := (harvestTarget{SessionID: "s1"}).String(); got != "session s1" {
		t.Errorf("= %q", got)
	}
	if got := (harvestTarget{BeaconID: "b1"}).String(); got != "beacon b1" {
		t.Errorf("= %q", got)
	}
	if !(harvestTarget{BeaconID: "b1"}).isBeacon() {
		t.Error("a beacon target did not identify as one")
	}
	if (harvestTarget{SessionID: "s1"}).isBeacon() {
		t.Error("a session target identified as a beacon")
	}
}

// ResolveTarget must not guess from the string. These IDs are shaped the same on
// purpose: the answer has to come from the lookup.
func TestResolveTargetLooksUpRatherThanGuesses(t *testing.T) {
	stub := &beaconStub{
		sessions: []*clientpb.Session{{ID: "abc", Hostname: "host-1"}},
	}
	c := &Client{RPC: stub}

	got, err := c.ResolveTarget("abc")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if got.isBeacon() {
		t.Error("a session ID resolved to a beacon")
	}

	stub.beacons = []*clientpb.Beacon{{ID: "zzz", Name: "beacon-1"}}
	gotBeacon, err := c.ResolveTarget("zzz")
	if err != nil {
		t.Fatalf("ResolveTarget(beacon): %v", err)
	}
	if !gotBeacon.isBeacon() {
		t.Error("a beacon ID resolved to a session")
	}
	if _, err := c.ResolveTarget("nope"); err == nil {
		t.Error("an unknown ID resolved to something instead of failing")
	}
	if _, err := c.ResolveTarget(""); err == nil {
		t.Error("an empty ID resolved to something")
	}
}
