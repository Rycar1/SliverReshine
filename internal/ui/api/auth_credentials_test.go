package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- credential file -------------------------------------------------------

func TestCredentialStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-auth")
	store := CredentialStore{Path: path}

	if _, _, found, err := store.Load(); found || err != nil {
		t.Fatalf("missing file must read as not-found without an error, got found=%v err=%v", found, err)
	}

	if err := store.Save("operator", "hunter2hunter2"); err != nil {
		t.Fatalf("save: %v", err)
	}
	user, pass, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("load after save: found=%v err=%v", found, err)
	}
	if user != "operator" || pass != "hunter2hunter2" {
		t.Fatalf("round trip changed the credentials: %q / %q", user, pass)
	}
}

func TestCredentialStoreKeepsColonsInPassword(t *testing.T) {
	// run.sh writes "user:pass" and so do we, so the split has to be on the
	// first colon only or a password containing one would be truncated on the
	// next start and lock the operator out.
	path := filepath.Join(t.TempDir(), "console-auth")
	store := CredentialStore{Path: path}

	const pass = "a:b:c:d"
	if err := store.Save("operator", pass); err != nil {
		t.Fatalf("save: %v", err)
	}
	user, got, _, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if user != "operator" || got != pass {
		t.Fatalf("password with colons mangled: user=%q pass=%q", user, got)
	}
}

func TestCredentialStoreToleratesCRLF(t *testing.T) {
	// The file may have been written on Windows by an operator editing it by
	// hand; a trailing CR must not become part of the password.
	path := filepath.Join(t.TempDir(), "console-auth")
	if err := os.WriteFile(path, []byte("operator:secret123\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	user, pass, found, err := CredentialStore{Path: path}.Load()
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if user != "operator" || pass != "secret123" {
		t.Fatalf("CRLF not trimmed: %q / %q", user, pass)
	}
}

func TestCredentialStoreRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-auth")
	if err := os.WriteFile(path, []byte("no-colon-here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := (CredentialStore{Path: path}).Load(); err == nil {
		t.Fatal("a file without a colon must be reported, not silently ignored")
	}
}

func TestCredentialStoreFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows has no Unix permission bits: a writable file reports 0666 no
		// matter what Chmod was asked for, so this assertion can only mean
		// something on the platform the console actually deploys to.
		t.Skip("Unix file modes are not representable on Windows")
	}
	path := filepath.Join(t.TempDir(), "console-auth")
	if err := (CredentialStore{Path: path}).Save("operator", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("credential file is %o, want 600", perm)
	}
}

func TestCredentialStoreRejectsNewlineInjection(t *testing.T) {
	store := CredentialStore{Path: filepath.Join(t.TempDir(), "console-auth")}
	if err := store.Save("operator", "good\noperator:attacker"); err == nil {
		t.Fatal("a password containing a newline would forge a second line and must be refused")
	}
	if err := store.Save("user:name", "whatever12"); err == nil {
		t.Fatal("a username containing a colon would shift the split and must be refused")
	}
}

// --- live credential changes ----------------------------------------------

// TestCredentialChangeAppliesToNextRequest is the property that matters: the
// console account is one record, so replacing it must take effect on the very
// next request rather than after a restart.
func TestCredentialChangeAppliesToNextRequest(t *testing.T) {
	cfg := &BasicAuth{User: "ops", Pass: "oldpassword"}
	h, reached := guarded(cfg)

	send := func(user, pass string) int {
		*reached = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", basicHeader(user, pass))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := send("ops", "oldpassword"); got != http.StatusOK {
		t.Fatalf("original password should work, got %d", got)
	}

	if err := cfg.SetCredentials("ops", "newpassword"); err != nil {
		t.Fatalf("set credentials: %v", err)
	}

	if got := send("ops", "newpassword"); got != http.StatusOK {
		t.Fatalf("new password must work immediately, got %d", got)
	}
	if got := send("ops", "oldpassword"); got != http.StatusUnauthorized {
		t.Fatalf("old password must stop working immediately, got %d", got)
	}
	if got := send("root", "newpassword"); got != http.StatusUnauthorized {
		t.Fatalf("renamed-away user must be rejected, got %d", got)
	}
}

