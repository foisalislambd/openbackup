// Package web embeds the compiled dashboard into the server binary.
//
// The dashboard is a Next.js app exported as static files ("output: export").
// Embedding it means a deployment is one binary and one data directory: no Node
// runtime, no second container, no reverse-proxy rules to get the UI and the API
// on the same origin. The dashboard is entirely client-rendered against the
// authenticated API, so server-side rendering would buy nothing.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist holds the exported dashboard. It is populated by 'make web'; a fresh
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
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return &spaHandler{fs: sub, files: http.FileServer(http.FS(sub))}
}

// spaHandler serves static assets and falls back to index.html for client-side
// routes, so a deep link such as /devices/dev_123 survives a page refresh.
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
		// Hashed build assets are immutable; everything else must revalidate so
		// a deployed update is picked up on the next load.
		if strings.HasPrefix(name, "_next/static/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		h.files.ServeHTTP(w, r)
		return
	}

	// Static export writes /devices as devices.html; try that before falling
	// back to the SPA shell.
	if _, err := fs.Stat(h.fs, name+".html"); err == nil {
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + name + ".html"
		w.Header().Set("Cache-Control", "no-cache")
		h.files.ServeHTTP(w, r2)
		return
	}

	index, err := fs.ReadFile(h.fs, "index.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(index)
}
