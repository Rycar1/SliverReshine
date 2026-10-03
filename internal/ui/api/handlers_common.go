package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"c2tool/internal/ui/sliver"
)

func (s *Server) sessionID(w http.ResponseWriter, r *http.Request) (string, *sliver.Client) {
	c := s.clientFor(w, r)
	if c == nil {
		return "", nil
	}
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "invalid session id")
		return "", nil
	}
	return id, c
}

// decodeBody reads a JSON request body into v.
//
// Unknown fields are rejected rather than ignored. A field the server does not
// know is either a typo or a client built against a different version, and in
// both cases silently dropping it means the operator gets a 200 for a request
// that did not do what they asked -- "autoAdd" spelled "auto_add" would run the
// harvest and quietly not store anything. The settings file already refuses

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		// A body past the middleware cap is not a malformed request, so it must
		// not be answered with 400. Reporting it as 413 keeps the operator's
		// client able to tell "my payload is too big" from "my JSON is wrong",
		// which are fixed differently.
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
			return false
		}
		writeErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// --- Filesystem ---
