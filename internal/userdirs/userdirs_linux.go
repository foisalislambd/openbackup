//go:build linux || freebsd || openbsd || netbsd

package userdirs

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// xdgKeys maps the freedesktop user-dirs keys to our folder kinds.
var xdgKeys = map[string]Kind{
	"XDG_DESKTOP_DIR":   KindDesktop,
	"XDG_DOCUMENTS_DIR": KindDocuments,
	"XDG_PICTURES_DIR":  KindPictures,
	"XDG_VIDEOS_DIR":    KindVideos,
	"XDG_MUSIC_DIR":     KindMusic,
	"XDG_DOWNLOAD_DIR":  KindDownloads,
}

// detect reads ~/.config/user-dirs.dirs, which is where desktop environments
// record localised folder names ("Documenti", "Bilder"), and falls back to the
// English defaults.
func detect() map[Kind]string {
	home := Home()
	out := make(map[Kind]string, len(xdgKeys))
	if home == "" {
		return out
	}

	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	if f, err := os.Open(filepath.Join(configHome, "user-dirs.dirs")); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			key, value, ok := parseUserDirsLine(sc.Text(), home)
			if !ok {
				continue
			}
			if kind, known := xdgKeys[key]; known {
				out[kind] = value
			}
		}
	}

	defaults := map[Kind]string{
		KindDesktop:   "Desktop",
		KindDocuments: "Documents",
		KindPictures:  "Pictures",
		KindVideos:    "Videos",
		KindMusic:     "Music",
		KindDownloads: "Downloads",
	}
	for kind, name := range defaults {
		if _, ok := out[kind]; !ok {
			out[kind] = filepath.Join(home, name)
		}
	}
	return out
}

// parseUserDirsLine parses lines of the form XDG_MUSIC_DIR="$HOME/Music".
func parseUserDirsLine(line, home string) (key, value string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	key, rawValue, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	rawValue = strings.Trim(strings.TrimSpace(rawValue), `"`)
	if rawValue == "" {
		return "", "", false
	}
	switch {
	case strings.HasPrefix(rawValue, "$HOME/"):
		rawValue = filepath.Join(home, strings.TrimPrefix(rawValue, "$HOME/"))
	case rawValue == "$HOME":
		// A folder pointing at $HOME itself is a misconfiguration; backing up
		// the whole home directory here would surprise the user.
		return "", "", false
	case !filepath.IsAbs(rawValue):
		rawValue = filepath.Join(home, rawValue)
	}
	return key, filepath.Clean(rawValue), true
}
