package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"google.golang.org/grpc"

	"sliverreshine/internal/ui/sliver"
)

// --- paginate, as a unit ---------------------------------------------------

func paginateFor(t *testing.T, target string, items []int) ([]int, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	got, ok := paginate(rec, httptest.NewRequest(http.MethodGet, target, nil), items)
	if !ok {
		return nil, rec
	}
	return got, rec
}

func TestPaginateDefaultsToEverything(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	got, rec := paginateFor(t, "/api/creds", items)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the caller to answer normally", rec.Code)
	}
	if len(got) != len(items) {
		t.Fatalf("len = %d, want %d: absent limit/offset must not change the response", len(got), len(items))
	}
}

func TestPaginateSlices(t *testing.T) {
	items := []int{1, 2, 3, 4, 5}
	cases := []struct {
		target string
		want   []int
	}{
		{"/api/creds?limit=2", []int{1, 2}},
		{"/api/creds?offset=3", []int{4, 5}},
		{"/api/creds?offset=1&limit=2", []int{2, 3}},
		{"/api/creds?limit=0", []int{}},
		{"/api/creds?offset=99", []int{}},
		{"/api/creds?offset=5", []int{}},
	}
	for _, tc := range cases {
		got, rec := paginateFor(t, tc.target, items)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", tc.target, rec.Code)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: len = %d (%v), want %d", tc.target, len(got), got, len(tc.want))
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: got %v, want %v", tc.target, got, tc.want)
				break
			}
		}
	}
}

// A malformed value must be refused rather than silently ignored: a client that
// asked for page 2 and got page 1 back has no way to notice the gap.
func TestPaginateRefusesMalformedValues(t *testing.T) {
	for _, target := range []string{
		"/api/creds?limit=abc",
		"/api/creds?offset=abc",
		"/api/creds?limit=-1",
		"/api/creds?offset=-1",
	} {
		rec := httptest.NewRecorder()
		if _, ok := paginate(rec, httptest.NewRequest(http.MethodGet, target, nil), []int{1, 2, 3}); ok {
			t.Errorf("%s: paginate accepted a malformed value", target)
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}

// An empty page must serialise as [] rather than null so a client can iterate
// the response without a nil check.
func TestPaginateEmptyPageIsAnEmptyArray(t *testing.T) {
	rec := httptest.NewRecorder()
	got, ok := paginate(rec, httptest.NewRequest(http.MethodGet, "/api/creds?offset=99", nil), []int{1})
	if !ok {
		t.Fatal("paginate refused a valid request")
	}
	b, err := json.Marshal(map[string]any{"credentials": got})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Fatalf("body = %s, want an empty array", b)
	}
}

// --- /api/creds, end to end ------------------------------------------------

// credsStub answers the one RPC the credential list uses. Embedding the
// generated interface leaves every other method nil, so an unexpected call
// panics instead of returning zero values.
type credsStub struct {
	rpcpb.SliverRPCClient
	creds []*clientpb.Credential
}

func (c *credsStub) Creds(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.Credentials, error) {
	return &clientpb.Credentials{Credentials: c.creds}, nil
}

func newCredsServer(n int) *Server {
	creds := make([]*clientpb.Credential, 0, n)
	for i := 0; i < n; i++ {
		creds = append(creds, &clientpb.Credential{
			ID:       string(rune('a' + i)),
			Username: "u",
		})
	}
	s := New()
	s.SetClient(&sliver.Client{RPC: &credsStub{creds: creds}})
	return s
}

func credsIDs(t *testing.T, body []byte) []string {
	t.Helper()
	var got struct {
		Credentials []struct {
			ID string `json:"ID"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decoding %s: %v", body, err)
	}
	out := make([]string, 0, len(got.Credentials))
	for _, c := range got.Credentials {
		out = append(out, c.ID)
	}
	return out
}

func TestCredsEndpointPaging(t *testing.T) {
	s := newCredsServer(4)
	h := s.Routes()

	cases := []struct {
		target string
		want   string
	}{
		{"/api/creds", "a,b,c,d"},
		{"/api/creds?limit=2", "a,b"},
		{"/api/creds?offset=2", "c,d"},
		{"/api/creds?offset=2&limit=1", "c"},
		{"/api/creds?offset=4", ""},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, body %s", tc.target, rec.Code, rec.Body.String())
			continue
		}
		if got := strings.Join(credsIDs(t, rec.Body.Bytes()), ","); got != tc.want {
			t.Errorf("%s: ids = %q, want %q", tc.target, got, tc.want)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/creds?limit=nope", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad limit: status = %d, want 400", rec.Code)
	}
}
