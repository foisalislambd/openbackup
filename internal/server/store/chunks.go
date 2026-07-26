package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/foisalislambd/openbackup/internal/hash"
)

// sqliteMaxParams keeps generated IN clauses inside SQLite's variable limit.
const sqliteMaxParams = 500

// RegisterChunk records a stored blob. It is idempotent, matching the blob
// store, so a retried upload changes nothing.
func (db *DB) RegisterChunk(ctx context.Context, digest string, storedBytes, plainBytes int64, encrypted bool) error {
	if err := hash.Validate(digest); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO chunks (digest, stored_bytes, plain_bytes, encrypted, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(digest) DO NOTHING`,
		digest, storedBytes, plainBytes, boolToInt(encrypted), nowMillis())
	return err
}

// MissingChunks returns the digests that are not stored yet, preserving the
// caller's order. This is the hot path of every backup: one round trip tells the
// agent exactly what it must upload, and everything already known from any
// device is skipped.
func (db *DB) MissingChunks(ctx context.Context, digests []string) ([]string, error) {
	if len(digests) == 0 {
		return nil, nil
	}
	present := make(map[string]struct{}, len(digests))
	for chunkStart := 0; chunkStart < len(digests); chunkStart += sqliteMaxParams {
		end := min(chunkStart+sqliteMaxParams, len(digests))
		batch := digests[chunkStart:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, len(batch))
		for _, d := range batch {
			args = append(args, d)
		}
		rows, err := db.sql.QueryContext(ctx,
			`SELECT digest FROM chunks WHERE digest IN (`+placeholders+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				rows.Close()
				return nil, err
			}
			present[d] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	missing := make([]string, 0, len(digests)-len(present))
	seen := make(map[string]struct{}, len(digests))
	for _, d := range digests {
		if _, ok := present[d]; ok {
			continue
		}
		if _, dup := seen[d]; dup {
			continue
		}
		seen[d] = struct{}{}
		missing = append(missing, d)
	}
	return missing, nil
}

// ChunkInfo describes a stored chunk.
type ChunkInfo struct {
	Digest      string
	StoredBytes int64
	PlainBytes  int64
	Encrypted   bool
}

// Chunk reads a chunk's metadata.
func (db *DB) Chunk(ctx context.Context, digest string) (*ChunkInfo, error) {
	var (
		info      ChunkInfo
		encrypted int
	)
	info.Digest = digest
	err := db.sql.QueryRowContext(ctx,
		`SELECT stored_bytes, plain_bytes, encrypted FROM chunks WHERE digest = ?`, digest).
		Scan(&info.StoredBytes, &info.PlainBytes, &encrypted)
	if err != nil {
		return nil, err
	}
	info.Encrypted = encrypted != 0
	return &info, nil
}

// UnreferencedChunks lists stored chunks that no snapshot references any more.
// Garbage collection deletes the blob first and the row second, so a crash in
// between leaves a row whose blob is gone; the next pass notices and cleans up.
func (db *DB) UnreferencedChunks(ctx context.Context, limit int) ([]ChunkInfo, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT c.digest, c.stored_bytes, c.plain_bytes FROM chunks c
		WHERE NOT EXISTS (SELECT 1 FROM snapshot_chunks sc WHERE sc.digest = c.digest)
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChunkInfo
	for rows.Next() {
		var info ChunkInfo
		if err := rows.Scan(&info.Digest, &info.StoredBytes, &info.PlainBytes); err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

// DeleteChunkRow removes chunk metadata after its blob has been deleted.
func (db *DB) DeleteChunkRow(ctx context.Context, digest string) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM chunks WHERE digest = ?`, digest)
	return err
}

// IsChunkKnown reports whether a digest has a metadata row. Used by the
// integrity checker to spot blobs on disk that the database forgot.
func (db *DB) IsChunkKnown(ctx context.Context, digest string) (bool, error) {
	var one int
	err := db.sql.QueryRowContext(ctx, `SELECT 1 FROM chunks WHERE digest = ?`, digest).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
