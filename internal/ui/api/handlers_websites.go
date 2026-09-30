package api

import (
	"net/http"

	"c2tool/internal/ui/sliver"
)

// --- Websites management ---

func (s *Server) handleWebsites(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	websites, err := c.Websites()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"websites": websites})
}

func (s *Server) handleWebsite(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	website, err := c.Website(r.PathValue("name"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if website == nil {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	writeJSON(w, http.StatusOK, website)
}

func (s *Server) handleWebsiteAddContent(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.WebsiteContentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	website, err := c.WebsiteAddContent(r.PathValue("name"), &req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, website)
}

func (s *Server) handleWebsiteUpdateContent(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req sliver.WebsiteContentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	website, err := c.WebsiteUpdateContent(r.PathValue("name"), &req)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, website)
}

func (s *Server) handleWebsiteRemoveContent(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	website, err := c.WebsiteRemoveContent(r.PathValue("name"), req.Paths)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, website)
}

func (s *Server) handleWebsiteRemove(w http.ResponseWriter, r *http.Request) {
	c := s.requireClient(w)
	if c == nil {
		return
	}
	if err := c.WebsiteRemove(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
