package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"sliverreshine/internal/ui/sliver"
)

// maxViewBytes caps what the in-console viewer will load.
//
// The viewer renders the file as text in the page, so a large file is not a slow
// page -- it is a frozen tab, because the whole thing has to be base64-decoded
// and painted as one text node. Anything bigger is a download, not a preview.
const maxViewBytes = 5 * 1024 * 1024

// sizeLabel renders a byte count for the refusal message, which the operator
// reads in the error banner.
func sizeLabel(n int64) string {
	if n >= 1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

func headerSafeFilename(name, path string) string {
	base := name
	if base == "" {
		base = path
	}
	// Windows and POSIX separators both, since the target decides which.
	base = base[strings.LastIndexAny(base, `/\`)+1:]

	var b strings.Builder
	for _, r := range base {
		switch r {
		case '"', '\\', '\r', '\n', 0:
			continue
		}
		b.WriteRune(r)
	}
	// A name that was nothing but filtered characters leaves an empty header,
	// which some browsers reject outright.
	if b.Len() == 0 {
		return "download"
	}
	return b.String()
}

func (s *Server) handleFsList(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		p, err := c.Pwd(id)
		if err != nil {
			writeClientError(w, err)
			return
		}
		path = p
	}
	dir, err := c.Ls(id, path)
	writeResult(w, dir, err)
}

func (s *Server) handleFsPwd(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path, err := c.Pwd(id)
	writeResult(w, map[string]string{"Path": path}, err)
}

func (s *Server) handleFsCd(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	path, err := c.Cd(id, req.Path)
	writeResult(w, map[string]string{"Path": path}, err)
}

// handleFsCat returns a file's contents as JSON, for the in-console viewer.
//
// It used to be a literal alias for handleFsDownload, which was correct until the
// download endpoint changed contract: that endpoint now streams raw bytes as an
// attachment (see below), so fsCat inherited it and started returning
// application/octet-stream with the file's bytes as the body. The frontend still
// calls fsCat through request<T>() and reads `res.Data` as base64, so the viewer
// broke -- it either surfaced a JSON parse error or rendered nonsense, depending
// on whether the file happened to be parseable JSON.
//
// The two endpoints want different things and now say so: cat is base64 in JSON

func (s *Server) handleFsCat(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}

	// Ask the target how big the file is before pulling it. Refusing after the
	// download would still drag the whole file through the implant and the
	// console, which is the freeze this limit exists to prevent.
	if size, isDir, ok := c.Stat(id, path); ok && !isDir && size > maxViewBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"file is %s; the viewer only opens files up to %s -- download it instead",
			sizeLabel(size), sizeLabel(maxViewBytes)))
		return
	}

	b64, name, err := c.Download(id, path)
	if err != nil {
		writeClientError(w, err)
		return
	}

	// Name falls back to the last path element so the viewer always has a title,
	// matching what the operator asked for rather than blank.
	if name == "" {
		name = headerSafeFilename("", path)
	}

	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "the file could not be decoded")
		return
	}

	// The stat above can be unavailable (an implant that will not list a file),
	// so the size is checked again on the bytes that actually arrived.
	if len(data) > maxViewBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"file is %s; the viewer only opens files up to %s -- download it instead",
			sizeLabel(int64(len(data))), sizeLabel(maxViewBytes)))
		return
	}

	// Binary files are refused with a flag rather than an error: it is a normal
	// answer, and the viewer shows the message instead of a wall of mojibake.
	if sliver.LooksBinary(data) {
		writeJSON(w, http.StatusOK, map[string]any{
			"Name":   name,
			"Binary": true,
			"Size":   len(data),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"Data": b64,
		"Name": name,
	})
}

// handleFsDownload streams a file from the target to the browser.
//
// The response is the file itself, not JSON. The previous shape was
// {"Data":"<base64>","Name":"..."}, which inflated the payload by a third and
// required the whole thing to exist as a string in Go and again in the browser
// before a single byte could be saved -- so a large file was a memory problem on
// both ends rather than a slow download. The minidump endpoint next door already

func (s *Server) handleFsDownload(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}

	b64, name, err := c.Download(id, path)
	if err != nil {
		writeClientError(w, err)
		return
	}

	// Download returns base64 because that is what the RPC hands back. It is
	// decoded here rather than in the client so the bytes are the only thing
	// that leaves this function: nothing downstream sees the inflated form.
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "the downloaded file could not be decoded")
		return
	}

	// The name comes from the target and is shown in the operator's Downloads
	// folder, so it is reduced to its last path element and stripped of the
	// bytes that would let it break out of the quoted header value. A Windows
	// path arrives with backslashes; url.PathEscape would mangle the name a user
	// sees, so the quoting is done here instead.
	filename := headerSafeFilename(name, path)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// headerSafeFilename reduces a path to a filename that is safe inside a quoted
// Content-Disposition value.
//
// Two things matter. Only the last element is used, so a target-supplied path
// cannot become a directory traversal in the operator's downloads. And quote,
// backslash, CR and LF are dropped, because any of them would end the quoted
// string early and let the remainder be read as another header -- a response
// splitting primitive reachable through a filename.
//
// The result is deliberately not percent-encoded: the browser shows this string

func (s *Server) handleFsUpload(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
		Data string `json:"data"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid base64 data")
		return
	}
	if err := c.Upload(id, req.Path, data); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleFsMkdir(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Mkdir(id, req.Path); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleFsRm(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	path := r.URL.Query().Get("path")
	recursive := r.URL.Query().Get("recursive") == "1" || r.URL.Query().Get("recursive") == "true"
	if path == "" {
		writeErr(w, http.StatusBadRequest, "missing path")
		return
	}
	if err := c.Rm(id, path, recursive); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *Server) handleFsMv(w http.ResponseWriter, r *http.Request) {
	id, c := s.sessionID(w, r)
	if c == nil {
		return
	}
	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := c.Mv(id, req.Src, req.Dst); err != nil {
		writeClientError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// --- Recon ---
