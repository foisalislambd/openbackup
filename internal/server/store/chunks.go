package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/hash"
)

// sqliteMaxParams keeps generated IN clauses inside SQLite's variable limit.
const sqliteMaxParams = 500

// ChunkRegistration is one chunk row written by an upload.
type ChunkRegistration struct {
	Digest      string
	StoredBytes int64
	PlainBytes  int64
	Encrypted   bool
}

// RegisterChunks records many chunks in one transaction. A backup of thousands
// of small files would otherwise prepare and commit a statement per chunk.
func (db *DB) RegisterChunks(ctx context.Context, userID string, rows []ChunkRegistration) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	chunkStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO chunks (digest, stored_bytes, plain_bytes, encrypted, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(digest) DO NOTHING`)
	if err != nil {
		return err
	}
	defer chunkStmt.Close()
	acctStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO account_chunks (user_id, digest) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer acctStmt.Close()
	now := nowMillis()
	for _, row := range rows {
		if err := hash.Validate(row.Digest); err != nil {
			return err
		}
		if _, err := chunkStmt.ExecContext(ctx, row.Digest, row.StoredBytes, row.PlainBytes, boolToInt(row.Encrypted), now); err != nil {
			return err
		}
		if _, err := acctStmt.ExecContext(ctx, userID, row.Digest); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RegisterChunk records a stored blob for an account. It is idempotent,
// matching the blob store, so a retried upload changes nothing.
func (db *DB) RegisterChunk(ctx context.Context, userID, digest string, storedBytes, plainBytes int64, encrypted bool) error {
	if err := hash.Validate(digest); err != nil {
		return err
	}
	if _, err := db.sql.ExecContext(ctx,
		`INSERT INTO chunks (digest, stored_bytes, plain_bytes, encrypted, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(digest) DO NOTHING`,
		digest, storedBytes, plainBytes, boolToInt(encrypted), nowMillis()); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO account_chunks (user_id, digest) VALUES (?, ?) ON CONFLICT DO NOTHING`,
		userID, digest)
	return err
}

