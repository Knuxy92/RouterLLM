package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

//go:embed all:dist
var assets embed.FS

func MountUI(r chi.Router) {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return
	}

	index, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return
	}

	files := http.FileServer(http.FS(dist))

	r.Get("/admin", http.RedirectHandler("/admin/", http.StatusFound).ServeHTTP)
	r.Get("/admin/*", func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/admin/")
		if path == "" {
			serveIndex(w, index)
			return
		}

		if _, err := fs.Stat(dist, path); err != nil {
			serveIndex(w, index)
			return
		}

		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		req.URL.Path = "/" + path
		files.ServeHTTP(w, req)
	})
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(index)
}
