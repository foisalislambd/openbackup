// Package web embeds the compiled dashboard into the server binary.
//
// The dashboard is a Vite React SPA. Embedding it means a deployment is one
// binary and one data directory: no Node runtime, no second container, no
// reverse-proxy rules to get the UI and the API on the same origin. The
// dashboard is entirely client-rendered against the authenticated API.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist holds the built dashboard. It is populated by 'make web'; a fresh
// checkout contains only a placeholder, and the server then runs headless.
//
//go:embed all:dist
var dist embed.FS

// Handler returns an HTTP handler for the dashboard, or nil when no dashboard
// was bundled into this build.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil
	}
	return HandlerFor(sub)
}

// HandlerFor serves a dashboard build from any filesystem, and returns nil when
// the filesystem holds no dashboard. Taking an fs.FS keeps the routing rules
// testable without a built dashboard in the tree.
func HandlerFor(fsys fs.FS) http.Handler {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil
	}
	return &spaHandler{fs: fsys, files: http.FileServer(http.FS(fsys))}
}

// spaHandler serves the Vite SPA.
//
// Hashed assets under assets/ are served directly. Everything else falls back
// to index.html so client-side routes (/devices, /backups?id=…) keep working on
// refresh and bookmark.
type spaHandler struct {
	fs    fs.FS
	files http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	if info, err := fs.Stat(h.fs, name); err == nil && !info.IsDir() {
		// Hashed build assets are immutable; everything else must revalidate so a
		// deployed update is picked up on the next load.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.files.ServeHTTP(w, r)
		return
	}

	// Missing file that looks like a static asset: real 404, not the SPA shell.
	if looksLikeAsset(name) {
		http.NotFound(w, r)
		return
	}

	// Client route: serve the SPA shell without going through FileServer, which
	// would otherwise 301 /index.html → ./ relative to the original path.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page, err := fs.ReadFile(h.fs, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(page)
}

func looksLikeAsset(name string) bool {
	if strings.HasPrefix(name, "assets/") {
		return true
	}
	base := path.Base(name)
	return strings.Contains(base, ".")
}
