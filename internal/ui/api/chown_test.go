package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"

	"sliverreshine/internal/ui/sliver"
)

// chownStub records the Chown request so the handler decoding can be checked
// end to end, and answers success.
type chownStub struct {
	rpcpb.SliverRPCClient
	req *sliverpb.ChownReq
}

func (s *chownStub) Chown(_ context.Context, in *sliverpb.ChownReq, _ ...grpc.CallOption) (*sliverpb.Chown, error) {
	s.req = in
	return &sliverpb.Chown{Response: &commonpb.Response{}}, nil
}

// The handler asks which platform the target runs before choosing between the
// Chown RPC and the Windows ACL path, so the stub has to answer the session
// list. It is empty here, which leaves the platform unresolved -- and an
// unresolved platform deliberately keeps the portable implementation, so these
// cases still exercise the RPC.
func (s *chownStub) GetSessions(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Sessions, error) {
	return &clientpb.Sessions{}, nil
}

func (s *chownStub) GetBeacons(_ context.Context, _ *commonpb.Empty, _ ...grpc.CallOption) (*clientpb.Beacons, error) {
	return &clientpb.Beacons{}, nil
}

// A hand-written JSON body sends uid and gid as numbers ("uid":0). Decoding
// them into strings refused the request with a message about a Go type, for a
// shape the API never documented; both spellings must reach the target as the
// same text. The named-user form ("www-data") is kept so the fix does not drop
// the string case.
func TestChownAcceptsNumericAndStringIDs(t *testing.T) {
	cases := []struct {
		name string
		body string
		uid  string
		gid  string
	}{
		{"numbers", "{\"path\":\"/tmp/a\",\"uid\":1000,\"gid\":0}", "1000", "0"},
		{"strings", "{\"path\":\"/tmp/a\",\"uid\":\"1000\",\"gid\":\"0\"}", "1000", "0"},
		{"names", "{\"path\":\"/tmp/a\",\"uid\":\"www-data\",\"gid\":\"www-data\"}", "www-data", "www-data"},
		{"empty", "{\"path\":\"/tmp/a\"}", "", ""},
	}
	for _, tc := range cases {
		stub := &chownStub{}
		srv := New()
		srv.SetClient(&sliver.Client{RPC: stub})
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/fs/chown", strings.NewReader(tc.body))
		prepareMutation(req)
		rec := httptest.NewRecorder()
		srv.Routes().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", tc.name, rec.Code, rec.Body.String())
		}
		if stub.req == nil {
			t.Fatalf("%s: the Chown RPC was never sent", tc.name)
		}
		if got := stub.req.GetUid(); got != tc.uid {
			t.Errorf("%s: uid = %q, want %q", tc.name, got, tc.uid)
		}
		if got := stub.req.GetGid(); got != tc.gid {
			t.Errorf("%s: gid = %q, want %q", tc.name, got, tc.gid)
		}
	}
}

// A uid that is neither a string nor a number is still refused, so the relaxed
// decoding does not accept arbitrary JSON.
func TestChownRejectsNonScalarIDs(t *testing.T) {
	stub := &chownStub{}
	srv := New()
	srv.SetClient(&sliver.Client{RPC: stub})
	req := httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/fs/chown",
		strings.NewReader("{\"path\":\"/tmp/a\",\"uid\":{\"n\":1}}"))
	prepareMutation(req)
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.req != nil {
		t.Fatal("a malformed uid still reached the target")
	}
}
