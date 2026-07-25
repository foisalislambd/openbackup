package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/openbackup/openbackup/internal/server/web"
)

// export mimics what `next build` writes with output: 'export' and
// trailingSlash: false — one HTML file per route, plus a 404 page and hashed
// assets.
func export() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                   {Data: []byte("<title>overview</title>")},
		"devices.html":                 {Data: []byte("<title>devices</title>")},
		"backups.html":                 {Data: []byte("<title>backups</title>")},
		"404.html":                     {Data: []byte("<title>not found</title>")},
		"_next/static/chunk.abc123.js": {Data: []byte("console.log(1)")},
	}
}

func get(t *testing.T, h http.Handler, path string) (*http.Response, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	resp := rec.Result()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp, string(body)
}

// TestRoutesResolveToTheirOwnPage is the difference between a bookmark working
// and a bookmark quietly showing the wrong screen: /devices must serve
// devices.html, not the overview.
func TestRoutesResolveToTheirOwnPage(t *testing.T) {
	h := web.HandlerFor(export())
	if h == nil {
		t.Fatal("HandlerFor returned nil for a filesystem containing a dashboard")
	}

	cases := map[string]string{
		"/":             "overview",
		"/devices":      "devices",
		"/backups":      "backups",
		"/devices.html": "devices",
		"/devices/":     "devices",
		"//devices":     "devices",
		// Traversal must resolve inside the export rather than escape it.
		"/../devices": "devices",
	}
	for path, want := range cases {
		resp, body := get(t, h, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, want) {
			t.Errorf("GET %s served %q, want the %s page", path, body, want)
		}
	}

	// /index.html redirects to / rather than serving a second URL for the same
	// page, which is what net/http's file server does and what we want.
	resp, _ := get(t, h, "/index.html")
	if resp.StatusCode != http.StatusMovedPermanently || resp.Header.Get("Location") != "./" {
		t.Errorf("GET /index.html = %d %q, want a permanent redirect to ./",
			resp.StatusCode, resp.Header.Get("Location"))
	}
}

// TestUnknownPathIsNotFound guards against answering 200 with the overview page
// for a mistyped URL, which would tell browsers, crawlers and uptime checks alike
// that a broken link is fine.
func TestUnknownPathIsNotFound(t *testing.T) {
	h := web.HandlerFor(export())

	for _, path := range []string{"/nonsense", "/devices/nested/deep", "/_next/static"} {
		resp, body := get(t, h, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
		if path == "/nonsense" && !strings.Contains(body, "not found") {
			t.Errorf("GET %s served %q, want the exported 404 page", path, body)
		}
	}
}

// TestAssetCachingRules keeps a deployed update from being invisible: hashed
// assets may be cached forever, HTML must be revalidated.
func TestAssetCachingRules(t *testing.T) {
	h := web.HandlerFor(export())

	resp, _ := get(t, h, "/_next/static/chunk.abc123.js")
	if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Errorf("hashed asset Cache-Control = %q, want it cached immutably", cc)
	}

	resp, _ = get(t, h, "/devices")
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("HTML Cache-Control = %q, want no-cache", cc)
	}
}

// TestNoDashboardBundled covers a fresh checkout, where the server must run
// headless rather than serve a broken page.
func TestNoDashboardBundled(t *testing.T) {
	if h := web.HandlerFor(fstest.MapFS{".gitkeep": {Data: []byte("placeholder")}}); h != nil {
		t.Error("HandlerFor must return nil when no dashboard was built")
	}
}
