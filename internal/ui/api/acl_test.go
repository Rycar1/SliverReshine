package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"

	"sliverreshine/internal/ui/sliver"
)

// aclStub answers the platform lookup and the icacls spawn that the Windows
// branch of the ACL handler reaches.
type aclStub struct {
	rpcpb.SliverRPCClient
	os      string
	execWin func(*sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error)
	calls   int
}

func (s *aclStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	if s.os == "" {
		return &clientpb.Sessions{}, nil
	}
	return &clientpb.Sessions{Sessions: []*clientpb.Session{{ID: "s-1", OS: s.os, Arch: "amd64"}}}, nil
}

func (s *aclStub) GetBeacons(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Beacons, error) {
	return &clientpb.Beacons{}, nil
}

func (s *aclStub) ExecuteWindows(_ context.Context, in *sliverpb.ExecuteWindowsReq, _ ...grpc.CallOption) (*sliverpb.Execute, error) {
	s.calls++
	if s.execWin == nil {
		return nil, context.Canceled
	}
	return s.execWin(in)
}

func postACL(t *testing.T, stub *aclStub, body string) *httptest.ResponseRecorder {
	t.Helper()
	srv := New()
	srv.SetClient(&sliver.Client{RPC: stub})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/fs/acl", strings.NewReader(body))
	prepareMutation(req)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)
	return rec
}

// The account and the level are both required: icacls has nothing sensible to
// do with a grant that names neither, and a half-built command is worse than a
// refusal.
func TestGrantACLRequiresEveryField(t *testing.T) {
	cases := map[string]string{
		"no path":      `{"principal":"Users","perm":"F"}`,
		"no account":   `{"path":"C:\\tmp\\a.txt","perm":"F"}`,
		"no level":     `{"path":"C:\\tmp\\a.txt","principal":"Users"}`,
		"empty object": `{}`,
	}
	for name, body := range cases {
		stub := &aclStub{os: "windows"}
		rec := postACL(t, stub, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
		if stub.calls != 0 {
			t.Errorf("%s: icacls ran for a request that was missing a field", name)
		}
	}
}

// The handler is the only place the account and the level are joined into the
// icacls argument, so the join is checked here rather than only in the backend.
func TestGrantACLPassesTheAccountAndLevelThrough(t *testing.T) {
	var got *sliverpb.ExecuteWindowsReq
	stub := &aclStub{
		os: "windows",
		execWin: func(in *sliverpb.ExecuteWindowsReq) (*sliverpb.Execute, error) {
			got = in
			return &sliverpb.Execute{Response: &commonpb.Response{}}, nil
		},
	}
	rec := postACL(t, stub, `{"path":"C:\\tmp\\a.txt","principal":"BUILTIN\\Users","perm":"RX","recursive":true}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got == nil {
		t.Fatal("icacls was never run")
	}
	args := got.GetArgs()
	if len(args) != 4 || args[0] != `C:\tmp\a.txt` || args[1] != "/grant:r" ||
		args[2] != `BUILTIN\Users:RX` || args[3] != "/T" {
		t.Fatalf("icacls argv = %q", args)
	}
}

// A POSIX target has no ACL entry to grant, so the request is refused with a
// sentence that points at chmod instead of running icacls where it does not
// exist.
func TestGrantACLRefusesAPosixTarget(t *testing.T) {
	stub := &aclStub{os: "linux"}
	rec := postACL(t, stub, `{"path":"/tmp/a","principal":"root","perm":"F"}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("an ACL grant was accepted for a Linux target: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "chmod") {
		t.Errorf("body = %q, want it to point at chmod", rec.Body.String())
	}
	if stub.calls != 0 {
		t.Error("a command ran on a target the operation does not apply to")
	}
}
