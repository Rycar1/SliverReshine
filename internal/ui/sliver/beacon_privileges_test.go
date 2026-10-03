package sliver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// The credential harvest used to be unreachable on a beacon, and said nothing
// about it. These tests pin the behaviour.
//
// The stub is deliberately shaped like the REAL server, because the earlier
// versions of these tests passed while the production path could not work at
// all. Three separate assumptions had to be corrected, and each was a lie the
// stub was telling:
//
//  1. The task LIST carries no Request/Response -- the server maps rows through
//     models.BeaconTask.ToProtobuf(false). The answer must come from
//     GetBeaconTaskContent, so the stub's list omits the payload.
//  2. Task.Description is the protobuf MESSAGE name, so it is "GetPrivsReq", not
//     "GetPrivs". A stub saying "GetPrivs" hides that a matcher on the command
//     name never fires.
//  3. The task Response is a MARSHALLED sliverpb.GetPrivs, not a string. A stub
//     putting "High" in as text hides that the integrity lives in a field.
//
// The implementation no longer matches on any of those -- it correlates by the
// TaskID the server returns -- but the stub still models them, so a future
// change back toward matching on the list or the description fails here rather
// than on an operator's beacon.

type beaconStub struct {
	rpcpb.SliverRPCClient

	getPrivsReq  *sliverpb.GetPrivsReq
	getSystemReq *clientpb.GetSystemReq
	beaconTasks  []*clientpb.BeaconTask
	sessions     []*clientpb.Session
	beacons      []*clientpb.Beacon

	// contentByID holds the raw response bytes that only GetBeaconTaskContent
	// may return, keyed by task ID.
	contentByID map[string][]byte

	// nextTaskID is handed out by the GetPrivs call, modelling the server's
	// asyncGenericHandler, which sets Response.TaskID on the queued request.
	nextTaskID string
	// privsResponse is marshalled into the new task's content.
	privsResponse *sliverpb.GetPrivs
	// taskState is the state the created task reports; "" means completed.
	taskState string
	// taskErr is placed in the response's Error field when set.
	taskErr string

	privsErr error
}

// taskList returns the tasks as the real list RPC would: descriptions and
// states, but no request or response payload.
func (s *beaconStub) taskList() []*clientpb.BeaconTask {
	out := make([]*clientpb.BeaconTask, 0, len(s.beaconTasks))
	for _, t := range s.beaconTasks {
		out = append(out, &clientpb.BeaconTask{
			ID:          t.ID,
			BeaconID:    t.BeaconID,
			State:       t.State,
			Description: t.Description,
			// Request and Response deliberately omitted, as the server does.
		})
	}
	return out
}

func (s *beaconStub) GetPrivs(_ context.Context, in *sliverpb.GetPrivsReq, _ ...grpc.CallOption) (*sliverpb.GetPrivs, error) {
	s.getPrivsReq = in
	if s.privsErr != nil {
		return nil, s.privsErr
	}

	resp := &sliverpb.GetPrivs{Response: &commonpb.Response{}}
	if s.nextTaskID != "" {
		resp.Response.TaskID = s.nextTaskID

		// A real beacon acts only on its next check-in, so the task did not
		// exist when the request was sent. Creating it here is what gives the
		// poll something to find.
		state := s.taskState
		if state == "" {
			state = "completed"
		}
		s.beaconTasks = append(s.beaconTasks, &clientpb.BeaconTask{
			ID:       s.nextTaskID,
			BeaconID: "beacon-123",
			// The real value: the protobuf message name, not the command name.
			Description: "GetPrivsReq",
			State:       state,
		})
		if s.contentByID == nil {
			s.contentByID = map[string][]byte{}
		}
		inner := s.privsResponse
		if inner == nil {
			inner = &sliverpb.GetPrivs{}
		}
		if inner.Response == nil {
			inner.Response = &commonpb.Response{}
		}
		inner.Response.Err = s.taskErr
		raw, _ := proto.Marshal(inner)
		s.contentByID[s.nextTaskID] = raw
	}
	return resp, nil
}

func (s *beaconStub) GetSystem(_ context.Context, in *clientpb.GetSystemReq, _ ...grpc.CallOption) (*sliverpb.GetSystem, error) {
	s.getSystemReq = in
	return &sliverpb.GetSystem{Response: &commonpb.Response{}}, nil
}

func (s *beaconStub) GetBeaconTasks(_ context.Context, _ *clientpb.Beacon, _ ...grpc.CallOption) (*clientpb.BeaconTasks, error) {
	return &clientpb.BeaconTasks{Tasks: s.taskList()}, nil
}