func TestSetCredentialsInvokesPersistHook(t *testing.T) {
	var gotUser, gotPass string
	calls := 0
	cfg := &BasicAuth{
		User: "ops", Pass: "oldpassword",
		Persist: func(u, p string) error {
			calls++
			gotUser, gotPass = u, p
			return nil
		},
	}
	if err := cfg.SetCredentials("ops", "newpassword"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || gotUser != "ops" || gotPass != "newpassword" {
		t.Fatalf("persist hook not called with the new credentials: calls=%d %q/%q", calls, gotUser, gotPass)
	}
}

func TestPersistFailureDoesNotRollBackInMemoryCredentials(t *testing.T) {
	// Reverting would leave the operator holding a password the console has
	// already accepted, so the change stays and the error is reported instead.
	cfg := &BasicAuth{
		User: "ops", Pass: "oldpassword",
		Persist: func(string, string) error { return os.ErrPermission },
	}
	if err := cfg.SetCredentials("ops", "newpassword"); err == nil {
		t.Fatal("a failed persist must be reported to the caller")
	}
	if user, pass := cfg.Credentials(); user != "ops" || pass != "newpassword" {
		t.Fatalf("in-memory credentials were rolled back: %q/%q", user, pass)
	}
}

// --- handlers --------------------------------------------------------------

func authRequest(method, body string) *http.Request {
	req := httptest.NewRequest(method, "/api/settings/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestHandleAuthGetReportsAccountWithoutPassword(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "ops", Pass: "hunter2hunter2"})

	rec := httptest.NewRecorder()
	s.handleAuthGet(rec, authRequest(http.MethodGet, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "hunter2hunter2") {
		t.Fatal("the response must never contain the password")
	}
	var got authSettingsView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Username != "ops" || !got.Enabled {
		t.Fatalf("unexpected view: %+v", got)
	}
}

func TestHandleAuthPutRequiresCurrentPassword(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "ops", Pass: "hunter2hunter2"})

	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"ops","password":"brandnewpass","currentPassword":"wrong"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for a wrong current password, got %d", rec.Code)
	}
	if _, pass := s.basicAuth().Credentials(); pass != "hunter2hunter2" {
		t.Fatal("a rejected change must not have modified the account")
	}
}

func TestHandleAuthPutRejectsShortPassword(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "ops", Pass: "hunter2hunter2"})

	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"ops","password":"short","currentPassword":"hunter2hunter2"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a short password, got %d", rec.Code)
	}
}

func TestHandleAuthPutRefusesWhenAuthDisabled(t *testing.T) {
	// Otherwise anyone who can reach an open console could set a password and
	// lock the operator out of a console that was previously open to them.
	s := New()
	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"ops","password":"brandnewpass","currentPassword":""}`))
	if rec.Code != http.StatusConflict {
		t.Fatalf("want 409 when no account exists, got %d", rec.Code)
	}
}

func TestHandleAuthPutChangesAccountAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console-auth")
	store := CredentialStore{Path: path}
	cfg := &BasicAuth{User: "ops", Pass: "hunter2hunter2", Persist: store.Save}

	s := New()
	s.SetBasicAuth(cfg)

	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"operator","password":"brandnewpass","currentPassword":"hunter2hunter2"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("change reported as failed: %v", body)
	}

	// The live account and the file on disk must agree, or the next restart
	// would ask for a different password than the one just accepted.
	if user, pass := cfg.Credentials(); user != "operator" || pass != "brandnewpass" {
		t.Fatalf("live credentials not updated: %q/%q", user, pass)
	}
	user, pass, found, err := store.Load()
	if err != nil || !found {
		t.Fatalf("credentials were not persisted: found=%v err=%v", found, err)
	}
	if user != "operator" || pass != "brandnewpass" {
		t.Fatalf("persisted credentials disagree with the live ones: %q/%q", user, pass)
	}
}

func TestHandleAuthPutRejectsUnchangedCredentials(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "ops", Pass: "hunter2hunter2"})
	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"ops","password":"hunter2hunter2","currentPassword":"hunter2hunter2"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for a no-op change, got %d", rec.Code)
	}
}

// TestAuthSettingsRouteIsGuarded pins the endpoint behind the same credential
// check as everything else: an unauthenticated caller must not even learn which
// account the console uses.
func TestAuthSettingsRouteIsGuarded(t *testing.T) {
	s := New()
	s.SetBasicAuth(&BasicAuth{User: "ops", Pass: "hunter2hunter2"})
	h := s.Routes()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/settings/auth", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 without credentials, got %d", rec.Code)
	}
}
