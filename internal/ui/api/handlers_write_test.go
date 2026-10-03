package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/bishopfox/sliver/protobuf/clientpb"
	"github.com/bishopfox/sliver/protobuf/commonpb"
	"github.com/bishopfox/sliver/protobuf/rpcpb"

	"sliverreshine/internal/ui/sliver"
)

// This file drives the state-changing handlers against a stubbed Sliver RPC.
// The rest of the package proves routing and the no-client path; these prove the
// handler itself: that it validates before it calls, that the request it builds
// carries what the operator sent, and that a server error is answered with the
// status it deserves rather than a blanket 500.

// rpcStub answers the handful of server calls the write handlers make. It embeds
// the generated interface so the hundreds of methods a test does not exercise
// still satisfy the type -- and panics loudly if one is reached by mistake.
type rpcStub struct {
	rpcpb.SliverRPCClient

	getBeaconsCalls int
	getBeaconsResp  *clientpb.Beacons
	getBeaconsErr   error
	rmBeaconIDs     []string
	rmBeaconErr     error

	getSessionsCalls int
	getSessionsResp  *clientpb.Sessions
	getSessionsErr   error

	mtlsReq     *clientpb.MTLSListenerReq
	httpReq     *clientpb.HTTPListenerReq
	wgReq       *clientpb.WGListenerReq
	dnsReq      *clientpb.DNSListenerReq
	listenerErr error

	generateReq *clientpb.GenerateReq
	generateErr error

	websiteAddReq  *clientpb.WebsiteAddContent
	websiteAddResp *clientpb.Website
	websiteAddErr  error

	credsAddReq *clientpb.Credentials
	credsAddErr error

	lootAddResp *clientpb.Loot
	lootAddErr  error
}

func (s *rpcStub) GetBeacons(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.Beacons, error) {
	s.getBeaconsCalls++
	if s.getBeaconsErr != nil {
		return nil, s.getBeaconsErr
	}
	if s.getBeaconsResp == nil {
		return &clientpb.Beacons{}, nil
	}
	return s.getBeaconsResp, nil
}

func (s *rpcStub) RmBeacon(_ context.Context, in *clientpb.Beacon, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.rmBeaconErr != nil {
		return nil, s.rmBeaconErr
	}
	s.rmBeaconIDs = append(s.rmBeaconIDs, in.GetID())
	return &commonpb.Empty{}, nil
}

func (s *rpcStub) GetSessions(context.Context, *commonpb.Empty, ...grpc.CallOption) (*clientpb.Sessions, error) {
	s.getSessionsCalls++
	if s.getSessionsErr != nil {
		return nil, s.getSessionsErr
	}
	if s.getSessionsResp == nil {
		return &clientpb.Sessions{}, nil
	}
	return s.getSessionsResp, nil
}

func (s *rpcStub) StartMTLSListener(_ context.Context, in *clientpb.MTLSListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	if s.listenerErr != nil {
		return nil, s.listenerErr
	}
	s.mtlsReq = in
	return &clientpb.ListenerJob{JobID: 11}, nil
}

func (s *rpcStub) StartHTTPListener(_ context.Context, in *clientpb.HTTPListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	if s.listenerErr != nil {
		return nil, s.listenerErr
	}
	s.httpReq = in
	return &clientpb.ListenerJob{JobID: 12}, nil
}

func (s *rpcStub) StartWGListener(_ context.Context, in *clientpb.WGListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	if s.listenerErr != nil {
		return nil, s.listenerErr
	}
	s.wgReq = in
	return &clientpb.ListenerJob{JobID: 13}, nil
}

func (s *rpcStub) StartDNSListener(_ context.Context, in *clientpb.DNSListenerReq, _ ...grpc.CallOption) (*clientpb.ListenerJob, error) {
	if s.listenerErr != nil {
		return nil, s.listenerErr
	}
	s.dnsReq = in
	return &clientpb.ListenerJob{JobID: 14}, nil
}

func (s *rpcStub) Generate(_ context.Context, in *clientpb.GenerateReq, _ ...grpc.CallOption) (*clientpb.Generate, error) {
	if s.generateErr != nil {
		return nil, s.generateErr
	}
	s.generateReq = in
	return &clientpb.Generate{}, nil
}

func (s *rpcStub) WebsiteAddContent(_ context.Context, in *clientpb.WebsiteAddContent, _ ...grpc.CallOption) (*clientpb.Website, error) {
	if s.websiteAddErr != nil {
		return nil, s.websiteAddErr
	}
	s.websiteAddReq = in
	return s.websiteAddResp, nil
}

func (s *rpcStub) CredsAdd(_ context.Context, in *clientpb.Credentials, _ ...grpc.CallOption) (*commonpb.Empty, error) {
	if s.credsAddErr != nil {
		return nil, s.credsAddErr
	}
	s.credsAddReq = in
	return &commonpb.Empty{}, nil
}

