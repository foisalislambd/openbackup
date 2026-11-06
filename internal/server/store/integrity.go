package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ChunkSample is one stored blob the integrity checker should read.
type ChunkSample struct {
	Digest      string
	StoredBytes int64
	Encrypted   bool
}

// IntegrityRun is the summary of one verification pass.
type IntegrityRun struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time
	Checked    int
	Failed     int
	Detail     string
}

// SampleChunks returns blobs that have not been checked, then the ones checked
// longest ago, so a small hourly sample eventually covers the repository.
func (db *DB) SampleChunks(ctx context.Context, limit int) ([]ChunkSample, error) {
	if limit <= 0 {
		limit = 24
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT c.digest, c.stored_bytes, c.encrypted
		FROM chunks c
		LEFT JOIN integrity_state s ON s.digest = c.digest
		ORDER BY s.checked_at IS NULL DESC, s.checked_at ASC
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChunkSample, 0, limit)
	for rows.Next() {
		var (
			c   ChunkSample
			enc int
		)
		if err := rows.Scan(&c.Digest, &c.StoredBytes, &enc); err != nil {
			return nil, err
		}
		c.Encrypted = enc != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// RecordChunkCheck remembers the outcome of reading one blob.
func (db *DB) RecordChunkCheck(ctx context.Context, digest string, ok bool, detail string) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO integrity_state (digest, checked_at, ok, detail) VALUES (?, ?, ?, ?)
		 ON CONFLICT(digest) DO UPDATE SET checked_at = excluded.checked_at, ok = excluded.ok, detail = excluded.detail`,
		digest, nowMillis(), boolToInt(ok), truncate(detail, 240))
	return err
}

// SaveIntegrityRun stores the summary shown in Settings.
func (db *DB) SaveIntegrityRun(ctx context.Context, run IntegrityRun) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO integrity_runs (started_at, finished_at, checked, failed, detail) VALUES (?, ?, ?, ?, ?)`,
		toMillis(run.StartedAt), toMillis(run.FinishedAt), run.Checked, run.Failed, truncate(run.Detail, 1000))
	return err
}

// LatestIntegrityRun returns the most recent summary, or a zero run when none
// has been recorded yet.
func (db *DB) LatestIntegrityRun(ctx context.Context) (IntegrityRun, error) {
	var (
		run      IntegrityRun
		started  int64
		finished int64
	)
	err := db.sql.QueryRowContext(ctx,
		`SELECT id, started_at, finished_at, checked, failed, detail FROM integrity_runs
		 ORDER BY id DESC LIMIT 1`).Scan(&run.ID, &started, &finished, &run.Checked, &run.Failed, &run.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return IntegrityRun{}, nil
	}
	if err != nil {
		return IntegrityRun{}, err
	}
	run.StartedAt = fromMillis(started)
	run.FinishedAt = fromMillis(finished)
	return run, nil
}

// UsersOwningDigests lists accounts that uploaded any of the given blobs.
func (db *DB) UsersOwningDigests(ctx context.Context, digests []string) ([]string, error) {
	if len(digests) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(digests)), ",")
	args := make([]any, len(digests))
	for i, d := range digests {
		args[i] = d
	}
	rows, err := db.sql.QueryContext(ctx,
		`SELECT DISTINCT user_id FROM account_chunks WHERE digest IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
