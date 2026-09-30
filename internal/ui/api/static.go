package api

import (
	"io"
	"io/fs"
	"net/http"
	"strings"

	"c2tool/web"
)

var staticFS = func() fs.FS {
	sub, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}()

var indexHTML = func() []byte {
	data, err := fs.ReadFile(staticFS, "index.html")
	if err != nil {
		return nil
	}
	return data
}()

// handleStatic serves the embedded frontend build with an SPA fallback so
// client-side routes (e.g. /sessions/abc) work on refresh.
func handleStatic(w http.ResponseWriter, r *http.Request) {
	serveStatic(w, r, staticFS, indexHTML)
}

// serveStatic serves a built frontend from fsys, falling back to the SPA
// index for any path that is not a real asset file.
func serveStatic(w http.ResponseWriter, r *http.Request, fsys fs.FS, index []byte) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if !fs.ValidPath(path) {
		http.NotFound(w, r)
		return
	}
	if f, err := fsys.Open(path); err == nil {
		f.Close()
		http.ServeFileFS(w, r, fsys, path)
		return
	}
	if index == nil {
		// No production frontend build present (e.g. dev binary).
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "Sliver UI API is running. Run `npm run build` in frontend/ and rebuild the backend to serve the web interface.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(index)
}