// MissingChunks returns the digests the account does not have yet, preserving
// the caller's order. This is the hot path of every backup: one round trip
// tells the agent exactly what it must upload.
//
// "Have" knowledge is scoped to the calling account (chunks it uploaded or that
// its snapshots reference). A global answer would let any enrolled device probe
// whether any other tenant stores a given digest — an existence oracle. The
// blob store itself still deduplicates across accounts at Put time, so nothing
// is stored twice; only redundant uploads of already-present data are possible.
func (db *DB) MissingChunks(ctx context.Context, userID string, digests []string) ([]string, error) {
	if len(digests) == 0 {
		return nil, nil
	}
	present := make(map[string]struct{}, len(digests))
	for chunkStart := 0; chunkStart < len(digests); chunkStart += sqliteMaxParams {
		end := min(chunkStart+sqliteMaxParams, len(digests))
		batch := digests[chunkStart:end]

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, 0, 2*len(batch)+2)
		args = append(args, userID)
		for _, d := range batch {
			args = append(args, d)
		}
		args = append(args, userID)
		for _, d := range batch {
			args = append(args, d)
		}
		rows, err := db.sql.QueryContext(ctx,
			`SELECT digest FROM (
			     SELECT ac.digest AS digest FROM account_chunks ac
			     WHERE ac.user_id = ? AND ac.digest IN (`+placeholders+`)
			     UNION
			     SELECT sc.digest FROM snapshot_chunks sc
			     JOIN snapshots s ON s.id = sc.snapshot_id
			     WHERE s.user_id = ? AND sc.digest IN (`+placeholders+`)
			 ) GROUP BY digest`, args...)
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

// AccountChunkKnown reports whether the account has the chunk: either it
// uploaded the blob itself, or one of its snapshots references it. Chunk reads
// are gated on this so one account cannot fetch another account's data by
// guessing a digest.
func (db *DB) AccountChunkKnown(ctx context.Context, userID, digest string) (bool, error) {
	var known int
	err := db.sql.QueryRowContext(ctx,
		`SELECT CASE WHEN EXISTS (
		     SELECT 1 FROM account_chunks WHERE user_id = ? AND digest = ?
		 ) OR EXISTS (
		     SELECT 1 FROM snapshot_chunks sc
		     JOIN snapshots s ON s.id = sc.snapshot_id
		     WHERE s.user_id = ? AND sc.digest = ?
		 ) THEN 1 ELSE 0 END`,
		userID, digest, userID, digest).Scan(&known)
	if err != nil {
		return false, err
	}
	return known != 0, nil
}

// ChunkInfo describes a stored chunk.
type ChunkInfo struct {
	Digest      string
	StoredBytes int64
	PlainBytes  int64
	Encrypted   bool
}

// gcGrace keeps a freshly stored chunk even when no snapshot references it
// yet. Agents upload chunks before they publish the file list.
const gcGrace = 15 * time.Minute

// UnreferencedChunks lists chunks that no snapshot references, that are older
// than the grace period, and that are not covered by a backup still running.
// The list can go stale; ClaimUnreferencedChunk must confirm each digest
// before its blob is deleted.
func (db *DB) UnreferencedChunks(ctx context.Context, limit int) ([]ChunkInfo, error) {
	if limit <= 0 {
		limit = 1000
	}
	cutoff := nowMillis() - gcGrace.Milliseconds()
	rows, err := db.sql.QueryContext(ctx, `
		SELECT c.digest, c.stored_bytes, c.plain_bytes FROM chunks c
		WHERE NOT EXISTS (SELECT 1 FROM snapshot_chunks sc WHERE sc.digest = c.digest)
		  AND NOT EXISTS (
		    SELECT 1 FROM snapshots s
		    WHERE s.status = 'running' AND c.created_at >= s.started_at - ?
		  )
		  AND c.created_at < ?
		LIMIT ?`, gcGrace.Milliseconds(), cutoff, limit)
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
	if _, err := db.sql.ExecContext(ctx, `DELETE FROM chunks WHERE digest = ?`, digest); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx, `DELETE FROM account_chunks WHERE digest = ?`, digest)
	return err
}

// DeleteChunkRows removes metadata for many digests in one transaction.
func (db *DB) DeleteChunkRows(ctx context.Context, digests []string) error {
	if len(digests) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	chunkStmt, err := tx.PrepareContext(ctx, `DELETE FROM chunks WHERE digest = ?`)
	if err != nil {
		return err
	}
	defer chunkStmt.Close()
	accountStmt, err := tx.PrepareContext(ctx, `DELETE FROM account_chunks WHERE digest = ?`)
	if err != nil {
		return err
	}
	defer accountStmt.Close()
	for _, digest := range digests {
		if _, err := chunkStmt.ExecContext(ctx, digest); err != nil {
			return err
		}
		if _, err := accountStmt.ExecContext(ctx, digest); err != nil {
			return err
		}
	}
	return tx.Commit()
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

// ClaimUnreferencedChunk deletes the metadata row only when digest is still
// unreferenced under the same rules as UnreferencedChunks, then runs deleteBlob
// before the transaction commits. AddEntries takes the same write lock, so it
// cannot publish a reference between the check and the blob delete. deleteBlob
// failure rolls the row back; the next pass tries again.
func (db *DB) ClaimUnreferencedChunk(ctx context.Context, digest string, deleteBlob func() error) (bool, error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	cutoff := nowMillis() - gcGrace.Milliseconds()
	res, err := tx.ExecContext(ctx, `
		DELETE FROM chunks WHERE digest = ?
		  AND NOT EXISTS (SELECT 1 FROM snapshot_chunks sc WHERE sc.digest = chunks.digest)
		  AND NOT EXISTS (
		    SELECT 1 FROM snapshots s
		    WHERE s.status = 'running' AND chunks.created_at >= s.started_at - ?
		  )
		  AND created_at < ?`, digest, gcGrace.Milliseconds(), cutoff)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_chunks WHERE digest = ?`, digest); err != nil {
		return false, err
	}
	if deleteBlob != nil {
		if err := deleteBlob(); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