// GetBeaconTaskContent is the only call that returns the payload.
func (s *beaconStub) GetBeaconTaskContent(_ context.Context, in *clientpb.BeaconTask, _ ...grpc.CallOption) (*clientpb.BeaconTask, error) {
	for _, t := range s.beaconTasks {
		if t.ID != in.ID {
			continue
		}
		return &clientpb.BeaconTask{
			ID:          t.ID,
			BeaconID:    t.BeaconID,
			State:       t.State,
			Description: t.Description,
			Response:    s.contentByID[t.ID],
		}, nil
	}
	return nil, fmt.Errorf("no task with id %q", in.ID)
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
		nextTaskID:    "task-1",
		privsResponse: &sliverpb.GetPrivs{ProcessIntegrity: "High"},
	}
	c := &Client{RPC: stub}

	level, err := c.BeaconIntegrity("beacon-123", 10*time.Second)
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

// The integrity is a FIELD of a marshalled protobuf. Reading the response as
// text yields control bytes and never equals "High", so this asserts on the
// decoded field rather than on a substring of the raw bytes.
func TestBeaconIntegrityDecodesTheProtobufResponse(t *testing.T) {
	stub := &beaconStub{
		nextTaskID:    "task-1",
		privsResponse: &sliverpb.GetPrivs{ProcessIntegrity: "High", ProcessName: "sliverreshine.exe"},
	}
	c := &Client{RPC: stub}

	level, err := c.BeaconIntegrity("beacon-123", 10*time.Second)
	if err != nil {
		t.Fatalf("BeaconIntegrity: %v", err)
	}
	if level != "High" {
		t.Errorf("integrity = %q, want High", level)
	}
	if strings.ContainsAny(level, "\x00\x12\x1a") {
		t.Errorf("integrity %q contains protobuf framing, so it was not decoded", level)
	}
}

// A previous run leaves its task in the list forever. Correlating by TaskID is
// what keeps its answer from being returned as this run's.
func TestBeaconIntegrityIgnoresAnEarlierCompletedTask(t *testing.T) {
	old, _ := proto.Marshal(&sliverpb.GetPrivs{
		ProcessIntegrity: "Medium",
		Response:         &commonpb.Response{},
	})
	stub := &beaconStub{
		beaconTasks: []*clientpb.BeaconTask{{
			ID:          "task-old",
			Description: "GetPrivsReq",
			State:       "completed",
		}},
		contentByID:   map[string][]byte{"task-old": old},
		nextTaskID:    "task-new",
		privsResponse: &sliverpb.GetPrivs{ProcessIntegrity: "High"},
	}
	c := &Client{RPC: stub}

	level, err := c.BeaconIntegrity("beacon-123", 10*time.Second)
	if err != nil {
		t.Fatalf("BeaconIntegrity: %v", err)
	}
	if level != "High" {
		t.Errorf("integrity = %q, want High: the stale task-old answer was returned", level)
	}
}

// The server must hand back a task to poll. Without one there is nothing to wait
// for, and reporting a timeout would misdescribe it.
func TestBeaconIntegrityReportsAMissingTaskID(t *testing.T) {
	stub := &beaconStub{} // nextTaskID empty, so no task is created
	c := &Client{RPC: stub}

	_, err := c.BeaconIntegrity("beacon-123", 150*time.Millisecond)
	if err == nil {
		t.Fatal("a response with no task ID was accepted")
	}
	if !strings.Contains(err.Error(), "no task ID") {
		t.Errorf("the error should say no task was returned, got: %v", err)
	}
}

// A beacon does not answer until it checks in, so "not yet" must not be
// mistaken for "no".
func TestBeaconIntegrityWaitsForTheTaskToComplete(t *testing.T) {
	stub := &beaconStub{
		nextTaskID:    "task-pending",
		taskState:     "pending",
		privsResponse: &sliverpb.GetPrivs{ProcessIntegrity: "High"},
	}
	c := &Client{RPC: stub}

	_, err := c.BeaconIntegrity("beacon-123", 150*time.Millisecond)
	if err == nil {
		t.Fatal("a pending task was accepted as an answer")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("the error should say the beacon did not answer, got: %v", err)
	}
}

