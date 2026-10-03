package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authServerWith builds a console whose account is already set, which is the
// only state handleAuthPut will act on.
func authServerWith(t *testing.T, cfg *BasicAuth) *Server {
	t.Helper()
	s := New()
	s.SetBasicAuth(cfg)
	return s
}

// Every rejected change below must leave the live account exactly as it was:
// a validation failure that half-applied would be worse than no validation.
func assertAccountUnchanged(t *testing.T, s *Server, wantUser, wantPass string) {
	t.Helper()
	if user, pass := s.basicAuth().Credentials(); user != wantUser || pass != wantPass {
		t.Fatalf("credentials changed by a rejected request: %q/%q, want %q/%q", user, pass, wantUser, wantPass)
	}
}

func TestHandleAuthPutRejectsMalformedBody(t *testing.T) {
	bodies := []string{
		"",
		"{",
		"not json at all",
		`{"username": 5}`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})

			rec := httptest.NewRecorder()
			s.handleAuthPut(rec, authRequest(http.MethodPut, body))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for %q: %s", rec.Code, body, rec.Body.String())
			}
			assertAccountUnchanged(t, s, "ops", "hunter2hunter2")
		})
	}
}

// The account name is written into the credential file as "user:pass", so a
// colon or a line break in it would let a caller forge a second record or
// truncate the one on disk.
func TestHandleAuthPutRejectsInjectedUsername(t *testing.T) {
	// Trailing whitespace is not in this list: it is trimmed before the check
	// (pinned in TestHandleAuthPutTrimsUsername), so only an embedded break or
	// colon can forge a record.
	usernames := []string{
		"ops:admin",
		"ops\r\ninjected",
		"op\ns",
		"a\rb",
	}
	for _, name := range usernames {
		t.Run(strings.ReplaceAll(name, "\n", "\\n"), func(t *testing.T) {
			s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})
			body, err := json.Marshal(map[string]string{
				"username":        name,
				"password":        "brandnewpass",
				"currentPassword": "hunter2hunter2",
			})
			if err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			s.handleAuthPut(rec, authRequest(http.MethodPut, string(body)))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for username %q: %s", rec.Code, name, rec.Body.String())
			}
			assertAccountUnchanged(t, s, "ops", "hunter2hunter2")
		})
	}
}

// Same injection risk on the password side, and the length check must not mask
// it: these are all at or above the minimum length.
func TestHandleAuthPutRejectsNewlineInPassword(t *testing.T) {
	passwords := []string{
		"long\r\npassword",
		"long\npassword",
		"long\rpassword",
	}
	for _, pass := range passwords {
		t.Run(strings.ReplaceAll(pass, "\n", "\\n"), func(t *testing.T) {
			s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})
			body, err := json.Marshal(map[string]string{
				"username":        "ops",
				"password":        pass,
				"currentPassword": "hunter2hunter2",
			})
			if err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			s.handleAuthPut(rec, authRequest(http.MethodPut, string(body)))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 for a password containing a line break: %s", rec.Code, rec.Body.String())
			}
			assertAccountUnchanged(t, s, "ops", "hunter2hunter2")
		})
	}
}

// A blank username means "keep the account name", not "rename to the empty
// string", which would leave a credential record with no user half.
func TestHandleAuthPutBlankUsernameKeepsTheCurrentAccount(t *testing.T) {
	bodies := []string{
		`{"username":"   ","password":"brandnewpass","currentPassword":"hunter2hunter2"}`,
		`{"password":"brandnewpass","currentPassword":"hunter2hunter2"}`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})

			rec := httptest.NewRecorder()
			s.handleAuthPut(rec, authRequest(http.MethodPut, body))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if user, pass := s.basicAuth().Credentials(); user != "ops" || pass != "brandnewpass" {
				t.Fatalf("credentials = %q/%q, want ops/brandnewpass", user, pass)
			}
		})
	}
}

// The surrounding whitespace an operator pastes in is not part of the name.
func TestHandleAuthPutTrimsUsername(t *testing.T) {
	s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})

	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"  alice  ","password":"brandnewpass","currentPassword":"hunter2hunter2"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if user, _ := s.basicAuth().Credentials(); user != "alice" {
		t.Fatalf("user = %q, want the trimmed name", user)
	}
}

// TrimSpace runs before the injection check, so a name that is only surrounded
// by line breaks is a valid name and not a rejected one. Pinning it keeps the
// trim from being dropped later and turning a paste accident into a 400.
func TestHandleAuthPutTrimsSurroundingLineBreaks(t *testing.T) {
	for _, name := range []string{"ops\n", "ops\r", "\nops\r\n"} {
		t.Run(strings.ReplaceAll(name, "\n", "\\n"), func(t *testing.T) {
			s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})
			body, err := json.Marshal(map[string]string{
				"username":        name,
				"password":        "brandnewpass",
				"currentPassword": "hunter2hunter2",
			})
			if err != nil {
				t.Fatal(err)
			}

			rec := httptest.NewRecorder()
			s.handleAuthPut(rec, authRequest(http.MethodPut, string(body)))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 for %q: %s", rec.Code, name, rec.Body.String())
			}
			if user, _ := s.basicAuth().Credentials(); user != "ops" {
				t.Fatalf("user = %q, want the trimmed name", user)
			}
		})
	}
}

// minPasswordLength is enforced server-side, so pin both sides of the boundary:
// a direct API call must not be able to install a seven-character password, and
// must not reject the eight-character one the browser also allows.
func TestHandleAuthPutPasswordLengthBoundary(t *testing.T) {
	t.Run("seven characters is refused", func(t *testing.T) {
		s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})

		rec := httptest.NewRecorder()
		s.handleAuthPut(rec, authRequest(http.MethodPut,
			`{"username":"ops","password":"1234567","currentPassword":"hunter2hunter2"}`))

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
		assertAccountUnchanged(t, s, "ops", "hunter2hunter2")
	})

	t.Run("eight characters is accepted", func(t *testing.T) {
		s := authServerWith(t, &BasicAuth{User: "ops", Pass: "hunter2hunter2"})

		rec := httptest.NewRecorder()
		s.handleAuthPut(rec, authRequest(http.MethodPut,
			`{"username":"ops","password":"12345678","currentPassword":"hunter2hunter2"}`))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		if _, pass := s.basicAuth().Credentials(); pass != "12345678" {
			t.Fatalf("pass = %q, want the accepted eight-character password", pass)
		}
	})
}

// A change that cannot be written to disk still takes effect for this session,
// and the response has to say so: reporting a plain success would tell the
// operator their new password survives a restart when it does not.
func TestHandleAuthPutReportsUnsavedChange(t *testing.T) {
	cfg := &BasicAuth{
		User: "ops", Pass: "hunter2hunter2",
		Persist: func(string, string) error { return errors.New("disk full") },
	}
	s := authServerWith(t, cfg)

	rec := httptest.NewRecorder()
	s.handleAuthPut(rec, authRequest(http.MethodPut,
		`{"username":"ops","password":"brandnewpass","currentPassword":"hunter2hunter2"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if ok, _ := body["ok"].(bool); ok {
		t.Fatalf("ok = true, want false when the write failed: %v", body)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "could not be saved") {
		t.Fatalf("message = %q, want it to say the change was not saved", msg)
	}
	// The in-memory change stands; reverting would log the operator out with a
	// password the console had already accepted.
	if _, pass := cfg.Credentials(); pass != "brandnewpass" {
		t.Fatalf("pass = %q, want the change applied in memory", pass)
	}
}
