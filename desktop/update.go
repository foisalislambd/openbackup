package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/version"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const githubReleasesAPI = "https://api.github.com/repos/foisalislambd/openbackup/releases/latest"
const githubReleasesPage = "https://github.com/foisalislambd/openbackup/releases/latest"

// UpdateCheck is the result of asking GitHub whether a newer desktop build exists.
type UpdateCheck struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url"`
	DownloadURL     string `json:"download_url,omitempty"`
	Name            string `json:"name,omitempty"`
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	HTMLURL string        `json:"html_url"`
	Name    string        `json:"name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// CheckForUpdates compares this build to the latest GitHub Release.
func (a *App) CheckForUpdates() (UpdateCheck, error) {
	out := UpdateCheck{
		Current:    version.Version,
		ReleaseURL: githubReleasesPage,
	}

	ctx, cancel := context.WithTimeout(a.context(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubReleasesAPI, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "openbackup-desktop/"+version.Version)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return out, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return out, fmt.Errorf("could not read the release list: %w", err)
	}
	if rel.TagName == "" {
		return out, fmt.Errorf("no release tag found")
	}

	out.Latest = rel.TagName
	if rel.HTMLURL != "" {
		out.ReleaseURL = rel.HTMLURL
	}
	out.Name = rel.Name
	out.DownloadURL = pickDesktopAsset(rel, goruntime.GOOS, goruntime.GOARCH)
	out.UpdateAvailable = versionNewer(rel.TagName, version.Version)
	return out, nil
}

// OpenUpdateDownload opens the installer download (or the releases page) in the browser.
func (a *App) OpenUpdateDownload(url string) {
	if strings.TrimSpace(url) == "" {
		url = githubReleasesPage
	}
	wailsruntime.BrowserOpenURL(a.context(), url)
}

func pickDesktopAsset(rel githubRelease, goos, goarch string) string {
	want := ""
	switch {
	case goos == "windows" && goarch == "amd64":
		want = "openbackup-desktop-windows-amd64-setup.exe"
	case goos == "windows" && goarch == "arm64":
		want = "openbackup-desktop-windows-arm64-setup.exe"
	case goos == "linux" && goarch == "amd64":
		want = "openbackup-desktop-linux-amd64"
	case goos == "linux" && goarch == "arm64":
		want = "openbackup-desktop-linux-arm64"
	}
	fallback := ""
	for _, a := range rel.Assets {
		name := strings.ToLower(a.Name)
		if want != "" && name == want {
			return a.BrowserDownloadURL
		}
		if goos == "windows" && strings.Contains(name, "windows") && strings.HasSuffix(name, "-setup.exe") {
			fallback = a.BrowserDownloadURL
		}
		if goos == "linux" && strings.Contains(name, "linux") && !strings.Contains(name, ".") {
			if fallback == "" {
				fallback = a.BrowserDownloadURL
			}
		}
	}
	return fallback
}

// versionNewer reports whether remote is a higher version than local.
// Accepts tags like v0.2.1 or 0.2.1; non-semver local builds (dev) treat any
// release as newer.
func versionNewer(remote, local string) bool {
	r := parseVersionParts(remote)
	l := parseVersionParts(local)
	if r == nil {
		return false
	}
	if l == nil {
		return true
	}
	for i := 0; i < 3; i++ {
		if r[i] != l[i] {
			return r[i] > l[i]
		}
	}
	return false
}

func parseVersionParts(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(strings.ToLower(v), "v")
	if v == "" || v == "dev" || v == "none" {
		return nil
	}
	// Drop build metadata / pre-release suffix for a simple compare.
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := []int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		n := 0
		for _, c := range parts[i] {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		out[i] = n
	}
	return out
}
