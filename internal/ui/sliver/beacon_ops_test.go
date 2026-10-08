package sliver

import (
	"context"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

func TestBeaconTaskToView(t *testing.T) {
	v := beaconTaskToView(&clientpb.BeaconTask{
		ID:          "task-1",
		BeaconID:    "beacon-1",
		CreatedAt:   1700000000,
		State:       "completed",
		CompletedAt: 1700000060,
		Description: "exec /bin/whoami",
		Response:    []byte("root"),
	})
	if v == nil {
		t.Fatal("nil view")
	}
	if v.ID != "task-1" || v.BeaconID != "beacon-1" || v.State != "completed" {
		t.Fatalf("unexpected view: %+v", v)
	}
	if v.ResponseB64 != "cm9vdA==" {
		t.Fatalf("ResponseB64 = %q, want cm9vdA==", v.ResponseB64)
	}
}

func TestBeaconTaskToView_Nil(t *testing.T) {
	if beaconTaskToView(nil) != nil {
		t.Fatal("nil input should produce nil view")
	}
}

func TestSocksProxyView_StartValidation(t *testing.T) {
	sm := NewSocksManager(nil)
	if _, err := sm.Start("", "127.0.0.1", 0, "", ""); err == nil {
		t.Fatal("empty session id should error")
	}
}

func TestItoa(t *testing.T) {
	cases := map[uint32]string{0: "0", 1: "1", 1080: "1080", 65535: "65535"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Fatalf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}

// openSessionStub captures the OpenSession request so a test can assert the
// fields that decide sync vs async on the server. The embedded interface is
// nil, so any other RPC a test drives panics instead of returning a zero value.
type openSessionStub struct {
	rpcpb.SliverRPCClient
	got *sliverpb.OpenSession
}

func (s *openSessionStub) OpenSession(_ context.Context, in *sliverpb.OpenSession, _ ...grpc.CallOption) (*sliverpb.OpenSession, error) {
	s.got = in
	return &sliverpb.OpenSession{Response: &commonpb.Response{Async: true}}, nil
}

// A beacon has no live session to look up, so the OpenSession request has to be
// marked async and carry the beacon ID. Sent without Async -- which is what the
// code did -- the server takes the synchronous path, looks up an empty session
// ID, finds nothing and answers "Invalid session ID", so a beacon's open-session
// button could never work.
func TestOpenSessionFromBeaconSendsAnAsyncRequest(t *testing.T) {
	stub := &openSessionStub{}
	c := &Client{RPC: stub}

	async, err := c.OpenSessionFromBeacon("beacon-1")
	if err != nil {
		t.Fatalf("OpenSessionFromBeacon: %v", err)
	}
	if !async {
		t.Fatal("async = false, want true")
	}
	if stub.got == nil || stub.got.Request == nil {
		t.Fatal("no request was sent")
	}
	if !stub.got.Request.Async {
		t.Error("Request.Async = false; the server then looks for a session and fails")
	}
	if stub.got.Request.BeaconID != "beacon-1" {
		t.Errorf("Request.BeaconID = %q, want beacon-1", stub.got.Request.BeaconID)
	}
	if stub.got.Request.Timeout <= 0 {
		t.Errorf("Request.Timeout = %d, want a positive timeout", stub.got.Request.Timeout)
	}
}
