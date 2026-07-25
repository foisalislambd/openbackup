//go:build windows

package userdirs

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// detect asks the Windows shell for each known folder.
//
// Reading the registry or joining names onto %USERPROFILE% would break for the
// large number of users whose Documents and Pictures have been redirected into
// OneDrive, or moved to a second drive to save space on a small SSD.
func detect() map[Kind]string {
	ids := map[Kind]*windows.KNOWNFOLDERID{
		KindDesktop:   windows.FOLDERID_Desktop,
		KindDocuments: windows.FOLDERID_Documents,
		KindPictures:  windows.FOLDERID_Pictures,
		KindVideos:    windows.FOLDERID_Videos,
		KindMusic:     windows.FOLDERID_Music,
		KindDownloads: windows.FOLDERID_Downloads,
	}
	out := make(map[Kind]string, len(ids))
	for kind, id := range ids {
		// KF_FLAG_DEFAULT never creates the folder, which matters: the agent
		// must not create a Videos folder for a user who deleted theirs.
		p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
		if err != nil || p == "" {
			continue
		}
		out[kind] = filepath.Clean(p)
	}
	if len(out) == 0 {
		// Extremely locked-down systems can fail the shell lookup; fall back to
		// the conventional layout rather than backing up nothing.
		if home := Home(); home != "" {
			out[KindDesktop] = filepath.Join(home, "Desktop")
			out[KindDocuments] = filepath.Join(home, "Documents")
			out[KindPictures] = filepath.Join(home, "Pictures")
			out[KindVideos] = filepath.Join(home, "Videos")
			out[KindMusic] = filepath.Join(home, "Music")
			out[KindDownloads] = filepath.Join(home, "Downloads")
		}
	}
	return out
}
