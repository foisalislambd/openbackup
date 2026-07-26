// Package store holds the server's persistence: a content-addressed blob store
// and a SQLite metadata database.
//
// Blobs live outside the database because that is what makes a self-hosted
// deployment easy to reason about: the data is plain files you can rsync to
// another VPS, and SQLite stays small enough to stay fast.
package store

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/foisalislambd/openbackup/internal/hash"
)

// FSBlobs stores blobs on the local filesystem.
type FSBlobs struct {
	root string
	// mkdirMu serialises shard directory creation; the writes themselves run
	// concurrently.
	mkdirMu sync.Mutex
}

// compile-time check that the filesystem backend satisfies the interface.
var _ Blobs = (*FSBlobs)(nil)

// NewFSBlobs prepares the on-disk layout under root.
func NewFSBlobs(root string) (*FSBlobs, error) {
	if root == "" {
		return nil, errors.New("store: blob root must be set")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{abs, filepath.Join(abs, "blobs"), filepath.Join(abs, "tmp")} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("store: create %s: %w", dir, err)
		}
	}
	b := &FSBlobs{root: abs}
	// A crash mid-upload leaves temp files behind; clean them at startup so a
	// long-running server does not slowly fill the disk with orphans.
	b.cleanTemp()
	return b, nil
}

// Location returns the base directory.
func (b *FSBlobs) Location() string { return b.root }

// FreeBytes reports free space on the volume holding the blobs.
func (b *FSBlobs) FreeBytes() int64 { return freeDiskBytes(b.root) }

// path maps a digest to its file, sharded two levels deep. Two levels of 256
// keeps directories under a few thousand entries for repositories into the
// hundreds of millions of chunks, which every filesystem handles well.
func (b *FSBlobs) path(digest string) string {
	return filepath.Join(b.root, "blobs", digest[0:2], digest[2:4], digest)
}

// Has reports whether a blob is present and returns its stored size.
func (b *FSBlobs) Has(_ context.Context, digest string) (int64, bool) {
	if err := hash.Validate(digest); err != nil {
		return 0, false
	}
	st, err := os.Stat(b.path(digest))
	if err != nil {
		return 0, false
	}
	return st.Size(), true
}

// Put stores a blob under digest. It is idempotent: re-uploading existing
// content is a no-op, which is what lets agents retry freely.
func (b *FSBlobs) Put(_ context.Context, digest string, blob []byte) (written int64, err error) {
	if err := hash.Validate(digest); err != nil {
		return 0, err
	}
	final := b.path(digest)
	if _, err := os.Stat(final); err == nil {
		// Already stored, and content addressing guarantees the bytes match.
		return 0, nil
	}

	tmp, err := os.CreateTemp(filepath.Join(b.root, "tmp"), "blob-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(blob); err != nil {
		return 0, err
	}
	// fsync before rename: without it a power cut can leave a correctly named
	// file with zero bytes, and a backup that silently restores empty files is
	// worse than no backup at all.
	if err = tmp.Sync(); err != nil {
		return 0, err
	}
	if err = tmp.Close(); err != nil {
		return 0, err
	}

	b.mkdirMu.Lock()
	err = os.MkdirAll(filepath.Dir(final), 0o750)
	b.mkdirMu.Unlock()
	if err != nil {
		return 0, err
	}
	if err = os.Rename(tmpName, final); err != nil {
		// A concurrent upload of the same digest may have won the race, which is
		// fine: the content is identical by definition.
		if _, statErr := os.Stat(final); statErr == nil {
			_ = os.Remove(tmpName)
			return 0, nil
		}
		return 0, err
	}
	return int64(len(blob)), nil
}

// Get reads a blob.
func (b *FSBlobs) Get(_ context.Context, digest string) ([]byte, error) {
	if err := hash.Validate(digest); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(b.path(digest))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrBlobNotFound
	}
	return data, err
}

// Open streams a blob.
func (b *FSBlobs) Open(_ context.Context, digest string) (io.ReadCloser, int64, error) {
	if err := hash.Validate(digest); err != nil {
		return nil, 0, err
	}
	f, err := os.Open(b.path(digest))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, ErrBlobNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, st.Size(), nil
}

// Delete removes a blob. Missing blobs are not an error, so garbage collection
// is idempotent.
func (b *FSBlobs) Delete(_ context.Context, digest string) error {
	if err := hash.Validate(digest); err != nil {
		return err
	}
	err := os.Remove(b.path(digest))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Walk visits every stored digest. Garbage collection uses it to find blobs the
// database no longer references, for example after a crash between the blob
// write and the metadata commit.
func (b *FSBlobs) Walk(ctx context.Context, fn func(digest string, size int64) error) error {
	blobs := filepath.Join(b.root, "blobs")
	return filepath.WalkDir(blobs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if hash.Validate(name) != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		return fn(name, info.Size())
	})
}

// cleanTemp removes leftover partial uploads.
func (b *FSBlobs) cleanTemp() {
	tmp := filepath.Join(b.root, "tmp")
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "blob-") {
			_ = os.Remove(filepath.Join(tmp, e.Name()))
		}
	}
}

// VerifyDigest recomputes a blob's content address. It applies to unencrypted
// blobs only; encrypted blobs are verified by their AEAD tag on the client,
// since the server has no key.
func VerifyDigest(digest string, plain []byte) bool {
	return subtle.ConstantTimeCompare([]byte(digest), []byte(hash.Sum(plain))) == 1
}
