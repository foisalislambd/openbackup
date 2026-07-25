package store

import (
	"context"
	"errors"
	"io"
)

// ErrBlobNotFound is returned when a digest is not stored.
var ErrBlobNotFound = errors.New("store: blob not found")

// Blobs is the content-addressed object store behind the server.
//
// Two implementations ship: the local filesystem, which is the default because
// a self-hosted VPS already has a disk, and S3-compatible object storage
// (AWS S3, MinIO, Backblaze B2, Hetzner, Wasabi) for when backups outgrow it.
// Everything above this interface is storage agnostic, so switching is a config
// change plus a copy of the blobs directory.
//
// Every method must be safe for concurrent use, and Put must be idempotent:
// content addressing means re-uploading a digest can never change its bytes.
type Blobs interface {
	// Has reports whether a blob exists and its stored size.
	Has(ctx context.Context, digest string) (int64, bool)
	// Put stores a blob, returning the bytes newly written (0 when it already
	// existed).
	Put(ctx context.Context, digest string, blob []byte) (int64, error)
	// Get reads a whole blob.
	Get(ctx context.Context, digest string) ([]byte, error)
	// Open streams a blob.
	Open(ctx context.Context, digest string) (io.ReadCloser, int64, error)
	// Delete removes a blob; a missing blob is not an error.
	Delete(ctx context.Context, digest string) error
	// Walk visits every stored digest, used by the integrity checker.
	Walk(ctx context.Context, fn func(digest string, size int64) error) error
	// Location is a human readable description for logs and the dashboard.
	Location() string
	// FreeBytes reports remaining capacity, or -1 when unknown (object stores
	// generally cannot answer this).
	FreeBytes() int64
}
