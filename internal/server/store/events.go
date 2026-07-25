package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/openbackup/openbackup/internal/api"
)

// maxEventMessage bounds stored log lines so a looping agent cannot fill the
// database with one enormous message.
const maxEventMessage = 1024

// AddEvents appends agent log events.
func (db *DB) AddEvents(ctx context.Context, userID, deviceID string, events []api.Event) error {
	if len(events) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO events (user_id, device_id, at, level, message, path, reason) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, e := range events {
		at := e.At
		if at.IsZero() {
			at = time.Now().UTC()
		}
		level := e.Level
		if level == "" {
			level = "info"
		}
		if _, err := stmt.ExecContext(ctx, userID, deviceID, toMillis(at), level,
			truncate(e.Message, maxEventMessage), truncate(e.Path, 1024), truncate(e.Reason, 256)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// StoredEvent is an event as returned to the dashboard.
type StoredEvent struct {
	ID         int64     `json:"id"`
	DeviceID   string    `json:"device_id,omitempty"`
	DeviceName string    `json:"device_name,omitempty"`
	At         time.Time `json:"at"`
	Level      string    `json:"level"`
	Message    string    `json:"message"`
	Path       string    `json:"path,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// ListEvents returns recent events, newest first, optionally filtered by device
// and level.
func (db *DB) ListEvents(ctx context.Context, userID, deviceID, level string, limit int) ([]StoredEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT e.id, COALESCE(e.device_id, ''), COALESCE(d.name, ''), e.at, e.level, e.message, e.path, e.reason
		FROM events e LEFT JOIN devices d ON d.id = e.device_id
		WHERE e.user_id = ?`
	args := []any{userID}
	if deviceID != "" {
		query += ` AND e.device_id = ?`
		args = append(args, deviceID)
	}
	if level != "" {
		query += ` AND e.level = ?`
		args = append(args, level)
	}
	query += ` ORDER BY e.at DESC, e.id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []StoredEvent{}
	for rows.Next() {
		var (
			e  StoredEvent
			at int64
		)
		if err := rows.Scan(&e.ID, &e.DeviceID, &e.DeviceName, &at, &e.Level, &e.Message, &e.Path, &e.Reason); err != nil {
			return nil, err
		}
		e.At = fromMillis(at)
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneEvents keeps the activity log bounded: events older than the retention
// window are dropped, and each account keeps at most maxRows rows.
func (db *DB) PruneEvents(ctx context.Context, olderThan time.Duration, maxRows int64) (int64, error) {
	cutoff := toMillis(time.Now().UTC().Add(-olderThan))
	var total int64
	res, err := db.sql.ExecContext(ctx, `DELETE FROM events WHERE at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err == nil {
		total += n
	}
	if maxRows > 0 {
		res, err = db.sql.ExecContext(ctx, `
			DELETE FROM events WHERE id IN (
				SELECT id FROM events ORDER BY id DESC LIMIT -1 OFFSET ?
			)`, maxRows)
		if err != nil {
			return total, err
		}
		if n, err := res.RowsAffected(); err == nil {
			total += n
		}
	}
	return total, nil
}

// LatestEventAt reports the most recent event time for a device, or zero.
func (db *DB) LatestEventAt(ctx context.Context, deviceID string) (time.Time, error) {
	var at sql.NullInt64
	err := db.sql.QueryRowContext(ctx, `SELECT MAX(at) FROM events WHERE device_id = ?`, deviceID).Scan(&at)
	if err != nil {
		return time.Time{}, err
	}
	if !at.Valid {
		return time.Time{}, nil
	}
	return fromMillis(at.Int64), nil
}
