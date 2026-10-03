package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"

	"c2tool/internal/ui/sliver"
)

// downloadStub answers the one RPC the download path uses. It embeds the
// generated interface so every other method is the nil embedded value -- calling
// one would panic, which is what makes an unexpected call visible rather than
// silently returning zero values.
type downloadStub struct {
	rpcpb.SliverRPCClient
	data   []byte
	path   string
	exists bool
}

func (d *downloadStub) Download(_ context.Context, in *sliverpb.DownloadReq, _ ...grpc.CallOption) (*sliverpb.Download, error) {
	return &sliverpb.Download{
		Path:   d.path,
		Data:   d.data,
		Exists: d.exists,
		Response: &commonpb.Response{
			Err: "",
		},
	}, nil
}

// F3, behaviourally: /fs/cat must answer JSON with base64 Data, and
// /fs/download must answer the raw bytes as an attachment.
//
// An earlier version of this test compared the two handler function values taken
// from the route table. That does NOT catch the defect: when handleFsCat was a
// one-line alias for handleFsDownload, the two routes still held two distinct
// method values (handleFsCat-fm and handleFsDownload-fm), so their pointers
// differed and the test passed against the broken code. The contract has to be
// observed by running the handler.
func TestFsCatAndFsDownloadAnswerDifferentContracts(t *testing.T) {
	body := []byte("root:x:0:0:root:/root:/bin/bash\n")
	const remote = "/etc/hosts"

	s := New()
	s.SetClient(&sliver.Client{RPC: &downloadStub{
		data:   body,
		path:   remote,
		exists: true,
	}})

	// fs/cat: JSON, with the bytes base64-encoded in Data.
	catRec := httptest.NewRecorder()
	catReq := httptest.NewRequest(http.MethodGet,
		"/api/sessions/s-1/fs/cat?path="+remote, nil)
	// PathValue comes from the mux; a direct handler call has to set it.
	catReq.SetPathValue("id", "s-1")
	s.handleFsCat(catRec, catReq)

	if ct := catRec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("fs/cat Content-Type = %q, want JSON", ct)
	}
	if cd := catRec.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("fs/cat set a download disposition: %q", cd)
	}
	var got struct {
		Data string `json:"Data"`
		Name string `json:"Name"`
	}
	if err := json.Unmarshal(catRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("fs/cat did not answer JSON (%v): %q", err, catRec.Body.String())
	}
	if got.Data != base64.StdEncoding.EncodeToString(body) {
		t.Errorf("fs/cat Data = %q, want base64 of the file bytes %q",
			got.Data, base64.StdEncoding.EncodeToString(body))
	}
	if got.Name != remote {
		t.Errorf("fs/cat Name = %q, want %q", got.Name, remote)
	}

	// fs/download: the raw bytes, as an attachment.
	dlRec := httptest.NewRecorder()
	dlReq := httptest.NewRequest(http.MethodGet,
		"/api/sessions/s-1/fs/download?path="+remote, nil)
	dlReq.SetPathValue("id", "s-1")
	s.handleFsDownload(dlRec, dlReq)

	if ct := dlRec.Header().Get("Content-Type"); !strings.Contains(ct, "octet-stream") {
		t.Errorf("fs/download Content-Type = %q, want octet-stream", ct)
	}
	if cd := dlRec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("fs/download Content-Disposition = %q, want an attachment", cd)
	}
	if dlRec.Body.String() != string(body) {
		t.Errorf("fs/download body = %q, want the raw file contents %q",
			dlRec.Body.String(), string(body))
	}

	// The decisive assertion: the two must not be interchangeable. This is what
	// fails when fsCat is an alias again.
	if catRec.Body.String() == dlRec.Body.String() {
		t.Error("fs/cat and fs/download returned identical bodies; the two endpoints " +
			"have different contracts, so fs/cat must not be an alias for the download")
	}
	if catRec.Header().Get("Content-Type") == dlRec.Header().Get("Content-Type") {
		t.Error("fs/cat and fs/download declared the same Content-Type")
	}
}

// The stub must actually be installed, or the test above passes for the wrong
// reason (an unset client makes every handler return 503 before producing a body,
// and two 503s would compare equal).
func TestDownloadStubProducesABody(t *testing.T) {
	s := New()
	if s.Client() != nil {
		t.Fatal("a fresh server already has a client")
	}

	s.SetClient(&sliver.Client{RPC: &downloadStub{
		data: []byte("hello"), path: "/x", exists: true,
	}})
	if s.Client() == nil || s.Client().RPC == nil {
		t.Fatal("the stub client was not installed")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/fs/download?path=/x", nil)
	req.SetPathValue("id", "s-1")
	s.handleFsDownload(rec, req)

	if rec.Code == http.StatusServiceUnavailable {
		t.Fatal("the handler reported no connection, so the stub is not in use")
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want the stub's bytes", rec.Body.String())
	}
}

// The frontend declaration, read from source, so a change on either side fails
// here rather than in the browser.
func TestFsCatContractMatchesTheFrontend(t *testing.T) {
	src, err := readFileString("../../../frontend/src/lib/api.ts")
	if err != nil {
		t.Skipf("cannot read the frontend source: %v", err)
	}

	idx := strings.Index(src, "fsCat:")
	if idx < 0 {
		t.Fatal("fsCat is no longer declared in the frontend API client")
	}
	tail := src[idx:]
	if end := strings.Index(tail, "fsDownload:"); end > 0 {
		tail = tail[:end]
	}
	// Drop comments: the JSDoc above fsDownload mentions a Blob and belongs to
	// that declaration, not this one.
	var body []string
	for _, line := range strings.Split(tail, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "*") ||
			strings.HasPrefix(trimmed, "/*") {
			continue
		}
		body = append(body, line)
	}
	code := strings.Join(body, "\n")

	for _, field := range []string{"Data", "Name"} {
		if !strings.Contains(code, field) {
			t.Errorf("the frontend no longer reads %q from fsCat; the handler returns "+
				"exactly those two fields", field)
		}
	}
	if strings.Contains(code, "fetch(") || strings.Contains(code, "blob") {
		t.Errorf("the frontend treats fsCat as raw bytes, but the handler returns JSON:\n%s", code)
	}
}

func readFileString(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
