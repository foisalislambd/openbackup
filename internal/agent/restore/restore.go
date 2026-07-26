// Package restore rebuilds files from a snapshot onto the local machine.
//
// Restoring runs on the device rather than the server because that is where the
// encryption key lives: with end-to-end encryption on, the server physically
// cannot produce the plaintext. It also means a restore can verify each file
// against its recorded digest before it replaces anything on disk.
package restore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/hash"
)

// Conflict decides what happens when a file already exists.
type Conflict string

// Conflict policies.
const (
	// ConflictSkip leaves existing files untouched. This is the default because
	// the common restore is "get back what I lost", not "overwrite my work".
	ConflictSkip Conflict = "skip"
	// ConflictOverwrite replaces existing files.
	ConflictOverwrite Conflict = "overwrite"
	// ConflictRename keeps both, writing the restored copy alongside.
	ConflictRename Conflict = "rename"
)

// Options configures a restore.
type Options struct {
	Client *api.Client
	Codec  *codec.Codec
	Logger *slog.Logger

	// SnapshotID is the snapshot to restore from.
	SnapshotID string
	// Prefix limits the restore to one folder or file inside the snapshot.
	Prefix string
	// Target is the directory to restore into.
	Target string
	// Conflict decides what to do about existing files.
	Conflict Conflict
	// DryRun reports what would happen without writing anything.
	DryRun bool
	// Progress is called as files are restored.
	Progress func(Progress)
}

// Progress reports restore movement.
type Progress struct {
	Path         string
	BytesWritten int64
	FilesDone    int64
	FilesTotal   int64
}

// Result summarises a restore.
type Result struct {
	FilesRestored int64
	FilesSkipped  int64
	BytesWritten  int64
	// Failed lists files that could not be restored, with the reason. A restore
	// reports partial success rather than pretending it all worked.
	Failed   []string
	Duration time.Duration
}

