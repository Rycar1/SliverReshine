package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"
	"github.com/bishopfox/sliver/protobuf/sliverpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"sliverreshine/internal/ui/sliver"
)

// fsStub answers the file-system RPCs and records the request it was handed, so
// a test can assert on what the handler actually asked the target to do rather
// than only on the HTTP status.
//
// It embeds the generated interface, so a handler that reaches for an RPC this
// test did not expect panics on the nil embedded value instead of quietly
// succeeding.
type fsStub struct {
	rpcpb.SliverRPCClient

	pwdReq      *sliverpb.PwdReq
	cdReq       *sliverpb.CdReq
	lsReq       *sliverpb.LsReq
	downloadReq *sliverpb.DownloadReq
	uploadReq   *sliverpb.UploadReq
	mkdirReq    *sliverpb.MkdirReq
	rmReq       *sliverpb.RmReq
	mvReq       *sliverpb.MvReq

	pwdPath string
	pwdErr  error

	cdPath string
	cdErr  error

	lsResp *sliverpb.Ls
	lsErr  error

	uploadErr error
	mkdirErr  error
	rmErr     error
	mvErr     error
}

func (s *fsStub) Pwd(_ context.Context, in *sliverpb.PwdReq, _ ...grpc.CallOption) (*sliverpb.Pwd, error) {
	s.pwdReq = in
	if s.pwdErr != nil {
		return nil, s.pwdErr
	}
	return &sliverpb.Pwd{Path: s.pwdPath, Response: &commonpb.Response{}}, nil
}

func (s *fsStub) Cd(_ context.Context, in *sliverpb.CdReq, _ ...grpc.CallOption) (*sliverpb.Pwd, error) {
	s.cdReq = in
	if s.cdErr != nil {
		return nil, s.cdErr
	}
	return &sliverpb.Pwd{Path: s.cdPath, Response: &commonpb.Response{}}, nil
}

func (s *fsStub) Ls(_ context.Context, in *sliverpb.LsReq, _ ...grpc.CallOption) (*sliverpb.Ls, error) {
	s.lsReq = in
	if s.lsErr != nil {
		return nil, s.lsErr
	}
	if s.lsResp != nil {
		return s.lsResp, nil
	}
	return &sliverpb.Ls{Response: &commonpb.Response{}}, nil
}

func (s *fsStub) Upload(_ context.Context, in *sliverpb.UploadReq, _ ...grpc.CallOption) (*sliverpb.Upload, error) {
	s.uploadReq = in
	if s.uploadErr != nil {
		return nil, s.uploadErr
	}
	return &sliverpb.Upload{Response: &commonpb.Response{}}, nil
}

func (s *fsStub) Mkdir(_ context.Context, in *sliverpb.MkdirReq, _ ...grpc.CallOption) (*sliverpb.Mkdir, error) {
	s.mkdirReq = in
	if s.mkdirErr != nil {
		return nil, s.mkdirErr
	}
	return &sliverpb.Mkdir{Response: &commonpb.Response{}}, nil
}

func (s *fsStub) Rm(_ context.Context, in *sliverpb.RmReq, _ ...grpc.CallOption) (*sliverpb.Rm, error) {
	s.rmReq = in
	if s.rmErr != nil {
		return nil, s.rmErr
	}
	return &sliverpb.Rm{Response: &commonpb.Response{}}, nil
}

func (s *fsStub) Mv(_ context.Context, in *sliverpb.MvReq, _ ...grpc.CallOption) (*sliverpb.Mv, error) {
	s.mvReq = in
	if s.mvErr != nil {
		return nil, s.mvErr
	}
	return &sliverpb.Mv{Response: &commonpb.Response{}}, nil
}

// fsBody builds a request with an optional JSON body, through the real route
// table so the mux supplies the {id} path value and the CSRF and body-limit
// middleware run as they do in production.
func fsBody(s *Server, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return recorderFor(s, req)
}

func fsPathQuery(base string, path string) string {
	v := url.Values{}
	v.Set("path", path)
	return base + "?" + v.Encode()
}

// --- Pwd ---

