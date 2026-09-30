package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newRecorder(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec, r)
	return rec
}

func TestInfoWithoutClient(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodGet, "/api/info", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["connected"] != false {
		t.Fatalf("connected = %v, want false", body["connected"])
	}
}

func TestEndpointsRequireClient(t *testing.T) {
	tests := []struct {
		name string
		req  *http.Request
	}{
		{"sessions", httptest.NewRequest(http.MethodGet, "/api/sessions", nil)},
		{"beacons", httptest.NewRequest(http.MethodGet, "/api/beacons", nil)},
		{"jobs", httptest.NewRequest(http.MethodGet, "/api/jobs", nil)},
		{"events", httptest.NewRequest(http.MethodGet, "/api/events", nil)},
		{"overview", httptest.NewRequest(http.MethodGet, "/api/overview", nil)},
		{"socks", httptest.NewRequest(http.MethodGet, "/api/socks", nil)},
		{"kill session", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/kill", nil)},
		{"fs list", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/fs", nil)},
		{"beacon tasks", httptest.NewRequest(http.MethodGet, "/api/beacons/b-1/tasks", nil)},
		{"stop listener", httptest.NewRequest(http.MethodDelete, "/api/listeners/1", nil)},
		{"regenerate", httptest.NewRequest(http.MethodPost, "/api/regenerate", nil)},
		{"loot list", httptest.NewRequest(http.MethodGet, "/api/loot", nil)},
		{"reg delete key", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/reg/delete-key", nil)},
		{"reconfigure", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/reconfigure", nil)},
		{"beacon single", httptest.NewRequest(http.MethodGet, "/api/beacons/b-1", nil)},
		{"loot add", httptest.NewRequest(http.MethodPost, "/api/loot", nil)},
		{"loot rename", httptest.NewRequest(http.MethodPost, "/api/loot/l-1/rename", nil)},
		{"compiler", httptest.NewRequest(http.MethodGet, "/api/compiler", nil)},
		{"beacon open session", httptest.NewRequest(http.MethodPost, "/api/beacons/b-1/open-session", nil)},
		{"session close", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/close", nil)},
		{"monitor start", httptest.NewRequest(http.MethodPost, "/api/monitor/start", nil)},
		{"monitor stop", httptest.NewRequest(http.MethodPost, "/api/monitor/stop", nil)},
		{"websites list", httptest.NewRequest(http.MethodGet, "/api/websites", nil)},
		{"website single", httptest.NewRequest(http.MethodGet, "/api/websites/site-1", nil)},
		{"website add content", httptest.NewRequest(http.MethodPost, "/api/websites/site-1/content", nil)},
		{"website update content", httptest.NewRequest(http.MethodPut, "/api/websites/site-1/content", nil)},
		{"website remove content", httptest.NewRequest(http.MethodDelete, "/api/websites/site-1/content", nil)},
		{"website rm", httptest.NewRequest(http.MethodDelete, "/api/websites/site-1", nil)},
		{"wg config", httptest.NewRequest(http.MethodGet, "/api/wg/config", nil)},
		{"wg unique ip", httptest.NewRequest(http.MethodGet, "/api/wg/ip", nil)},
		{"wg forwarders", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/wg/forwarders", nil)},
		{"wg start portfwd", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/wg/forwarders", nil)},
		{"wg stop portfwd", httptest.NewRequest(http.MethodDelete, "/api/sessions/s-1/wg/forwarders/1", nil)},
		{"wg socks", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/wg/socks", nil)},
		{"wg start socks", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/wg/socks", nil)},
		{"wg stop socks", httptest.NewRequest(http.MethodDelete, "/api/sessions/s-1/wg/socks/1", nil)},
		{"get privs", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/privs", nil)},
		{"token owner", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/token-owner", nil)},
		{"execute token", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/execute-token", nil)},
		{"runas", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/runas", nil)},
		{"pivot graph", httptest.NewRequest(http.MethodGet, "/api/pivots/graph", nil)},
		{"pivot listeners", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/pivots/listeners", nil)},
		{"pivot start listener", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/pivots/listeners", nil)},
		{"pivot stop listener", httptest.NewRequest(http.MethodDelete, "/api/sessions/s-1/pivots/listeners/1", nil)},
		{"start service", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/services", nil)},
		{"stop service", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/services/stop", nil)},
		{"remove service", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/services/remove", nil)},
		{"run ssh command", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/ssh", nil)},
		{"list extensions", httptest.NewRequest(http.MethodGet, "/api/sessions/s-1/extensions", nil)},
		{"register extension", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/extensions/register", nil)},
		{"call extension", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/extensions/call", nil)},
		{"msf", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/msf", nil)},
		{"msf remote", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/msf/remote", nil)},
		{"msf stage", httptest.NewRequest(http.MethodPost, "/api/msf/stage", nil)},
		{"backdoor", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/backdoor", nil)},
		{"dll hijack", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/dll-hijack", nil)},
		{"shellcode rdi", httptest.NewRequest(http.MethodPost, "/api/shellcode/rdi", nil)},
		{"canaries", httptest.NewRequest(http.MethodGet, "/api/canaries", nil)},
		{"exec shellcode", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/exec-shellcode", nil)},
		{"psexec", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/psexec", nil)},
		{"prune beacons", httptest.NewRequest(http.MethodPost, "/api/beacons/prune", nil)},
		{"prune sessions", httptest.NewRequest(http.MethodPost, "/api/sessions/prune", nil)},
		{"aliases", httptest.NewRequest(http.MethodGet, "/api/aliases", nil)},
		{"alias install", httptest.NewRequest(http.MethodPost, "/api/aliases", nil)},
		{"alias remove", httptest.NewRequest(http.MethodDelete, "/api/aliases/some", nil)},
		{"alias run", httptest.NewRequest(http.MethodPost, "/api/sessions/s-1/aliases/some/run", nil)},
		{"hosts list", httptest.NewRequest(http.MethodGet, "/api/hosts", nil)},
		{"host single", httptest.NewRequest(http.MethodGet, "/api/hosts/h-1", nil)},
		{"host rm", httptest.NewRequest(http.MethodDelete, "/api/hosts/h-1", nil)},
		{"host ioc rm", httptest.NewRequest(http.MethodDelete, "/api/hosts/h-1/iocs/ioc-1", nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := newRecorder(tt.req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}
			if !strings.Contains(rec.Body.String(), "not connected") {
				t.Fatalf("body = %q, want a 'not connected' error", rec.Body.String())
			}
		})
	}
}

func TestConnectInvalidBody(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodPost, "/api/connect", strings.NewReader("not-json{")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "invalid request body") {
		t.Fatalf("body = %q, want invalid request body", rec.Body.String())
	}
}

func TestConnectMissingProfile(t *testing.T) {
	body := bytes.NewBufferString(`{"name":"does-not-exist","lhost":"127.0.0.1","lport":31337}`)
	rec := newRecorder(httptest.NewRequest(http.MethodPost, "/api/connect", body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (missing profile should be a client error)", rec.Code, http.StatusBadRequest)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] == "" {
		t.Fatal("expected an error message")
	}
}

func TestDisconnectWithoutClient(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodPost, "/api/disconnect", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodGet, "/api/does-not-exist", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["error"] != "not found" {
		t.Fatalf("error = %q, want not found", resp["error"])
	}
}

func TestUnknownWSPathReturnsJSON404(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodGet, "/ws/unknown", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestCORSMiddleware(t *testing.T) {
	rec := newRecorder(httptest.NewRequest(http.MethodGet, "/api/info", nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}

	preflight := httptest.NewRequest(http.MethodOptions, "/api/info", nil)
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	rec2 := httptest.NewRecorder()
	New().Routes().ServeHTTP(rec2, preflight)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want %d", rec2.Code, http.StatusNoContent)
	}
}

func TestServeStaticServesAssetFile(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	serveStatic(rec, req, fsys, fsys["index.html"].Data)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "console.log(1)" {
		t.Fatalf("body = %q, want the asset content", rec.Body.String())
	}
}

func TestServeStaticSPAFallback(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
	}
	for _, path := range []string{"/", "/sessions/abc123", "/loot"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		serveStatic(rec, req, fsys, fsys["index.html"].Data)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want %d", path, rec.Code, http.StatusOK)
		}
		if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Fatalf("%s: content-type = %q, want html", path, rec.Header().Get("Content-Type"))
		}
	}
}

func TestServeStaticDevBinary(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	serveStatic(rec, req, fstest.MapFS{}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "Sliver UI API is running") {
		t.Fatalf("body = %q, want the dev fallback message", rec.Body.String())
	}
}

func TestServeStaticRejectsTraversal(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": {Data: []byte("<html>index</html>")},
		"secret.txt": {Data: []byte("top-secret")},
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/../secret.txt", nil)
	serveStatic(rec, req, fsys, fsys["index.html"].Data)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (traversal must be rejected)", rec.Code, http.StatusNotFound)
	}
	if rec.Body.String() == "top-secret" {
		t.Fatal("path traversal leaked a file")
	}
}
