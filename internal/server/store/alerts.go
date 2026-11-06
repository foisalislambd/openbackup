package store

import (
	"context"
	"time"

	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/idgen"
)

// Alert kinds the dashboard knows how to explain.
const (
	AlertBackupFailed  = "backup_failed"
	AlertBackupStale   = "backup_stale"
	AlertNeverBackedUp = "never_backed_up"
	AlertIntegrity     = "integrity"
)

// Alert is an open problem the owner has not dismissed.
type Alert struct {
	ID         string
	UserID     string
	DeviceID   string
	DeviceName string
	Kind       string
	Level      string
	Message    string
	OpenedAt   time.Time
}

// ListOpenAlerts returns undismissed alerts, newest first.
func (db *DB) ListOpenAlerts(ctx context.Context, userID string) ([]Alert, error) {
	rows, err := db.sql.QueryContext(ctx, `
		SELECT a.id, a.device_id, COALESCE(d.name, ''), a.kind, a.level, a.message, a.opened_at
		FROM alerts a
		LEFT JOIN devices d ON d.id = a.device_id
		WHERE a.user_id = ? AND a.acked_at IS NULL
		ORDER BY a.opened_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var (
			a      Alert
			opened int64
		)
		if err := rows.Scan(&a.ID, &a.DeviceID, &a.DeviceName, &a.Kind, &a.Level, &a.Message, &opened); err != nil {
			return nil, err
		}
		a.UserID = userID
		a.OpenedAt = fromMillis(opened)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AckAlert dismisses one alert. A missing id is not an error: the dashboard
// may dismiss something maintenance already cleared.
func (db *DB) AckAlert(ctx context.Context, userID, alertID string) error {
	_, err := db.sql.ExecContext(ctx,
		`UPDATE alerts SET acked_at = ? WHERE id = ? AND user_id = ? AND acked_at IS NULL`,
		nowMillis(), alertID, userID)
	return err
}

// ReconcileDeviceAlerts opens or clears backup-health alerts for one account
// so the dashboard matches what the devices are actually doing.
func (db *DB) ReconcileDeviceAlerts(ctx context.Context, userID string) error {
	user, err := db.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	devices, err := db.ListDevices(ctx, userID)
	if err != nil {
		return err
	}
	wanted := deviceAlerts(userID, devices, time.Duration(user.Policy.AlertAfterHours)*time.Hour, time.Now().UTC())

	open, err := db.ListOpenAlerts(ctx, userID)
	if err != nil {
		return err
	}
	openByKey := make(map[string]Alert, len(open))
	for _, a := range open {
		if a.Kind == AlertIntegrity {
			continue
		}
		openByKey[a.DeviceID+"\x00"+a.Kind] = a
	}
	wantedKey := make(map[string]Alert, len(wanted))
	for _, a := range wanted {
		key := a.DeviceID + "\x00" + a.Kind
		wantedKey[key] = a
		if existing, ok := openByKey[key]; ok {
			if existing.Message != a.Message {
				if _, err := db.sql.ExecContext(ctx, `UPDATE alerts SET message = ? WHERE id = ?`, a.Message, existing.ID); err != nil {
					return err
				}
			}
			continue
		}
		if _, err := db.sql.ExecContext(ctx,
			`INSERT INTO alerts (id, user_id, device_id, kind, level, message, opened_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			idgen.New(), userID, a.DeviceID, a.Kind, a.Level, a.Message, nowMillis()); err != nil {
			return err
		}
	}
	for key, existing := range openByKey {
		if _, ok := wantedKey[key]; ok {
			continue
		}
		if err := db.AckAlert(ctx, userID, existing.ID); err != nil {
			return err
		}
	}
	return nil
}

// RaiseIntegrityAlert opens one account-level alert, replacing any open one so
// a repeated failing check does not stack identical rows.
func (db *DB) RaiseIntegrityAlert(ctx context.Context, userID, message string) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE alerts SET message = ?, opened_at = ? WHERE user_id = ? AND kind = ? AND device_id = '' AND acked_at IS NULL`,
		message, nowMillis(), userID, AlertIntegrity)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n > 0 {
		return nil
	}
	_, err = db.sql.ExecContext(ctx,
		`INSERT INTO alerts (id, user_id, device_id, kind, level, message, opened_at)
		 VALUES (?, ?, '', ?, 'bad', ?, ?)`,
		idgen.New(), userID, AlertIntegrity, message, nowMillis())
	return err
}

func deviceAlerts(userID string, devices []api.Device, staleAfter time.Duration, now time.Time) []Alert {
	out := make([]Alert, 0, len(devices))
	for _, d := range devices {
		switch {
		case d.LastError != "" || d.State == api.StateError:
			msg := d.Name + " reported a backup error"
			if d.LastError != "" {
				msg = d.Name + ": " + d.LastError
			}
			out = append(out, Alert{UserID: userID, DeviceID: d.ID, Kind: AlertBackupFailed, Level: "bad", Message: msg})
		case d.LastBackupAt == nil && staleAfter > 0 && now.Sub(d.CreatedAt) > staleAfter:
			out = append(out, Alert{
				UserID: userID, DeviceID: d.ID, Kind: AlertNeverBackedUp, Level: "warn",
				Message: d.Name + " has not completed a first backup",
			})
		case d.LastBackupAt != nil && staleAfter > 0 && now.Sub(*d.LastBackupAt) > staleAfter:
			out = append(out, Alert{
				UserID: userID, DeviceID: d.ID, Kind: AlertBackupStale, Level: "warn",
				Message: d.Name + " has not finished a backup recently",
			})
		}
	}
	return out
}
