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
	return HandlerFor(sub)
}

// HandlerFor serves a dashboard export from any filesystem, and returns nil when
// the filesystem holds no dashboard. Taking an fs.FS keeps the routing rules
// testable without a built dashboard in the tree.
func HandlerFor(fsys fs.FS) http.Handler {
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil
	}
	return &spaHandler{fs: fsys, files: http.FileServer(http.FS(fsys))}
}

// spaHandler serves the exported dashboard.
//
// The export is one HTML file per route rather than a single shell, so a request
// for /devices must find devices.html. That is what makes a bookmark or a refresh
// land on the right page instead of on the overview.
type spaHandler struct {
	fs    fs.FS
	files http.Handler
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}

	// Candidates in the order a static host would try them: the exact file, the
	// route's HTML file, then a directory index.
	for _, candidate := range []string{name, name + ".html", path.Join(name, "index.html")} {
		info, err := fs.Stat(h.fs, candidate)
		if err != nil || info.IsDir() {
			continue
		}
		// Hashed build assets are immutable; everything else must revalidate so a
		// deployed update is picked up on the next load.
		if strings.HasPrefix(candidate, "_next/static/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		req := r
		if candidate != name {
			req = r.Clone(r.Context())
			req.URL.Path = "/" + candidate
		}
		h.files.ServeHTTP(w, req)
		return
	}

	// Nothing matched. Serve the exported not-found page with a 404 status:
	// answering 200 with the overview page would tell a browser, a crawler and a
	// monitoring check alike that a mistyped URL was fine.
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page, err := fs.ReadFile(h.fs, "404.html")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write(page)
}
