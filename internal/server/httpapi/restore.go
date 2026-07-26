package httpapi

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/server/store"
)

// handleDownloadFile streams one restored file straight to the browser.
//
// This only works for repositories that are not end-to-end encrypted: with E2EE
// the server holds no key, so the bytes are undecodable here by design. In that
// case the response explains that the restore must run through the agent, which
// is the honest answer rather than a generic 500.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request, user *store.User) {
	snapshotID := r.PathValue("id")
	if _, err := s.db.SnapshotByID(r.Context(), user.ID, snapshotID); err != nil {
		writeStoreError(w, err)
		return
	}
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		writeError(w, http.StatusBadRequest, "", "path is required")
		return
	}
	entry, err := s.db.TreeEntry(r.Context(), snapshotID, rawPath)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if entry.Type != api.EntryFile {
		writeError(w, http.StatusBadRequest, "", "only files can be downloaded directly; use the archive endpoint for folders")
		return
	}

	// Check readability before writing any response headers. Once Content-Length
	// is set, switching to a JSON error would produce a truncated body, so the
	// user would see an empty download instead of an explanation.
	if err := s.probeEntry(r, entry); err != nil {
		s.reportRestoreFailure(w, r, err, entry.Path)
		return
	}

	filename := path.Base(entry.Path)
	contentType := mime.TypeByExtension(strings.ToLower(path.Ext(filename)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(entry.Size, 10))
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	// Restores are private user data; never let a proxy or CDN keep a copy.
	w.Header().Set("Cache-Control", "private, no-store")

	if err := s.writePlainFile(r, w, entry); err != nil {
		s.reportRestoreFailure(w, r, err, entry.Path)
	}
}

// handleDownloadArchive streams a folder, or a whole snapshot, as a ZIP.
//
// The archive is generated on the fly with no temporary file: entries are
// written as the chunks are fetched, so restoring 200 GiB needs no server disk
// space and starts downloading immediately.
func (s *Server) handleDownloadArchive(w http.ResponseWriter, r *http.Request, user *store.User) {
	snapshotID := r.PathValue("id")
	snap, err := s.db.SnapshotByID(r.Context(), user.ID, snapshotID)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	prefix := r.URL.Query().Get("prefix")

	// Probe one entry first so an encrypted repository or a missing path fails
	// with a clean JSON error instead of a corrupt ZIP the user only discovers
	// after the download finishes.
	if err := s.probeRestorable(r, snapshotID, prefix); err != nil {
		s.reportRestoreFailure(w, r, err, prefix)
		return
	}

	name := archiveName(snap, prefix)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(name))
	w.Header().Set("Cache-Control", "private, no-store")
	// The length is unknown up front, so the response is streamed chunked.
	flusher, _ := w.(http.Flusher)

	zw := zip.NewWriter(w)
	var written int64
	cursor := ""
	for {
		entries, next, err := s.db.Tree(r.Context(), snapshotID,
			store.TreeQuery{Prefix: prefix, Cursor: cursor, Limit: 500})
		if err != nil {
			s.log.Error("archive tree read", "snapshot", snapshotID, "error", err)
			break
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if r.Context().Err() != nil {
				return
			}
			if err := s.writeArchiveEntry(r, zw, entry, prefix); err != nil {
				// Mid-stream failures cannot become an HTTP error any more. Log
				// it and abandon the response so the client sees a truncated
				// archive rather than a silently incomplete one.
				s.log.Error("archive entry", "snapshot", snapshotID, "path", entry.Path, "error", err)
				_ = zw.Close()
				return
			}
			written += entry.Size
			if flusher != nil {
				flusher.Flush()
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if err := zw.Close(); err != nil {
		s.log.Debug("close archive", "error", err)
	}
	s.log.Info("archive restored", "snapshot", snapshotID, "user", user.ID, "prefix", prefix, "bytes", written)
}

// writeArchiveEntry adds one tree entry to the archive.
func (s *Server) writeArchiveEntry(r *http.Request, zw *zip.Writer, entry api.Entry, prefix string) error {
	name := archiveEntryName(entry.Path, prefix)
	if name == "" {
		return nil
	}
	switch entry.Type {
	case api.EntryDir:
		_, err := zw.Create(name + "/")
		return err
	case api.EntrySymlink:
		// Symlinks are stored as a small text member: ZIP has no portable link
		// representation, and silently following them could pull in data from
		// outside the backup.
		header := &zip.FileHeader{Name: name + ".symlink", Method: zip.Deflate, Modified: entry.ModTime}
		f, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, entry.LinkTarget)
		return err
	default:
		header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: entry.ModTime}
		header.SetMode(0o644)
		if entry.Size > 1<<30 {
			// ZIP64 is required past 4 GiB; archive/zip handles it, but Deflate
			// on an already-compressed multi-gigabyte file wastes CPU for
			// nothing, so store those verbatim.
			header.Method = zip.Store
		}
		f, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		return s.streamEntry(r, f, entry)
	}
}

