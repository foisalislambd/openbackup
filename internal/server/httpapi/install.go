package httpapi

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/foisalislambd/openbackup/internal/version"
)

// installScript is served at /install.sh so a new machine can be set up with one
// command that points at the user's own server:
//
//	curl -fsSL https://backup.example.com/install.sh | sh
//
// Serving it from the server rather than a third-party domain means the script,
// the binary's address and the backups all come from the same host the user
// already decided to trust.
//
//go:embed install.sh
var installScript string

// handleInstallScript returns the installer with this server's URL baked in, so
// the printed instructions name the right host without the user editing anything.
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	serverURL := s.cfg.PublicURL
	if serverURL == "" {
		// Fall back to the host the request arrived on. Behind a reverse proxy
		// the scheme is only knowable from the forwarded header, and guessing
		// http would hand out a URL that leaks a device token, so assume https
		// unless the request itself was plain and local.
		scheme := "https"
		if r.TLS == nil && (s.cfg.TrustProxy && !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")) {
			scheme = "http"
		} else if r.TLS == nil && isLoopbackHost(r.Host) {
			scheme = "http"
		}
		serverURL = scheme + "://" + r.Host
	}

	script := strings.ReplaceAll(installScript, "__SERVER_URL__", serverURL)
	script = strings.ReplaceAll(script, "__VERSION__", version.Version)

	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	// The script is generated per host, and a stale cached copy pointing at an old
	// URL would be confusing rather than fast.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(script))
}

func isLoopbackHost(host string) bool {
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host[i:], "]") {
		host = host[:i]
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
