package store

import (
	"context"
	"time"

	"github.com/openbackup/openbackup/internal/api"
)

// Usage summarises an account's storage for the dashboard.
//
// StoredBytes counts each chunk once, so a family that backs up the same photo
// library from three devices sees the real cost rather than three copies. That
// number, next to LogicalBytes, is the clearest possible proof that dedup and
// compression are working.
func (db *DB) Usage(ctx context.Context, userID string) (api.UsageStats, error) {
	var stats api.UsageStats
	err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(stored_bytes), 0), COALESCE(SUM(plain_bytes), 0)
		FROM chunks WHERE digest IN (
			SELECT sc.digest FROM snapshot_chunks sc
			JOIN snapshots s ON s.id = sc.snapshot_id
			WHERE s.user_id = ?
		)`, userID).Scan(&stats.ChunkCount, &stats.StoredBytes, &stats.LogicalBytes)
	if err != nil {
		return stats, err
	}

	err = db.sql.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM devices WHERE user_id = ? AND revoked = 0),
			(SELECT COUNT(*) FROM snapshots WHERE user_id = ? AND status = ?),
			(SELECT quota_bytes FROM users WHERE id = ?)`,
		userID, userID, api.SnapshotStatusComplete, userID).
		Scan(&stats.DeviceCount, &stats.SnapshotCount, &stats.QuotaBytes)
	if err != nil {
		return stats, err
	}

	if stats.StoredBytes > 0 {
		stats.DedupRatio = float64(stats.LogicalBytes) / float64(stats.StoredBytes)
	}
	return stats, nil
}

// AccountStoredBytes returns just the deduplicated size, for quota checks on the
// upload path where the full stats query would be wasteful.
func (db *DB) AccountStoredBytes(ctx context.Context, userID string) (int64, error) {
	var n int64
	err := db.sql.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(stored_bytes), 0) FROM chunks WHERE digest IN (
			SELECT sc.digest FROM snapshot_chunks sc
			JOIN snapshots s ON s.id = sc.snapshot_id
			WHERE s.user_id = ?
		)`, userID).Scan(&n)
	return n, err
}

// DailyUpload reports bytes uploaded per day over the last n days, for the
// dashboard chart.
type DailyUpload struct {
	Day   string `json:"day"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

// UploadHistory aggregates completed snapshots by day.
func (db *DB) UploadHistory(ctx context.Context, userID string, days int) ([]DailyUpload, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	cutoff := toMillis(time.Now().UTC().AddDate(0, 0, -days))
	rows, err := db.sql.QueryContext(ctx, `
		SELECT strftime('%Y-%m-%d', started_at / 1000, 'unixepoch') AS day,
		       COALESCE(SUM(uploaded_bytes), 0), COALESCE(SUM(file_count), 0)
		FROM snapshots
		WHERE user_id = ? AND started_at >= ? AND status = ?
		GROUP BY day ORDER BY day`, userID, cutoff, api.SnapshotStatusComplete)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DailyUpload{}
	for rows.Next() {
		var d DailyUpload
		if err := rows.Scan(&d.Day, &d.Bytes, &d.Files); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