// Run restores files from a snapshot.
func Run(ctx context.Context, opts Options) (*Result, error) {
	if opts.Client == nil || opts.Codec == nil {
		return nil, errors.New("restore: client and codec are required")
	}
	if opts.SnapshotID == "" {
		return nil, errors.New("restore: a snapshot id is required")
	}
	if opts.Target == "" {
		return nil, errors.New("restore: a target directory is required")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	if opts.Conflict == "" {
		opts.Conflict = ConflictSkip
	}

	target, err := filepath.Abs(opts.Target)
	if err != nil {
		return nil, err
	}
	if !opts.DryRun {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return nil, err
		}
	}

	start := time.Now()
	result := &Result{}
	var filesDone atomic.Int64
	cursor := ""

	for {
		entries, next, err := opts.Client.SnapshotEntries(ctx, opts.SnapshotID,
			api.EntryQuery{Prefix: opts.Prefix, Cursor: cursor, Limit: 500})
		if err != nil {
			return result, err
		}
		if len(entries) == 0 {
			break
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			dest, err := safeJoin(target, relativeName(entry.Path, opts.Prefix))
			if err != nil {
				// A path that escapes the target is either a bug or an attack; in
				// both cases it must not be written.
				log.Error("refusing to restore an unsafe path", "path", entry.Path, "error", err)
				result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", entry.Path, err))
				continue
			}

			switch entry.Type {
			case api.EntryDir:
				if !opts.DryRun {
					if err := os.MkdirAll(dest, 0o755); err != nil {
						result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", entry.Path, err))
					}
				}
				continue

			case api.EntrySymlink:
				if opts.DryRun {
					continue
				}
				if err := restoreSymlink(dest, entry, opts.Conflict); err != nil {
					result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", entry.Path, err))
				}
				continue
			}

			written, skipped, err := restoreFile(ctx, opts, entry, dest)
			switch {
			case err != nil:
				log.Warn("could not restore a file", "path", entry.Path, "error", err)
				result.Failed = append(result.Failed, fmt.Sprintf("%s: %v", entry.Path, err))
			case skipped:
				result.FilesSkipped++
			default:
				result.FilesRestored++
				result.BytesWritten += written
			}
			if opts.Progress != nil {
				opts.Progress(Progress{
					Path: entry.Path, BytesWritten: result.BytesWritten, FilesDone: filesDone.Add(1),
				})
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	result.Duration = time.Since(start)
	return result, nil
}

// restoreFile writes one file, verifying it before it is put in place.
func restoreFile(ctx context.Context, opts Options, entry api.Entry, dest string) (written int64, skipped bool, err error) {
	if existing, statErr := os.Stat(dest); statErr == nil {
		switch opts.Conflict {
		case ConflictSkip:
			return 0, true, nil
		case ConflictRename:
			dest = uniqueName(dest)
		case ConflictOverwrite:
			if existing.IsDir() {
				return 0, false, fmt.Errorf("a directory already exists at %s", dest)
			}
		}
	}
	if opts.DryRun {
		return entry.Size, false, nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, false, err
	}

	// Write to a temporary file and rename: an interrupted restore must never
	// leave a half-written file where the original used to be.
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".openbackup-restore-*")
	if err != nil {
		return 0, false, err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	verifier := hash.NewHasher()
	for _, digest := range entry.Chunks {
		blob, err := opts.Client.GetChunk(ctx, digest)
		if err != nil {
			return 0, false, fmt.Errorf("fetch chunk %s: %w", digest[:12], err)
		}
		plain, err := opts.Codec.Decode(blob)
		if err != nil {
			if codec.IsEncrypted(blob) {
				return 0, false, errors.New("this data is encrypted with a different key; " +
					"restore from a device that holds the right key, or supply the recovery code")
			}
			return 0, false, fmt.Errorf("decode chunk %s: %w", digest[:12], err)
		}
		// Each chunk is checked against its own content address, so corruption
		// anywhere between the server's disk and this file is caught.
		if hash.Sum(plain) != digest {
			return 0, false, fmt.Errorf("chunk %s failed its integrity check", digest[:12])
		}
		if _, err := tmp.Write(plain); err != nil {
			return 0, false, err
		}
		if _, err := verifier.Write(plain); err != nil {
			return 0, false, err
		}
		written += int64(len(plain))
	}

	// The whole-file digest catches a truncated or reordered chunk list, which no
	// per-chunk check can see.
	if entry.Digest != "" && verifier.Hex() != entry.Digest {
		return 0, false, errors.New("the restored file does not match its recorded digest")
	}
	if err = tmp.Sync(); err != nil {
		return 0, false, err
	}
	if err = tmp.Close(); err != nil {
		return 0, false, err
	}
	if err = os.Rename(tmpName, dest); err != nil {
		return 0, false, err
	}

	// Restore the original timestamps so backup tools, sync clients and the
	// user's own sense of "when did I write this" stay correct.
	if !entry.ModTime.IsZero() {
		_ = os.Chtimes(dest, time.Now(), entry.ModTime)
	}
	if entry.Mode != 0 {
		_ = os.Chmod(dest, os.FileMode(entry.Mode).Perm())
	}
	return written, false, nil
}

// restoreSymlink recreates a symlink, or records it as a text file when the
// platform refuses (Windows needs privileges for links).
func restoreSymlink(dest string, entry api.Entry, conflict Conflict) error {
	if _, err := os.Lstat(dest); err == nil {
		if conflict != ConflictOverwrite {
			return nil
		}
		if err := os.Remove(dest); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(entry.LinkTarget, dest); err != nil {
		// Falling back to a plain file keeps the information rather than losing
		// the entry entirely.
		return os.WriteFile(dest+".symlink", []byte(entry.LinkTarget+"\n"), 0o644)
	}
	return nil
}

// relativeName strips the requested prefix so restoring a folder does not
// recreate the entire original tree under the target.
func relativeName(entryPath, prefix string) string {
	name := strings.Trim(entryPath, "/")
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return name
	}
	if name == prefix {
		return path.Base(name)
	}
	return strings.TrimPrefix(name, prefix+"/")
}

// safeJoin resolves name under root, refusing anything that would escape it.
//
// Snapshot paths come from another machine, so they are untrusted input: without
// this check a crafted entry such as "../../.ssh/authorized_keys" would let a
// restore write outside the target directory.
func safeJoin(root, name string) (string, error) {
	if name == "" {
		return "", errors.New("empty path")
	}
	clean := path.Clean("/" + strings.ReplaceAll(name, `\`, "/"))
	joined := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))

	rel, err := filepath.Rel(root, joined)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q would escape the restore folder", name)
	}
	// A reserved Windows device name would make the write go somewhere very
	// surprising.
	if isReservedName(filepath.Base(joined)) {
		return "", fmt.Errorf("path %q uses a reserved name", name)
	}
	return joined, nil
}

// reservedNames are Windows device names that cannot be used as filenames.
var reservedNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

func isReservedName(name string) bool {
	base := strings.ToLower(name)
	if idx := strings.Index(base, "."); idx > 0 {
		base = base[:idx]
	}
	return reservedNames[base]
}

// uniqueName finds a free filename next to an existing one.
func uniqueName(dest string) string {
	ext := filepath.Ext(dest)
	base := strings.TrimSuffix(dest, ext)
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (restored %d)%s", base, i, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return dest
}

// FindSnapshot resolves a user-supplied snapshot reference.
//
// "latest" is accepted because that is what people actually want, and a prefix of
// an id is accepted because nobody wants to type a full identifier.
func FindSnapshot(ctx context.Context, client *api.Client, ref string) (*api.Snapshot, error) {
	snapshots, err := client.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	complete := make([]api.Snapshot, 0, len(snapshots))
	for _, s := range snapshots {
		if s.Status == api.SnapshotStatusComplete {
			complete = append(complete, s)
		}
	}
	if len(complete) == 0 {
		return nil, errors.New("there are no completed backups to restore from yet")
	}
	if ref == "" || ref == "latest" {
		newest := complete[0]
		for _, s := range complete {
			if s.StartedAt.After(newest.StartedAt) {
				newest = s
			}
		}
		return &newest, nil
	}
	var matches []api.Snapshot
	for _, s := range complete {
		if s.ID == ref {
			return &s, nil
		}
		if strings.HasPrefix(s.ID, ref) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no backup matches %q", ref)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("%q matches %d backups; use a longer id", ref, len(matches))
	}
}

// Search finds entries matching a name fragment across a snapshot, which is how
// a user restores "that spreadsheet" without remembering where it lived.
func Search(ctx context.Context, client *api.Client, snapshotID, query string, limit int) ([]api.Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	needle := strings.ToLower(query)
	var out []api.Entry
	cursor := ""
	for {
		entries, next, err := client.SnapshotEntries(ctx, snapshotID,
			api.EntryQuery{Cursor: cursor, Limit: 500})
		if err != nil {
			return nil, err
		}
		if len(entries) == 0 {
			return out, nil
		}
		for _, entry := range entries {
			if entry.Type != api.EntryFile {
				continue
			}
			if strings.Contains(strings.ToLower(entry.Path), needle) {
				out = append(out, entry)
				if len(out) >= limit {
					return out, nil
				}
			}
		}
		if next == "" {
			return out, nil
		}
		cursor = next
	}
}

// CopyTo streams a single restored file to a writer, used by "openbackup cat".
func CopyTo(ctx context.Context, client *api.Client, c *codec.Codec, entry api.Entry, w io.Writer) error {
	for _, digest := range entry.Chunks {
		blob, err := client.GetChunk(ctx, digest)
		if err != nil {
			return err
		}
		plain, err := c.Decode(blob)
		if err != nil {
			return err
		}
		if _, err := w.Write(plain); err != nil {
			return err
		}
	}
	return nil
}