// Beacon escalation cannot work: the server's GetSystem has no beacon path. The
// call must report that rather than spend the operator's wait on a request that
// is certain to be refused.
func TestBeaconElevationReportsUnsupportedWithoutCallingTheServer(t *testing.T) {
	stub := &beaconStub{}
	c := &Client{RPC: stub}

	_, err := c.BeaconElevateToSystem("beacon-9", "", 100*time.Millisecond)
	if err == nil {
		t.Fatal("beacon elevation reported success")
	}
	if stub.getSystemReq != nil {
		t.Error("GetSystem was called for a beacon, which the server always refuses")
	}
	// The message has to name the way out, or it is just a dead end.
	for _, want := range []string{"beacon", "session", "getsystem"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not mention %q: %v", want, err)
		}
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
		nextTaskID:    "task-1",
		privsResponse: &sliverpb.GetPrivs{ProcessIntegrity: "High"},
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

// ResolveTarget must not guess from the string. The two ID spaces are not
// promised to be distinguishable by shape, so the answer comes from a lookup.
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

// The list must genuinely omit the payload, or these tests would pass against a
// server that does not exist. This asserts the stub's own fidelity.
func TestBeaconStubModelsTheListWithoutContent(t *testing.T) {
	stub := &beaconStub{
		beaconTasks: []*clientpb.BeaconTask{{
			ID: "t1", Description: "GetPrivsReq", State: "completed",
		}},
		contentByID: map[string][]byte{"t1": []byte("payload")},
	}

	list, err := stub.GetBeaconTasks(context.Background(), &clientpb.Beacon{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(list.Tasks))
	}
	if len(list.Tasks[0].Response) != 0 {
		t.Error("the stub's list carries a response; the real list does not, so a test " +
			"relying on it would pass while production fails")
	}
	if list.Tasks[0].Description != "GetPrivsReq" {
		t.Errorf("Description = %q; the real server sets the protobuf message name",
			list.Tasks[0].Description)
	}

	content, err := stub.GetBeaconTaskContent(context.Background(), &clientpb.BeaconTask{ID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if base64.StdEncoding.EncodeToString(content.Response) != "cGF5bG9hZA==" {
		t.Error("the content call did not return the payload, so nothing could ever be read")
	}
}

// The response field is base64 in the view and a protobuf once decoded. Both
// steps have to work, and neither failure should be mistaken for "not ready".
func TestBeaconTaskResponseBytesHandlesBadInput(t *testing.T) {
	if _, err := beaconTaskResponseBytes(nil); err != nil {
		t.Errorf("a nil view should not be an error: %v", err)
	}
	if raw, err := beaconTaskResponseBytes(&BeaconTaskView{}); err != nil || raw != nil {
		t.Errorf("an empty response should give no bytes and no error, got %v / %v", raw, err)
	}
	if _, err := beaconTaskResponseBytes(&BeaconTaskView{ResponseB64: "!!!not base64!!!"}); err == nil {
		t.Error("undecodable base64 was accepted; the caller would parse garbage")
	}

	raw, err := beaconTaskResponseBytes(&BeaconTaskView{
		ResponseB64: base64.StdEncoding.EncodeToString([]byte("hello")),
	})
	if err != nil || string(raw) != "hello" {
		t.Errorf("a valid response did not round-trip: %q / %v", raw, err)
	}
}

// A task that completed with an implant error must be distinguishable from one
// that is still running, or the caller waits out the whole deadline for a
// question that was already answered.
//
// The error lives in the embedded commonpb.Response of the typed payload, so a
// test that marshals a bare Response would be asserting against a shape the
// server never sends.
func TestBeaconIntegritySurfacesATaskError(t *testing.T) {
	stub := &beaconStub{
		nextTaskID: "task-err",
		taskErr:    "access denied",
		// A zero ProcessIntegrity: the point is that the error, not an empty
		// value, is what the caller reports.
		privsResponse: &sliverpb.GetPrivs{},
	}
	c := &Client{RPC: stub}

	_, err := c.BeaconIntegrity("beacon-123", 10*time.Second)
	if err == nil {
		t.Fatal("a failed task was treated as a pending one")
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("the implant's error was not surfaced: %v", err)
	}
	// The message must not be a timeout: the target answered.
	if strings.Contains(err.Error(), "did not answer") {
		t.Errorf("a completed-but-failed task was reported as a timeout: %v", err)
	}
}

// Nothing new must never be answered with an old value. This is the case that a
// description matcher gets wrong: the previous task is still completed, so it
// looks like a valid answer.
func TestBeaconIntegrityNeverReusesAPriorAnswer(t *testing.T) {
	old, _ := proto.Marshal(&sliverpb.GetPrivs{
		ProcessIntegrity: "High",
		Response:         &commonpb.Response{},
	})
	stub := &beaconStub{
		beaconTasks: []*clientpb.BeaconTask{{
			ID: "task-old", Description: "GetPrivsReq", State: "completed",
		}},
		contentByID: map[string][]byte{"task-old": old},
		// No nextTaskID, so this run queues nothing new.
	}
	c := &Client{RPC: stub}

	_, err := c.BeaconIntegrity("beacon-123", 150*time.Millisecond)
	if err == nil {
		t.Fatal("a previous run's answer was returned as this run's; it would look fresh")
	}
}
