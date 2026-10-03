package api

import (
	"net/http"

	"sliverreshine/internal/ui/sliver"
)

// --- Websites management ---

func (s *Server) handleWebsites(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	websites, err := c.Websites()
	writeResult(w, map[string]any{"websites": websites}, err)
}

func (s *Server) handleWebsite(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	website, err := c.Website(r.PathValue("name"))
	if err != nil {
		writeClientError(w, err)
		return
	}
	if website == nil {
		writeErr(w, http.StatusNotFound, "website not found")
		return
	}
	writeJSON(w, http.StatusOK, website)
}

func (s *Server) handleWebsiteAddContent(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req sliver.WebsiteContentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	website, err := c.WebsiteAddContent(r.PathValue("name"), &req)
	writeResult(w, website, err)
}

func (s *Server) handleWebsiteUpdateContent(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req sliver.WebsiteContentRequest
	if !decodeBody(w, r, &req) {
		return
	}
	website, err := c.WebsiteUpdateContent(r.PathValue("name"), &req)
	writeResult(w, website, err)
}

func (s *Server) handleWebsiteRemoveContent(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	website, err := c.WebsiteRemoveContent(r.PathValue("name"), req.Paths)
	writeResult(w, website, err)
}

func (s *Server) handleWebsiteRemove(c *sliver.Client, w http.ResponseWriter, r *http.Request) {
	if err := c.WebsiteRemove(r.PathValue("name")); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
