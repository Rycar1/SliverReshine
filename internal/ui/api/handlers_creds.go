package api

import (
	"net/http"
	"strconv"

	"sliverreshine/internal/ui/sliver"
)

func (s *Server) handleCreds(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	creds, err := c.Creds()
	if err != nil {
		writeClientError(w, err)
		return
	}
	creds, ok := paginate(w, r, creds)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

func (s *Server) handleCredsAdd(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credentials []sliver.CredentialView `json:"credentials"`
		// Convenience form for a single entry typed straight into the UI.
		Username   string `json:"username"`
		Plaintext  string `json:"plaintext"`
		Hash       string `json:"hash"`
		HashType   int32  `json:"hashType"`
		Collection string `json:"collection"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	batch := req.Credentials
	if len(batch) == 0 {
		batch = []sliver.CredentialView{{
			Username:   req.Username,
			Plaintext:  req.Plaintext,
			Hash:       req.Hash,
			HashType:   req.HashType,
			Collection: req.Collection,
		}}
	}
	if err := c.CredsAdd(batch); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "added": len(batch)})
}

func (s *Server) handleCredsRemove(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
		ID  string   `json:"id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ids := req.IDs
	if len(ids) == 0 && req.ID != "" {
		ids = []string{req.ID}
	}
	if err := c.CredsRm(ids); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCredsUpdate(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Credentials []sliver.CredentialView `json:"credentials"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.CredsUpdate(req.Credentials); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCredByID(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	cred, err := c.GetCredByID(r.PathValue("id"))
	writeResult(w, cred, err)
}

func (s *Server) handleCredsSniff(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hash string `json:"hash"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	cred, err := c.CredsSniffHashType(req.Hash)
	writeResult(w, cred, err)
}

// handleCredsByHashType serves both filtered variants: ?plaintext=1 narrows to

func (s *Server) handleCredsByHashType(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	hashType := int32(0)
	if v := r.URL.Query().Get("type"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid hash type")
			return
		}
		hashType = int32(n)
	}
	var (
		creds []sliver.CredentialView
		err   error
	)
	if r.URL.Query().Get("plaintext") == "1" {
		creds, err = c.GetPlaintextCredsByHashType(hashType)
	} else {
		creds, err = c.GetCredsByHashType(hashType)
	}
	if err != nil {
		writeClientError(w, err)
		return
	}
	creds, ok := paginate(w, r, creds)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": creds})
}

// ---------------------------------------------------------------------------
// Memfiles
// ---------------------------------------------------------------------------
