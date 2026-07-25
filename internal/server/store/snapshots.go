package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/idgen"
)

// maxChainLength bounds how many delta snapshots may build on one full
// snapshot. Agents are told to run a full walk periodically; this is the
// backstop that keeps tree resolution fast and restores predictable.
const maxChainLength = 64

// StartSnapshot opens a snapshot for a device.
func (db *DB) StartSnapshot(ctx context.Context, userID, deviceID string, req api.StartSnapshotRequest) (string, error) {
	roots, err := json.Marshal(req.Roots)
	if err != nil {
		return "", err
	}
	kind := req.Kind
	if kind != api.SnapshotDelta {
		kind = api.SnapshotFull
	}
	parent := req.ParentID
	if kind == api.SnapshotDelta {
		// Refuse to chain a delta onto a parent we cannot resolve, and force a
		// full snapshot once the chain gets too long. Silently accepting either
		// would produce a snapshot that cannot be restored.
		chain, err := db.snapshotChain(ctx, parent)
		if err != nil || len(chain) == 0 || len(chain) >= maxChainLength {
			kind = api.SnapshotFull
			parent = ""
		}
	}

	id := idgen.NewPrefixed("snp")
	started := req.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	var parentArg any
	if parent != "" {
		parentArg = parent
	}
	_, err = db.sql.ExecContext(ctx,
		`INSERT INTO snapshots (id, user_id, device_id, kind, status, parent_id, started_at, roots)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, userID, deviceID, string(kind), api.SnapshotStatusRunning, parentArg, toMillis(started), string(roots))
	if err != nil {
		return "", err
	}
	return id, nil
}

// AddEntries appends a batch of entries and deletions to an open snapshot.
// Everything happens in one transaction so a dropped connection mid-batch
// leaves no half-written state; the agent simply resends the batch.
func (db *DB) AddEntries(ctx context.Context, snapshotID string, entries []api.Entry, deleted []string) error {
	if len(entries) == 0 && len(deleted) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	entryStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO entries (snapshot_id, path, type, size, mode, mtime, digest, link_target, chunks)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(snapshot_id, path) DO UPDATE SET type = excluded.type, size = excluded.size,
		   mode = excluded.mode, mtime = excluded.mtime, digest = excluded.digest,
		   link_target = excluded.link_target, chunks = excluded.chunks`)
	if err != nil {
		return err
	}
	defer entryStmt.Close()

	refStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO snapshot_chunks (snapshot_id, digest) VALUES (?, ?) ON CONFLICT DO NOTHING`)
	if err != nil {
		return err
	}
	defer refStmt.Close()

	for _, e := range entries {
		path := normalizePath(e.Path)
		if path == "" {
			continue
		}
		var chunkJSON string
		if len(e.Chunks) > 0 {
			raw, err := json.Marshal(e.Chunks)
			if err != nil {
				return err
			}
			chunkJSON = string(raw)
		}
		if _, err := entryStmt.ExecContext(ctx, snapshotID, path, string(e.Type), e.Size, e.Mode,
			toMillis(e.ModTime), e.Digest, e.LinkTarget, chunkJSON); err != nil {
			return err
		}
		for _, digest := range e.Chunks {
			if _, err := refStmt.ExecContext(ctx, snapshotID, digest); err != nil {
				return err
			}
		}
	}

	if len(deleted) > 0 {
		delStmt, err := tx.PrepareContext(ctx,
			`INSERT INTO deletions (snapshot_id, path) VALUES (?, ?) ON CONFLICT DO NOTHING`)
		if err != nil {
			return err
		}
		defer delStmt.Close()
		for _, p := range deleted {
			path := normalizePath(p)
			if path == "" {
				continue
			}
			if _, err := delStmt.ExecContext(ctx, snapshotID, path); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// CompleteSnapshot closes a snapshot after verifying that every chunk it
// references is actually stored.
//
// This check is the difference between a backup product and a hopeful one: a
// snapshot is only marked complete when the server can prove it is restorable.
func (db *DB) CompleteSnapshot(ctx context.Context, snapshotID string, req api.CompleteSnapshotRequest) error {
	missing, err := db.MissingSnapshotChunks(ctx, snapshotID, 5)
	if err != nil {
		return err
	}
	status := api.SnapshotStatusComplete
	errMsg := req.Error
	if len(missing) > 0 {
		status = api.SnapshotStatusFailed
		errMsg = fmt.Sprintf("snapshot references %d or more chunks that were never uploaded (for example %s)",
			len(missing), missing[0])
	} else if req.Error != "" {
		status = api.SnapshotStatusFailed
	}

	completed := req.CompletedAt
	if completed.IsZero() {
		completed = time.Now().UTC()
	}
	res, err := db.sql.ExecContext(ctx,
		`UPDATE snapshots SET status = ?, completed_at = ?, file_count = ?, dir_count = ?, total_bytes = ?,
		 uploaded_bytes = ?, skipped_count = ?, error = ? WHERE id = ? AND status = ?`,
		status, toMillis(completed), req.FileCount, req.DirCount, req.TotalBytes, req.UploadedBytes,
		req.SkippedCount, truncate(errMsg, 1024), snapshotID, api.SnapshotStatusRunning)
	if err := affected(res, err); err != nil {
		return err
	}
	if status == api.SnapshotStatusFailed {
		return fmt.Errorf("store: snapshot marked failed: %s", errMsg)
	}
	return nil
}

// MissingSnapshotChunks lists referenced digests that are not stored, up to
// limit results.
func (db *DB) MissingSnapshotChunks(ctx context.Context, snapshotID string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.sql.QueryContext(ctx,
		`SELECT sc.digest FROM snapshot_chunks sc
		 LEFT JOIN chunks c ON c.digest = sc.digest
		 WHERE sc.snapshot_id = ? AND c.digest IS NULL LIMIT ?`, snapshotID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// FailStaleSnapshots marks snapshots abandoned by a crashed or removed agent as
// failed, so the dashboard never shows a backup "running" for days.
func (db *DB) FailStaleSnapshots(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	res, err := db.sql.ExecContext(ctx,
		`UPDATE snapshots SET status = ?, completed_at = ?, error = 'agent stopped reporting before the snapshot completed'
		 WHERE status = ? AND started_at < ?`,
		api.SnapshotStatusFailed, nowMillis(), api.SnapshotStatusRunning, toMillis(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

const snapshotColumns = `s.id, s.device_id, s.kind, s.status, COALESCE(s.parent_id, ''), s.started_at,
	s.completed_at, s.file_count, s.dir_count, s.total_bytes, s.uploaded_bytes, s.skipped_count, s.roots, s.error`

// SnapshotByID reads one snapshot, scoped to an account.
func (db *DB) SnapshotByID(ctx context.Context, userID, snapshotID string) (*api.Snapshot, error) {
	row := db.sql.QueryRowContext(ctx,
		`SELECT `+snapshotColumns+`, COALESCE(d.name, '') FROM snapshots s
		 LEFT JOIN devices d ON d.id = s.device_id
		 WHERE s.id = ? AND s.user_id = ?`, snapshotID, userID)
	var (
		s          api.Snapshot
		kind       string
		started    int64
		completed  sql.NullInt64
		roots      string
		deviceName string
	)
	err := row.Scan(&s.ID, &s.DeviceID, &kind, &s.Status, &s.ParentID, &started, &completed,
		&s.FileCount, &s.DirCount, &s.TotalBytes, &s.UploadedBytes, &s.SkippedCount, &roots, &s.Error, &deviceName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	s.Kind = api.SnapshotKind(kind)
	s.StartedAt = fromMillis(started)
	s.CompletedAt = nullTime(completed)
	s.DeviceName = deviceName
	_ = json.Unmarshal([]byte(roots), &s.Roots)
	return &s, nil
}

// ListSnapshots returns snapshots for an account, newest first, optionally
// filtered to one device.
func (db *DB) ListSnapshots(ctx context.Context, userID, deviceID string, limit int) ([]api.Snapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + snapshotColumns + `, COALESCE(d.name, '') FROM snapshots s
		LEFT JOIN devices d ON d.id = s.device_id
		WHERE s.user_id = ?`
	args := []any{userID}
	if deviceID != "" {
		query += ` AND s.device_id = ?`
		args = append(args, deviceID)
	}
	query += ` ORDER BY s.started_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []api.Snapshot{}
	for rows.Next() {
		var (
			s          api.Snapshot
			kind       string
			started    int64
			completed  sql.NullInt64
			roots      string
			deviceName string
		)
		if err := rows.Scan(&s.ID, &s.DeviceID, &kind, &s.Status, &s.ParentID, &started, &completed,
			&s.FileCount, &s.DirCount, &s.TotalBytes, &s.UploadedBytes, &s.SkippedCount, &roots, &s.Error,
			&deviceName); err != nil {
			return nil, err
		}
		s.Kind = api.SnapshotKind(kind)
		s.StartedAt = fromMillis(started)
		s.CompletedAt = nullTime(completed)
		s.DeviceName = deviceName
		_ = json.Unmarshal([]byte(roots), &s.Roots)
		out = append(out, s)
	}
	return out, rows.Err()
}