func TestHandleFsPwdReportsTheSessionDirectory(t *testing.T) {
	stub := &fsStub{pwdPath: "/home/ops"}
	rec := fsBody(serverWithStub(stub), http.MethodGet, "/api/sessions/s-1/fs/pwd", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Path string `json:"Path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/home/ops" {
		t.Fatalf("Path = %q, want /home/ops", got.Path)
	}
	if id := stub.pwdReq.GetRequest().GetSessionID(); id != "s-1" {
		t.Fatalf("SessionID = %q, want the id from the URL", id)
	}
}

// A server-side failure has to reach the operator as the status it deserves,
// not a blanket 500: "session not found" is the caller's mistake.
func TestHandleFsPwdMapsServerErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"a missing session is a 404", status.Error(codes.NotFound, "session not found"), http.StatusNotFound},
		{"an unmapped failure stays a 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := fsBody(serverWithStub(&fsStub{pwdErr: tc.err}), http.MethodGet, "/api/sessions/s-1/fs/pwd", "")
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// --- Cd ---

func TestHandleFsCdForwardsThePath(t *testing.T) {
	stub := &fsStub{cdPath: "/var/log"}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/cd", `{"path":"/var/log"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.cdReq.GetPath() != "/var/log" {
		t.Fatalf("Cd path = %q, want /var/log", stub.cdReq.GetPath())
	}
	var got struct {
		Path string `json:"Path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Path != "/var/log" {
		t.Fatalf("Path = %q, want the directory the target moved to", got.Path)
	}
}

// A body the handler cannot understand must be rejected before the target is
// touched: an unknown field means a client built against a different version,
// and running the command anyway would act on a request nobody made.
func TestHandleFsCdRejectsBadBodies(t *testing.T) {
	bodies := []string{
		`{`,
		`{"path":"/tmp","extra":1}`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			stub := &fsStub{}
			rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/cd", body)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %q: %s", rec.Code, body, rec.Body.String())
			}
			if stub.cdReq != nil {
				t.Fatal("a rejected request reached the target")
			}
		})
	}
}

// encoding/json matches field names case-insensitively, so a client that spells
// the field "Path" is understood rather than refused. Pinning it keeps a future
// switch to a stricter decoder from silently breaking that client.
func TestHandleFsCdMatchesFieldNamesCaseInsensitively(t *testing.T) {
	stub := &fsStub{cdPath: "/tmp"}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/cd", `{"Path":"/tmp"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.cdReq.GetPath() != "/tmp" {
		t.Fatalf("Cd path = %q, want /tmp", stub.cdReq.GetPath())
	}
}

// --- List ---

func TestHandleFsListUsesTheGivenPath(t *testing.T) {
	stub := &fsStub{lsResp: &sliverpb.Ls{
		Path:   "/etc",
		Exists: true,
		Files: []*sliverpb.FileInfo{
			{Name: "zzz.txt"},
			{Name: "conf.d", IsDir: true},
		},
		Response: &commonpb.Response{},
	}}
	rec := fsBody(serverWithStub(stub), http.MethodGet, fsPathQuery("/api/sessions/s-1/fs", "/etc"), "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.lsReq.GetPath() != "/etc" {
		t.Fatalf("Ls path = %q, want /etc", stub.lsReq.GetPath())
	}
	if stub.pwdReq != nil {
		t.Fatal("a listing with an explicit path must not ask for the working directory first")
	}
	var got sliver.DirView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Exists || got.Path != "/etc" {
		t.Fatalf("unexpected view: %+v", got)
	}
	// Directories sort before files, then by name: the order the panel shows.
	if len(got.Files) != 2 || got.Files[0].Name != "conf.d" || got.Files[1].Name != "zzz.txt" {
		t.Fatalf("files = %+v, want the directory first", got.Files)
	}
}

func TestHandleFsListFallsBackToPwd(t *testing.T) {
	stub := &fsStub{pwdPath: "/root", lsResp: &sliverpb.Ls{Path: "/root", Exists: true, Response: &commonpb.Response{}}}
	rec := fsBody(serverWithStub(stub), http.MethodGet, "/api/sessions/s-1/fs", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.pwdReq == nil {
		t.Fatal("a listing with no path must ask for the working directory")
	}
	if stub.lsReq.GetPath() != "/root" {
		t.Fatalf("Ls path = %q, want the working directory from Pwd", stub.lsReq.GetPath())
	}
}

func TestHandleFsListStopsWhenPwdFails(t *testing.T) {
	stub := &fsStub{pwdErr: status.Error(codes.NotFound, "session not found")}
	rec := fsBody(serverWithStub(stub), http.MethodGet, "/api/sessions/s-1/fs", "")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if stub.lsReq != nil {
		t.Fatal("the listing must not run after the working directory could not be read")
	}
}

// --- Upload ---

func TestHandleFsUploadDecodesAndForwards(t *testing.T) {
	stub := &fsStub{}
	payload := base64.StdEncoding.EncodeToString([]byte("hello, target"))
	body, err := json.Marshal(map[string]string{"path": "/tmp/x.bin", "data": payload})
	if err != nil {
		t.Fatal(err)
	}

	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/upload", string(body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got["success"] {
		t.Fatalf("body = %v, want success true", got)
	}
	if stub.uploadReq.GetPath() != "/tmp/x.bin" {
		t.Fatalf("upload path = %q", stub.uploadReq.GetPath())
	}
	if string(stub.uploadReq.GetData()) != "hello, target" {
		t.Fatalf("upload data = %q, want the decoded bytes", stub.uploadReq.GetData())
	}
	if !stub.uploadReq.GetOverwrite() {
		t.Fatal("Overwrite = false, want true: the console always replaces what it uploads")
	}
	if id := stub.uploadReq.GetRequest().GetSessionID(); id != "s-1" {
		t.Fatalf("SessionID = %q, want the id from the URL", id)
	}
}

func TestHandleFsUploadRejectsInvalidBase64(t *testing.T) {
	stub := &fsStub{}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/upload",
		`{"path":"/tmp/x","data":"not base64!!"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.uploadReq != nil {
		t.Fatal("a request with undecodable data reached the target")
	}
}

// An empty payload is a legitimate upload (a zero-byte file), not a validation
// error, so it must still reach the target.
func TestHandleFsUploadAcceptsAnEmptyFile(t *testing.T) {
	stub := &fsStub{}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/upload",
		`{"path":"/tmp/empty","data":""}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.uploadReq == nil {
		t.Fatal("an empty file must still be uploaded")
	}
	if len(stub.uploadReq.GetData()) != 0 {
		t.Fatalf("data = %q, want empty", stub.uploadReq.GetData())
	}
}

func TestHandleFsUploadMapsServerErrors(t *testing.T) {
	stub := &fsStub{uploadErr: status.Error(codes.InvalidArgument, "invalid file name")}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/upload",
		`{"path":"","data":"aGk="}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// --- Mkdir ---

func TestHandleFsMkdirForwardsThePath(t *testing.T) {
	stub := &fsStub{}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/mkdir", `{"path":"/tmp/newdir"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.mkdirReq.GetPath() != "/tmp/newdir" {
		t.Fatalf("mkdir path = %q", stub.mkdirReq.GetPath())
	}
}

func TestHandleFsMkdirRejectsUnknownFields(t *testing.T) {
	stub := &fsStub{}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/mkdir",
		`{"path":"/tmp/x","mode":"0777"}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.mkdirReq != nil {
		t.Fatal("a rejected request reached the target")
	}
}

// --- Rm ---

func TestHandleFsRmReadsTheRecursiveFlag(t *testing.T) {
	tests := []struct {
		query string
		want  bool
	}{
		{"", false},
		{"recursive=0", false},
		{"recursive=false", false},
		{"recursive=1", true},
		{"recursive=true", true},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			stub := &fsStub{}
			target := fsPathQuery("/api/sessions/s-1/fs", "/tmp/junk")
			if tc.query != "" {
				target += "&" + tc.query
			}
			rec := fsBody(serverWithStub(stub), http.MethodDelete, target, "")

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if stub.rmReq.GetPath() != "/tmp/junk" {
				t.Fatalf("rm path = %q", stub.rmReq.GetPath())
			}
			if stub.rmReq.GetRecursive() != tc.want {
				t.Fatalf("Recursive = %v, want %v for %q", stub.rmReq.GetRecursive(), tc.want, tc.query)
			}
			if !stub.rmReq.GetForce() {
				t.Fatal("Force = false, want true: the console does not prompt the target")
			}
		})
	}
}

func TestHandleFsRmRequiresAPath(t *testing.T) {
	stub := &fsStub{}
	rec := fsBody(serverWithStub(stub), http.MethodDelete, "/api/sessions/s-1/fs", "")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.rmReq != nil {
		t.Fatal("a delete with no path reached the target")
	}
}

// --- Mv ---

func TestHandleFsMvForwardsBothPaths(t *testing.T) {
	stub := &fsStub{}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/mv",
		`{"src":"/tmp/a","dst":"/tmp/b"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.mvReq.GetSrc() != "/tmp/a" || stub.mvReq.GetDst() != "/tmp/b" {
		t.Fatalf("mv = %q -> %q, want /tmp/a -> /tmp/b", stub.mvReq.GetSrc(), stub.mvReq.GetDst())
	}
	if id := stub.mvReq.GetRequest().GetSessionID(); id != "s-1" {
		t.Fatalf("SessionID = %q, want the id from the URL", id)
	}
}

func TestHandleFsMvMapsServerErrors(t *testing.T) {
	stub := &fsStub{mvErr: status.Error(codes.NotFound, "session not found")}
	rec := fsBody(serverWithStub(stub), http.MethodPost, "/api/sessions/s-1/fs/mv",
		`{"src":"/a","dst":"/b"}`)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