func (s *rpcStub) LootAdd(_ context.Context, _ *clientpb.Loot, _ ...grpc.CallOption) (*clientpb.Loot, error) {
	if s.lootAddErr != nil {
		return nil, s.lootAddErr
	}
	if s.lootAddResp == nil {
		return &clientpb.Loot{ID: "loot-1"}, nil
	}
	return s.lootAddResp, nil
}

// serverWithStub wires a client whose RPC layer is the stub, so the handler
// under test runs for real instead of stopping at the 503 no-client path.
func serverWithStub(stub rpcpb.SliverRPCClient) *Server {
	s := New()
	s.SetClient(&sliver.Client{RPC: stub})
	return s
}

// recorderFor is newRecorder for a server that already carries a client.
func recorderFor(s *Server, r *http.Request) *httptest.ResponseRecorder {
	prepareMutation(r)
	rec := httptest.NewRecorder()
	s.Routes().ServeHTTP(rec, r)
	return rec
}

// --- Prune ---

func TestWritePruneBeaconsRejectsNonPositiveDays(t *testing.T) {
	for _, body := range []string{`{"days":0}`, `{"days":-3}`} {
		stub := &rpcStub{}
		rec := recorderFor(serverWithStub(stub),
			httptest.NewRequest(http.MethodPost, "/api/beacons/prune", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400: %s", body, rec.Code, rec.Body.String())
		}
		if stub.getBeaconsCalls != 0 {
			t.Errorf("%s: a rejected request reached the server", body)
		}
	}
}

func TestWritePruneBeaconsRemovesOnlyStaleBeacons(t *testing.T) {
	now := time.Now()
	stub := &rpcStub{getBeaconsResp: &clientpb.Beacons{Beacons: []*clientpb.Beacon{
		{ID: "stale", NextCheckin: now.Add(-48 * time.Hour).Unix()},
		{ID: "recent", NextCheckin: now.Add(-1 * time.Hour).Unix()},
		{ID: "future", NextCheckin: now.Add(2 * time.Hour).Unix()},
	}}}
	rec := recorderFor(serverWithStub(stub),
		httptest.NewRequest(http.MethodPost, "/api/beacons/prune", strings.NewReader(`{"days":1}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Pruned  int  `json:"pruned"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.Pruned != 1 {
		t.Fatalf("body = %s, want success with exactly one pruned beacon", rec.Body.String())
	}
	if len(stub.rmBeaconIDs) != 1 || stub.rmBeaconIDs[0] != "stale" {
		t.Fatalf("removed = %v, want [stale]: only the check-in that is both past due and older than the window may go", stub.rmBeaconIDs)
	}
}

func TestWritePruneBeaconsReportsATransportFailure(t *testing.T) {
	stub := &rpcStub{getBeaconsErr: status.Error(codes.Unavailable, "connection refused")}
	rec := recorderFor(serverWithStub(stub),
		httptest.NewRequest(http.MethodPost, "/api/beacons/prune", strings.NewReader(`{"days":1}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
}

// --- Listeners ---

func TestWriteListenersFillsInBindAddressAndPort(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    uint32
		host    func(*rpcStub) string
		port    func(*rpcStub) uint32
		website func(*rpcStub) string
	}{
		{"mtls", `{"type":"mtls"}`, 8888,
			func(s *rpcStub) string { return s.mtlsReq.GetHost() },
			func(s *rpcStub) uint32 { return s.mtlsReq.GetPort() }, nil},
		{"http", `{"type":"http"}`, 80,
			func(s *rpcStub) string { return s.httpReq.GetHost() },
			func(s *rpcStub) uint32 { return s.httpReq.GetPort() },
			func(s *rpcStub) string { return s.httpReq.GetWebsite() }},
		{"wireguard", `{"type":"wireguard"}`, 51820,
			func(s *rpcStub) string { return s.wgReq.GetHost() },
			func(s *rpcStub) uint32 { return s.wgReq.GetPort() }, nil},
		{"dns", `{"type":"dns"}`, 53,
			func(s *rpcStub) string { return s.dnsReq.GetHost() },
			func(s *rpcStub) uint32 { return s.dnsReq.GetPort() }, nil},
	}
	for _, tc := range cases {
		stub := &rpcStub{}
		rec := recorderFor(serverWithStub(stub),
			httptest.NewRequest(http.MethodPost, "/api/listeners", strings.NewReader(tc.body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", tc.name, rec.Code, rec.Body.String())
		}
		if got := tc.host(stub); got != "0.0.0.0" {
			t.Errorf("%s: host = %q, want 0.0.0.0 so the listener is reachable", tc.name, got)
		}
		if got := tc.port(stub); got != tc.want {
			t.Errorf("%s: port = %d, want %d", tc.name, got, tc.want)
		}
		if tc.website != nil {
			if got := tc.website(stub); got != defaultDeliverySite {
				t.Errorf("%s: website = %q, want %q", tc.name, got, defaultDeliverySite)
			}
		}
	}
}

func TestWriteListenersKeepsAnExplicitBindAndPort(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/listeners",
		strings.NewReader(`{"type":"mtls","addr":"10.0.0.5","port":4443}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got := stub.mtlsReq.GetHost(); got != "10.0.0.5" {
		t.Errorf("host = %q, want the address the caller asked for", got)
	}
	if got := stub.mtlsReq.GetPort(); got != 4443 {
		t.Errorf("port = %d, want 4443", got)
	}
}

func TestWriteListenersReportsAServerFailure(t *testing.T) {
	stub := &rpcStub{listenerErr: status.Error(codes.InvalidArgument, "port already in use")}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/listeners",
		strings.NewReader(`{"type":"mtls","port":4443}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// --- Generate ---

func TestWriteGenerateRejectsBadRequests(t *testing.T) {
	cases := []struct{ name, body string }{
		{"malformed json", `{`},
		{"missing name", `{"os":"windows","arch":"amd64"}`},
		{"unsupported os", `{"name":"x","os":"plan9","arch":"amd64"}`},
		{"unsupported format", `{"name":"x","os":"windows","arch":"amd64","format":"rom"}`},
	}
	for _, tc := range cases {
		stub := &rpcStub{}
		rec := recorderFor(serverWithStub(stub),
			httptest.NewRequest(http.MethodPost, "/api/generate", strings.NewReader(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400: %s", tc.name, rec.Code, rec.Body.String())
		}
		if stub.generateReq != nil {
			t.Errorf("%s: an invalid request was forwarded to the server", tc.name)
		}
	}
}

// --- Websites ---

func TestWriteWebsiteAddContentPassesTheEntryThrough(t *testing.T) {
	stub := &rpcStub{websiteAddResp: &clientpb.Website{Name: "site-1"}}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/websites/site-1/content",
		strings.NewReader(`{"path":"/index.html","text":"hello"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if stub.websiteAddReq.GetName() != "site-1" {
		t.Errorf("name = %q, want the path segment", stub.websiteAddReq.GetName())
	}
	entry := stub.websiteAddReq.GetContents()["/index.html"]
	if entry == nil {
		t.Fatal("the content entry never reached the server")
	}
	if string(entry.GetContent()) != "hello" {
		t.Errorf("content = %q, want hello", entry.GetContent())
	}
	if entry.GetContentType() != "text/html; charset=utf-8" {
		t.Errorf("content type = %q, want the html default", entry.GetContentType())
	}
}

func TestWriteWebsiteAddContentMapsAServerError(t *testing.T) {
	stub := &rpcStub{websiteAddErr: status.Error(codes.NotFound, "website not found")}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/websites/site-1/content",
		strings.NewReader(`{"path":"/index.html","text":"hello"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// --- Loot ---

func TestWriteLootAddRejectsReservedUsername(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/loot",
		strings.NewReader(`{"type":"credential","name":"c","cred_user":"apikey","cred_password":"x"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.credsAddReq != nil {
		t.Error("a credential that collides with the API-key marker was stored anyway")
	}
}

func TestWriteLootAddRejectsInvalidFileData(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/loot",
		strings.NewReader(`{"name":"f","file_data_b64":"!!!"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestWriteLootAddStoresFileLoot(t *testing.T) {
	stub := &rpcStub{lootAddResp: &clientpb.Loot{ID: "loot-9"}}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/loot",
		strings.NewReader(`{"name":"f","file_name":"a.txt","file_type":"text","file_data_b64":"eA=="}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool   `json:"success"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Success || body.ID != "loot-9" {
		t.Fatalf("body = %s, want success with the stored id", rec.Body.String())
	}
}

// --- Persistence ---

func TestWritePersistenceInstallRequiresAModule(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost,
		"/api/sessions/s-1/persistence/install", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.getSessionsCalls != 0 {
		t.Error("a request with no module reached the server before being refused")
	}
}

func TestWritePersistenceInstallRequiresAPlatformWhenTheSessionIsUnknown(t *testing.T) {
	stub := &rpcStub{getSessionsResp: &clientpb.Sessions{}}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost,
		"/api/sessions/s-1/persistence/install", strings.NewReader(`{"module":"cron"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if stub.getSessionsCalls == 0 {
		t.Error("the handler must try to resolve the session OS before giving up")
	}
}

// --- Small write endpoints ---

func TestWriteSocksStopRejectsInvalidID(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodDelete, "/api/socks/not-a-number", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestWriteAliasInstallRequiresABundle(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/aliases",
		strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestWritePortfwdStartRequiresSessionAndRemotePort(t *testing.T) {
	stub := &rpcStub{}
	rec := recorderFor(serverWithStub(stub), httptest.NewRequest(http.MethodPost, "/api/portfwd",
		strings.NewReader(`{"session_id":"","remote_port":0}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}
