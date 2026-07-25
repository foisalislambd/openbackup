//go:build darwin

package userdirs

import "path/filepath"

// detect uses the fixed macOS home directory layout. Unlike Windows, these
// names are stable on disk even when Finder shows a localised name, and iCloud
// Drive redirection keeps ~/Documents in place as a symlinked container that we
// follow explicitly when the user opts in.
func detect() map[Kind]string {
	home := Home()
	if home == "" {
		return map[Kind]string{}
	}
	return map[Kind]string{
		KindDesktop:   filepath.Join(home, "Desktop"),
		KindDocuments: filepath.Join(home, "Documents"),
		KindPictures:  filepath.Join(home, "Pictures"),
		KindVideos:    filepath.Join(home, "Movies"),
		KindMusic:     filepath.Join(home, "Music"),
		KindDownloads: filepath.Join(home, "Downloads"),
	}
}