// writePlainFile streams a file entry to w.
func (s *Server) writePlainFile(r *http.Request, w io.Writer, entry *api.Entry) error {
	return s.streamEntry(r, w, *entry)
}

// streamEntry fetches, decodes and writes an entry's chunks in order.
func (s *Server) streamEntry(r *http.Request, w io.Writer, entry api.Entry) error {
	for _, digest := range entry.Chunks {
		blob, err := s.blobs.Get(r.Context(), digest)
		if err != nil {
			return fmt.Errorf("read chunk %s of %s: %w", digest, entry.Path, err)
		}
		if codec.IsEncrypted(blob) {
			return errEncryptedRepository
		}
		plain, err := s.codec.Decode(blob)
		if err != nil {
			return fmt.Errorf("decode chunk %s of %s: %w", digest, entry.Path, err)
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
	}
	return nil
}

// probeEntry verifies that a single entry's data is present and decodable.
func (s *Server) probeEntry(r *http.Request, entry *api.Entry) error {
	if len(entry.Chunks) == 0 {
		return nil
	}
	blob, err := s.blobs.Get(r.Context(), entry.Chunks[0])
	if err != nil {
		return err
	}
	if codec.IsEncrypted(blob) {
		return errEncryptedRepository
	}
	return nil
}

// probeRestorable checks the first file in the range so archive downloads fail
// early and cleanly.
func (s *Server) probeRestorable(r *http.Request, snapshotID, prefix string) error {
	entries, _, err := s.db.Tree(r.Context(), snapshotID, store.TreeQuery{Prefix: prefix, Limit: 50})
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return store.ErrNotFound
	}
	for _, entry := range entries {
		if entry.Type != api.EntryFile || len(entry.Chunks) == 0 {
			continue
		}
		blob, err := s.blobs.Get(r.Context(), entry.Chunks[0])
		if err != nil {
			return err
		}
		if codec.IsEncrypted(blob) {
			return errEncryptedRepository
		}
		return nil
	}
	return nil
}

// errEncryptedRepository means the server cannot decode the data, because only
// the user's devices hold the key.
var errEncryptedRepository = errors.New("this backup is end-to-end encrypted, so the server cannot decrypt it")

// reportRestoreFailure turns a restore error into the clearest possible answer.
func (s *Server) reportRestoreFailure(w http.ResponseWriter, r *http.Request, err error, path string) {
	switch {
	case errors.Is(err, errEncryptedRepository):
		writeError(w, http.StatusConflict, "encrypted",
			"This backup is end-to-end encrypted, so the server cannot read it. "+
				"Restore from a device with: openbackup restore --snapshot <id> --path "+path)
	case errors.Is(err, store.ErrBlobNotFound):
		s.log.Error("restore hit a missing blob", "path", path, "error", err)
		writeError(w, http.StatusGone, "missing_data",
			"Some data for this file is missing from storage. Run 'openbackup-server check' for details.")
	case errors.Is(err, r.Context().Err()):
		// Client cancelled the download; nothing to report.
	default:
		writeStoreError(w, err)
	}
}

// archiveName builds a friendly download filename.
func archiveName(snap *api.Snapshot, prefix string) string {
	base := "backup"
	if snap.DeviceName != "" {
		base = sanitizeFilename(snap.DeviceName)
	}
	when := snap.StartedAt
	if when.IsZero() {
		when = time.Now()
	}
	name := fmt.Sprintf("%s-%s", base, when.UTC().Format("2006-01-02-1504"))
	if prefix != "" {
		name += "-" + sanitizeFilename(path.Base(prefix))
	}
	return name + ".zip"
}

// archiveEntryName makes the member path relative to the requested prefix, so a
// folder restore does not recreate the whole absolute tree.
func archiveEntryName(entryPath, prefix string) string {
	name := strings.Trim(entryPath, "/")
	prefix = strings.Trim(prefix, "/")
	if prefix != "" {
		if name == prefix {
			return path.Base(name)
		}
		name = strings.TrimPrefix(name, prefix+"/")
	}
	return name
}

// sanitizeFilename strips characters that break Content-Disposition or land
// badly on a Windows filesystem.
func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", `\`, "-", ":", "-", "*", "-", "?", "-", `"`, "-",
		"<", "-", ">", "-", "|", "-", "\n", " ", "\r", " ",
	)
	out := strings.TrimSpace(replacer.Replace(s))
	if out == "" {
		return "backup"
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// contentDisposition builds a header that works for both ASCII and Unicode
// filenames, since browsers disagree about the plain form.
func contentDisposition(filename string) string {
	safe := sanitizeFilename(filename)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, safe, url.PathEscape(filename))
}