// LatestSnapshotID returns a device's newest completed snapshot, which is what
// a delta backup chains onto.
func (db *DB) LatestSnapshotID(ctx context.Context, deviceID string) (string, error) {
	var id string
	err := db.sql.QueryRowContext(ctx,
		`SELECT id FROM snapshots WHERE device_id = ? AND status = ? ORDER BY started_at DESC LIMIT 1`,
		deviceID, api.SnapshotStatusComplete).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// snapshotChain returns the snapshot ids from the full snapshot at the base of
// the chain up to and including id.
func (db *DB) snapshotChain(ctx context.Context, id string) ([]string, error) {
	if id == "" {
		return nil, nil
	}
	var chain []string
	current := id
	for range maxChainLength + 1 {
		var (
			kind   string
			parent sql.NullString
			status string
		)
		err := db.sql.QueryRowContext(ctx,
			`SELECT kind, parent_id, status FROM snapshots WHERE id = ?`, current).Scan(&kind, &parent, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		chain = append(chain, current)
		if api.SnapshotKind(kind) == api.SnapshotFull {
			// Oldest first, so a higher index means newer and therefore wins.
			reverse(chain)
			return chain, nil
		}
		if !parent.Valid || parent.String == "" {
			// A delta with no parent cannot be resolved on its own.
			return nil, fmt.Errorf("store: snapshot %s is a delta with no parent", current)
		}
		current = parent.String
	}
	return nil, fmt.Errorf("store: snapshot chain for %s exceeds %d links", id, maxChainLength)
}

// Tree resolves the effective file list of a snapshot.
//
// Delta snapshots only store what changed, so the tree is the union of the
// chain from its base full snapshot, where a newer entry shadows an older one
// and a newer deletion removes it. Resolving at read time keeps a daily backup
// cheap: storing a materialised copy of a million-file tree per snapshot would
// cost more than the file data.
func (db *DB) Tree(ctx context.Context, snapshotID, prefix, cursor string, limit int) ([]api.Entry, string, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	chain, err := db.snapshotChain(ctx, snapshotID)
	if err != nil {
		return nil, "", err
	}
	if len(chain) == 0 {
		return nil, "", ErrNotFound
	}

	values := make([]string, 0, len(chain))
	args := make([]any, 0, len(chain)*2+8)
	for i, sid := range chain {
		values = append(values, "(?, ?)")
		args = append(args, sid, i)
	}

	prefix = normalizePath(prefix)
	pathFilter := ""
	if prefix != "" {
		// Range comparisons instead of LIKE: they use the primary key index and
		// need no escaping of % or _ in user paths.
		pathFilter = ` AND (e.path = ? OR (e.path >= ? AND e.path < ?))`
	}

	query := `WITH chain(sid, seq) AS (VALUES ` + strings.Join(values, ", ") + `),
	latest AS (
		SELECT e.path AS path, e.type AS type, e.size AS size, e.mode AS mode, e.mtime AS mtime,
		       e.digest AS digest, e.link_target AS link_target, e.chunks AS chunks, MAX(c.seq) AS seq
		FROM entries e JOIN chain c ON c.sid = e.snapshot_id
		WHERE e.path > ?` + pathFilter + `
		GROUP BY e.path
	),
	removed AS (
		SELECT d.path AS path, MAX(c.seq) AS seq
		FROM deletions d JOIN chain c ON c.sid = d.snapshot_id
		GROUP BY d.path
	)
	SELECT l.path, l.type, l.size, l.mode, l.mtime, l.digest, l.link_target, l.chunks
	FROM latest l LEFT JOIN removed r ON r.path = l.path
	WHERE r.path IS NULL OR r.seq < l.seq
	ORDER BY l.path
	LIMIT ?`

	args = append(args, cursor)
	if prefix != "" {
		args = append(args, prefix, prefix+"/", prefix+"0")
	}
	args = append(args, limit+1)

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	entries := make([]api.Entry, 0, limit)
	for rows.Next() {
		var (
			e         api.Entry
			entryType string
			mtime     int64
			chunkJSON string
		)
		if err := rows.Scan(&e.Path, &entryType, &e.Size, &e.Mode, &mtime, &e.Digest, &e.LinkTarget, &chunkJSON); err != nil {
			return nil, "", err
		}
		e.Type = api.EntryType(entryType)
		e.ModTime = fromMillis(mtime)
		if chunkJSON != "" {
			_ = json.Unmarshal([]byte(chunkJSON), &e.Chunks)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	next := ""
	if len(entries) > limit {
		entries = entries[:limit]
		next = entries[len(entries)-1].Path
	}
	return entries, next, nil
}

// TreeEntry resolves a single path within a snapshot, used to serve a file
// download.
func (db *DB) TreeEntry(ctx context.Context, snapshotID, path string) (*api.Entry, error) {
	entries, _, err := db.Tree(ctx, snapshotID, normalizePath(path), "", 2)
	if err != nil {
		return nil, err
	}
	want := normalizePath(path)
	for i := range entries {
		if entries[i].Path == want {
			return &entries[i], nil
		}
	}
	return nil, ErrNotFound
}

// DeleteSnapshot removes a snapshot. Deltas that depend on it are removed too,
// because a delta without its base is unrestorable.
func (db *DB) DeleteSnapshot(ctx context.Context, userID, snapshotID string) (int, error) {
	var owner string
	err := db.sql.QueryRowContext(ctx, `SELECT user_id FROM snapshots WHERE id = ?`, snapshotID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if owner != userID {
		return 0, ErrNotFound
	}

	ids, err := db.descendants(ctx, snapshotID)
	if err != nil {
		return 0, err
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		// Entries, deletions and chunk references cascade from the snapshot row.
		if _, err := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// descendants returns snapshotID plus every snapshot that chains onto it.
func (db *DB) descendants(ctx context.Context, snapshotID string) ([]string, error) {
	rows, err := db.sql.QueryContext(ctx, `
		WITH RECURSIVE tree(id) AS (
			SELECT ? UNION ALL
			SELECT s.id FROM snapshots s JOIN tree t ON s.parent_id = t.id
		)
		SELECT id FROM tree`, snapshotID)
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

// normalizePath canonicalises a stored path: forward slashes, no leading or
// trailing separator, and no traversal segments.
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return ""
	}
	// Reject traversal outright rather than trying to clean it: a malicious
	// agent must not be able to make a restore write outside its target.
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return ""
		}
	}
	return p
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
