package sliver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
)

// rmStub records the Rm calls cleanupStagedFile issues and can be told to fail.
type rmStub struct {
	rpcpb.SliverRPCClient
	calls []*sliverpb.RmReq
	err   error
}

func (s *rmStub) Rm(_ context.Context, in *sliverpb.RmReq, _ ...grpc.CallOption) (*sliverpb.Rm, error) {
	s.calls = append(s.calls, in)
	if s.err != nil {
		return nil, s.err
	}
	return &sliverpb.Rm{Response: &commonpb.Response{}}, nil
}

// A staged mimikatz binary left behind on the target is the failure this guards.
// The cleanup used to be silent, so a failed delete was indistinguishable from a
// successful one and the tool stayed in the target's temp directory.
func TestCleanupStagedFileReportsAFailedDelete(t *testing.T) {
	const staged = `C:\Windows\Temp\mimi.exe`
	stub := &rmStub{err: errors.New("access denied")}
	c := &Client{RPC: stub}

	note := c.cleanupStagedFile("s-1", staged)
	if note == "" {
		t.Fatal("a failed delete must produce a note, not silence")
	}
	if !strings.Contains(note, staged) {
		t.Errorf("the note must name the file that is still there, got %q", note)
	}
	if !strings.Contains(note, "access denied") {
		t.Errorf("the note must carry the reason, got %q", note)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("want exactly one delete attempt, got %d", len(stub.calls))
	}
	if stub.calls[0].GetRecursive() {
		t.Error("a staged file must be deleted non-recursively")
	}
	if !stub.calls[0].GetForce() {
		t.Error("the delete must be forced: the staged file may be read-only")
	}
}

func TestCleanupStagedFileIsSilentWhenTheDeleteSucceeds(t *testing.T) {
	stub := &rmStub{}
	c := &Client{RPC: stub}

	if note := c.cleanupStagedFile("s-1", `C:\Windows\Temp\mimi.exe`); note != "" {
		t.Fatalf("a successful delete must not produce a note, got %q", note)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("want one delete attempt, got %d", len(stub.calls))
	}
	if got := stub.calls[0].GetRequest().GetSessionID(); got != "s-1" {
		t.Fatalf("session id = %q, want s-1", got)
	}
}

// An empty path means nothing was staged (the in-memory route), so no RPC may be
// issued: deleting "" would target the session's working directory.
func TestCleanupStagedFileDoesNothingWithoutAPath(t *testing.T) {
	stub := &rmStub{}
	c := &Client{RPC: stub}

	if note := c.cleanupStagedFile("s-1", ""); note != "" {
		t.Fatalf("want no note for an empty path, got %q", note)
	}
	if len(stub.calls) != 0 {
		t.Fatalf("want no RPC for an empty path, got %d", len(stub.calls))
	}
}
