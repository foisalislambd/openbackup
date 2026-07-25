// Package userdirs discovers the well-known folders that hold user data.
//
// Zero configuration means the agent has to be right about what "my files"
// means on each platform, including when the user has moved Documents to
// another drive or when OneDrive has redirected it. Every lookup therefore asks
// the operating system rather than guessing from the home directory layout.
package userdirs

import (
	"os"
	"path/filepath"
	"sort"
)

// Kind identifies a well-known folder.
type Kind string

// Well-known folder kinds.
const (
	KindDesktop   Kind = "desktop"
	KindDocuments Kind = "documents"
	KindPictures  Kind = "pictures"
	KindVideos    Kind = "videos"
	KindMusic     Kind = "music"
	KindDownloads Kind = "downloads"
)

// Dir is a discovered folder.
type Dir struct {
	Kind Kind `json:"kind"`
	// Label is the display name shown in the UI.
	Label string `json:"label"`
	// Path is the absolute path as reported by the OS.
	Path string `json:"path"`
	// Exists reports whether the folder is present on disk.
	Exists bool `json:"exists"`
	// DefaultOn reports whether the folder is selected out of the box.
	// Downloads is off by default: it is usually re-downloadable bulk, and
	// including it is the fastest way to turn a 5 GiB backup into 300 GiB.
	DefaultOn bool `json:"default_on"`
}

var labels = map[Kind]string{
	KindDesktop:   "Desktop",
	KindDocuments: "Documents",
	KindPictures:  "Pictures",
	KindVideos:    "Videos",
	KindMusic:     "Music",
	KindDownloads: "Downloads",
}

// order fixes the presentation order so the UI is stable across platforms.
var order = []Kind{KindDesktop, KindDocuments, KindPictures, KindVideos, KindMusic, KindDownloads}

// Detect returns the well-known folders for the current user. Folders that the
// OS does not report are omitted; folders that are reported but missing on disk
// are returned with Exists false so the UI can explain the gap.
func Detect() []Dir {
	found := detect()
	out := make([]Dir, 0, len(order))
	for _, kind := range order {
		path, ok := found[kind]
		if !ok || path == "" {
			continue
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		st, statErr := os.Stat(abs)
		out = append(out, Dir{
			Kind:      kind,
			Label:     labels[kind],
			Path:      abs,
			Exists:    statErr == nil && st.IsDir(),
			DefaultOn: kind != KindDownloads,
		})
	}
	return out
}

// DefaultPaths returns the paths that a fresh install backs up.
func DefaultPaths() []string {
	var out []string
	for _, d := range Detect() {
		if d.DefaultOn && d.Exists {
			out = append(out, d.Path)
		}
	}
	sort.Strings(out)
	return out
}

// Home returns the user's home directory, or an empty string if unknown.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
