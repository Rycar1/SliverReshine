package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// minPasswordLength is enforced here rather than only in the browser so a
// direct API call cannot install a one-character console password.
const minPasswordLength = 8

// authSettingsView is what the Settings panel renders. The password is never
// echoed back, not even masked: the console only ever needs to confirm which
// account is in use, and a value in the response would end up in browser
// history, logs, and screenshots for no benefit.
type authSettingsView struct {
	Username string `json:"username"`
	Enabled  bool   `json:"enabled"`
	Source   string `json:"source"`
}

type authChangeRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	CurrentPassword string `json:"currentPassword"`
}

// handleAuthGet reports the account currently guarding the console.
func (s *Server) handleAuthGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.basicAuth()
	user, _ := cfg.Credentials()
	writeJSON(w, http.StatusOK, authSettingsView{
		Username: user,
		Enabled:  cfg.Enabled(),
		Source:   "console-auth",
	})
}

// handleAuthPut changes the console account.
//
// This is the one place the account can change at runtime, and it writes
// through to the same record the browser logs in against, so the credential the
// operator just chose is the credential the next request and the next restart
// both expect. Requiring the current password means a borrowed browser tab
// cannot silently lock the operator out of their own console.
func (s *Server) handleAuthPut(w http.ResponseWriter, r *http.Request) {
	cfg := s.basicAuth()
	if !cfg.Enabled() {
		// Refused on purpose. An unauthenticated console is already fully open,
		// so accepting a change here would hand anyone who can reach the port a
		// trivial lockout: set a password, and the operator is locked out of a
		// console that was previously open to them.
		writeErr(w, http.StatusConflict,
			"the console has no account yet; set one with -auth-user/-auth-pass or C2TOOL_AUTH_USER/C2TOOL_AUTH_PASS and restart")
		return
	}

	var req authChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	user, pass := cfg.Credentials()
	if req.CurrentPassword != pass {
		writeErr(w, http.StatusForbidden, "current password is incorrect")
		return
	}

	nextUser := strings.TrimSpace(req.Username)
	if nextUser == "" {
		nextUser = user
	}
	if strings.ContainsAny(nextUser, ":\r\n") {
		writeErr(w, http.StatusBadRequest, "username must not contain ':', CR or LF")
		return
	}
	if len(req.Password) < minPasswordLength {
		writeErr(w, http.StatusBadRequest,
			"password must be at least 8 characters")
		return
	}
	if strings.ContainsAny(req.Password, "\r\n") {
		writeErr(w, http.StatusBadRequest, "password must not contain CR or LF")
		return
	}
	if nextUser == user && req.Password == pass {
		writeErr(w, http.StatusBadRequest, "username and password are unchanged")
		return
	}

	if err := cfg.SetCredentials(nextUser, req.Password); err != nil {
		// The in-memory change already took effect, so the operator is logged
		// in under the new account but it was not written to disk. Saying so is
		// the only way they can tell that the change will not survive a restart.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      false,
			"message": "credentials changed for this session but could not be saved: " + err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"message": "credentials updated; the next request and the next restart use them",
	})
}
