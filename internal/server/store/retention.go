package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/openbackup/openbackup/internal/api"
)

// RetentionRow is the minimum information the retention planner needs about a
// snapshot.
type RetentionRow struct {
	ID            string
	UserID        string
	DeviceID      string
	Kind          api.SnapshotKind
	ParentID      string
	StartedAt     time.Time
	Pinned        bool
	RetentionDays int
}

// SnapshotsForRetention lists every snapshot with its account's retention
// setting, newest first.
func (db *DB) SnapshotsForRetention(ctx context.Context) ([]RetentionRow, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT s.id, s.user_id, s.device_id, s.kind, COALESCE(s.parent_id, ''), s.started_at, s.pinned,
		       u.retention_days
		FROM snapshots s JOIN users u ON u.id = s.user_id
		WHERE s.status = ?
		ORDER BY s.device_id, s.started_at DESC`, api.SnapshotStatusComplete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RetentionRow
	for rows.Next() {
		var (
			r       RetentionRow
			kind    string
			started int64
			pinned  int
		)
		if err := rows.Scan(&r.ID, &r.UserID, &r.DeviceID, &kind, &r.ParentID, &started, &pinned,
			&r.RetentionDays); err != nil {
			return nil, err
		}
		r.Kind = api.SnapshotKind(kind)
		r.StartedAt = fromMillis(started)
		r.Pinned = pinned != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetSnapshotPinned protects a snapshot from retention. Users pin the snapshot
// from the day before something went wrong, and retention must never eat it.
func (db *DB) SetSnapshotPinned(ctx context.Context, userID, snapshotID string, pinned bool) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE snapshots SET pinned = ? WHERE id = ? AND user_id = ?`, boolToInt(pinned), snapshotID, userID)
	return affected(res, err)
}

// DeleteSnapshotCascade removes a snapshot and everything chained onto it,
// without an ownership check. Only internal maintenance may call it.
func (db *DB) DeleteSnapshotCascade(ctx context.Context, snapshotID string) (int, error) {
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
		if _, err := tx.ExecContext(ctx, `DELETE FROM snapshots WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// CountRows returns the row count of a whitelisted table, for the status page.
func (db *DB) CountRows(ctx context.Context, table string) (int64, error) {
	switch table {
	case "users", "devices", "snapshots", "entries", "chunks", "events", "snapshot_chunks":
	default:
		return 0, ErrNotFound
	}
	var n int64
	// The table name is validated against a fixed list above, never interpolated
	// from user input.
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n)
	if err != nil && err != sql.ErrNoRows {
		return 0, err
	}
	return n, nil
}
