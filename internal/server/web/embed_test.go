package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/foisalislambd/openbackup/internal/server/web"
)

// export mimics what `vite build` writes: a single index.html shell plus hashed
// assets under assets/.
func export() fstest.MapFS {
	return fstest.MapFS{
		"index.html":             {Data: []byte("<title>OpenBackup</title><div id=root>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
		"favicon.svg":            {Data: []byte("<svg></svg>")},
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

// TestClientRoutesServeSpaShell is the difference between a bookmark working
// and a blank page: /devices must serve index.html so React Router can take over.
func TestClientRoutesServeSpaShell(t *testing.T) {
	h := web.HandlerFor(export())
	if h == nil {
		t.Fatal("HandlerFor returned nil for a filesystem containing a dashboard")
	}

	for _, path := range []string{"/", "/devices", "/backups", "/settings", "/devices/"} {
		resp, body := get(t, h, path)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
			continue
		}
		if !strings.Contains(body, "OpenBackup") {
			t.Errorf("GET %s served %q, want the SPA shell", path, body)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, cc)
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

// TestMissingAssetIsNotFound guards against answering with the SPA shell for a
// missing JS/CSS file, which would break the dashboard after a bad deploy.
func TestMissingAssetIsNotFound(t *testing.T) {
	h := web.HandlerFor(export())

	for _, path := range []string{"/assets/missing.js", "/favicon.ico", "/nonsense.txt"} {
		resp, _ := get(t, h, path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestAssetCachingRules keeps a deployed update from being invisible: hashed
// assets may be cached forever, HTML must be revalidated.
func TestAssetCachingRules(t *testing.T) {
	h := web.HandlerFor(export())

	resp, _ := get(t, h, "/assets/index-abc123.js")
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
