package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/idgen"
)

// Device is an enrolled agent.
type Device struct {
	ID           string
	UserID       string
	Name         string
	Hostname     string
	Platform     api.Platform
	OSVersion    string
	AgentVersion string
	KeyID        string
	CreatedAt    time.Time
	LastSeen     *time.Time
	State        string
	StateReason  string
	QueuedFiles  int64
	QueuedBytes  int64
	LastError    string
	BatteryPct   int
	OnMetered    bool
	Revoked      bool
}

// CreateDevice registers an agent and returns the stored record. tokenHash must
// already be hashed by the caller.
func (db *DB) CreateDevice(ctx context.Context, d Device, tokenHash string) (*Device, error) {
	if d.ID == "" {
		d.ID = idgen.NewPrefixed("dev")
	}
	d.CreatedAt = time.Now().UTC()
	d.State = api.StateIdle
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO devices (id, user_id, name, hostname, platform, os_version, agent_version, key_id,
		 token_hash, created_at, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.UserID, d.Name, d.Hostname, string(d.Platform), d.OSVersion, d.AgentVersion, d.KeyID,
		tokenHash, toMillis(d.CreatedAt), d.State)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

const deviceColumns = `id, user_id, name, hostname, platform, os_version, agent_version, key_id,
	created_at, last_seen, state, state_reason, queued_files, queued_bytes, last_error,
	battery_pct, on_metered, revoked`

// DeviceByTokenHash authenticates an agent request.
func (db *DB) DeviceByTokenHash(ctx context.Context, tokenHash string) (*Device, error) {
	return scanDevice(db.sql.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE token_hash = ? AND revoked = 0`, tokenHash))
}

// DeviceByID looks up a device.
func (db *DB) DeviceByID(ctx context.Context, id string) (*Device, error) {
	return scanDevice(db.sql.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id))
}

func scanDevice(row *sql.Row) (*Device, error) {
	var (
		d        Device
		platform string
		created  int64
		lastSeen sql.NullInt64
		metered  int
		revoked  int
	)
	err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.Hostname, &platform, &d.OSVersion, &d.AgentVersion,
		&d.KeyID, &created, &lastSeen, &d.State, &d.StateReason, &d.QueuedFiles, &d.QueuedBytes,
		&d.LastError, &d.BatteryPct, &metered, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Platform = api.Platform(platform)
	d.CreatedAt = fromMillis(created)
	d.LastSeen = nullTime(lastSeen)
	d.OnMetered = metered != 0
	d.Revoked = revoked != 0
	return &d, nil
}

// ListDevices returns an account's devices with their rolled-up stats, ready
// for the dashboard.
func (db *DB) ListDevices(ctx context.Context, userID string) ([]api.Device, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT d.id, d.name, d.hostname, d.platform, d.os_version, d.agent_version, d.key_id,
		       d.created_at, d.last_seen, d.state, d.state_reason, d.queued_files, d.queued_bytes, d.last_error,
		       (SELECT COUNT(*) FROM snapshots s WHERE s.device_id = d.id AND s.status = ?) AS snapshot_count,
		       COALESCE((SELECT s.total_bytes FROM snapshots s
		                 WHERE s.device_id = d.id AND s.status = ?
		                 ORDER BY s.started_at DESC LIMIT 1), 0) AS logical_bytes,
		       COALESCE((SELECT s.completed_at FROM snapshots s
		                 WHERE s.device_id = d.id AND s.status = ?
		                 ORDER BY s.started_at DESC LIMIT 1), 0) AS last_complete
		FROM devices d WHERE d.user_id = ? AND d.revoked = 0
		ORDER BY d.created_at ASC`,
		api.SnapshotStatusComplete, api.SnapshotStatusComplete, api.SnapshotStatusComplete, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []api.Device{}
	for rows.Next() {
		var (
			d            api.Device
			platform     string
			created      int64
			lastSeen     sql.NullInt64
			lastComplete int64
		)
		if err := rows.Scan(&d.ID, &d.Name, &d.Hostname, &platform, &d.OSVersion, &d.AgentVersion, &d.KeyID,
			&created, &lastSeen, &d.State, &d.StateReason, &d.QueuedFiles, &d.QueuedBytes, &d.LastError,
			&d.SnapshotCount, &d.LogicalBytes, &lastComplete); err != nil {
			return nil, err
		}
		d.Platform = api.Platform(platform)
		d.CreatedAt = fromMillis(created)
		d.LastSeen = nullTime(lastSeen)
		d.Health = deviceHealth(d, fromMillis(lastComplete))
		out = append(out, d)
	}
	return out, rows.Err()
}

// deviceHealth derives the traffic-light status shown in the dashboard.
//
// A backup tool that says "OK" while a device has silently been offline for a
// week is worse than useless, so health is derived from when the device last
// *completed* a snapshot, not from whether it is currently connected.
func deviceHealth(d api.Device, lastComplete time.Time) string {
	switch {
	case d.LastError != "" || d.State == api.StateError:
		return api.HealthError
	case lastComplete.IsZero():
		return api.HealthNeverRun
	case time.Since(lastComplete) > 48*time.Hour:
		return api.HealthStale
	default:
		return api.HealthOK
	}
}

// TouchDevice records a heartbeat.
func (db *DB) TouchDevice(ctx context.Context, deviceID string, hb api.HeartbeatRequest) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE devices SET last_seen = ?, state = ?, state_reason = ?, queued_files = ?, queued_bytes = ?,
		 last_error = ?, agent_version = COALESCE(NULLIF(?, ''), agent_version),
		 os_version = COALESCE(NULLIF(?, ''), os_version), battery_pct = ?, on_metered = ?
		 WHERE id = ?`,
		nowMillis(), hb.State, truncate(hb.StateReason, 256), hb.QueuedFiles, hb.QueuedBytes,
		truncate(hb.LastError, 512), hb.AgentVersion, hb.OSVersion, hb.BatteryPct, boolToInt(hb.OnMetered),
		deviceID)
	return affected(res, err)
}

// RenameDevice updates the display name.
func (db *DB) RenameDevice(ctx context.Context, userID, deviceID, name string) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE devices SET name = ? WHERE id = ? AND user_id = ?`, truncate(name, 128), deviceID, userID)
	return affected(res, err)
}

// RevokeDevice disables an agent's credentials without deleting its backups.
// This is the safe default for a lost laptop: the device can no longer write,
// but the user can still restore from it.
func (db *DB) RevokeDevice(ctx context.Context, userID, deviceID string) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE devices SET revoked = 1, state = 'revoked' WHERE id = ? AND user_id = ?`, deviceID, userID)
	return affected(res, err)
}

// DeleteDevice removes a device and all of its snapshots. Blobs it uniquely
// owned are freed by the next garbage collection pass.
func (db *DB) DeleteDevice(ctx context.Context, userID, deviceID string) error {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID)
	return affected(res, err)
}
