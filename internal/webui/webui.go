package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed web/dist
var staticFiles embed.FS

// Handler returns an http.Handler that serves the embedded frontend SPA.
// Files that exist in web/dist are served directly (JS, CSS, images, etc.).
// All other paths return index.html to support client-side routing.
func Handler() http.Handler {
	distFS, err := fs.Sub(staticFiles, "web/dist")
	if err != nil {
		panic("webui: sub embed FS: " + err.Error())
	}
	return &spaHandler{
		fs:         distFS,
		fileServer: http.FileServer(http.FS(distFS)),
	}
}

type spaHandler struct {
	fs         fs.FS
	fileServer http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	// Serve the file directly if it exists in dist.
	if f, err := h.fs.Open(path); err == nil {
		_ = f.Close()
		h.fileServer.ServeHTTP(w, r)
		return
	}

	// SPA fallback: rewrite to "/" so FileServer finds and serves index.html.
	// We cannot rewrite to "/index.html" because http.FileServer 301-redirects
	// that path back to "/" as a canonical redirect.
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/"
	h.fileServer.ServeHTTP(w, r2)
}
