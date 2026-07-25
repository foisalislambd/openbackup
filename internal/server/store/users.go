package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/idgen"
)

// User is an account.
type User struct {
	ID            string
	Email         string
	PasswordHash  string
	IsAdmin       bool
	QuotaBytes    int64
	RetentionDays int
	CreatedAt     time.Time
}

// CreateUser inserts a user. Email comparison is case-insensitive because
// treating Alice@ and alice@ as different accounts only ever confuses people.
func (db *DB) CreateUser(ctx context.Context, email, passwordHash string, isAdmin bool) (*User, error) {
	u := &User{
		ID:            idgen.NewPrefixed("usr"),
		Email:         normalizeEmail(email),
		PasswordHash:  passwordHash,
		IsAdmin:       isAdmin,
		RetentionDays: api.DefaultPolicy().RetentionDays,
		CreatedAt:     time.Now().UTC(),
	}
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO users (id, email, password_hash, is_admin, quota_bytes, retention_days, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, boolToInt(isAdmin), u.QuotaBytes, u.RetentionDays, toMillis(u.CreatedAt))
	if err != nil {
		return nil, err
	}
	return u, nil
}

// UserByEmail looks up an account for login.
func (db *DB) UserByEmail(ctx context.Context, email string) (*User, error) {
	return db.scanUser(db.sql.QueryRowContext(ctx,
		`SELECT id, email, password_hash, is_admin, quota_bytes, retention_days, created_at
		 FROM users WHERE email = ?`, normalizeEmail(email)))
}

// UserByID looks up an account by id.
func (db *DB) UserByID(ctx context.Context, id string) (*User, error) {
	return db.scanUser(db.sql.QueryRowContext(ctx,
		`SELECT id, email, password_hash, is_admin, quota_bytes, retention_days, created_at
		 FROM users WHERE id = ?`, id))
}

func (db *DB) scanUser(row *sql.Row) (*User, error) {
	var (
		u       User
		admin   int
		created int64
	)
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &admin, &u.QuotaBytes, &u.RetentionDays, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.IsAdmin = admin != 0
	u.CreatedAt = fromMillis(created)
	return &u, nil
}

// FirstUser returns the oldest account. Single-account servers are the common
// self-hosted case, so CLI commands can default to it instead of demanding an
// email address.
func (db *DB) FirstUser(ctx context.Context) (*User, error) {
	return db.scanUser(db.sql.QueryRowContext(ctx,
		`SELECT id, email, password_hash, is_admin, quota_bytes, retention_days, created_at
		 FROM users ORDER BY created_at ASC LIMIT 1`))
}

// CountUsers reports how many accounts exist. The server uses it to decide
// whether to show the first-run setup screen.
func (db *DB) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UpdateUserPassword sets a new password hash.
func (db *DB) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	res, err := db.sql.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	return affected(res, err)
}

// UpdateUserPolicy stores the account's retention and quota settings.
func (db *DB) UpdateUserPolicy(ctx context.Context, userID string, retentionDays int, quotaBytes int64) error {
	res, err := db.sql.ExecContext(ctx,
		`UPDATE users SET retention_days = ?, quota_bytes = ? WHERE id = ?`, retentionDays, quotaBytes, userID)
	return affected(res, err)
}

// CreateSession stores a dashboard session. Only the hash of the token is
// stored, so a database leak does not hand out live sessions.
func (db *DB) CreateSession(ctx context.Context, userID, tokenHash, userAgent string, ttl time.Duration) error {
	now := time.Now().UTC()
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, user_agent) VALUES (?, ?, ?, ?, ?)`,
		tokenHash, userID, toMillis(now), toMillis(now.Add(ttl)), truncate(userAgent, 256))
	return err
}

// SessionUser resolves a session token hash to its account, refusing expired
// sessions.
func (db *DB) SessionUser(ctx context.Context, tokenHash string) (*User, error) {
	var userID string
	var expires int64
	err := db.sql.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if expires < nowMillis() {
		_ = db.DeleteSession(ctx, tokenHash)
		return nil, ErrNotFound
	}
	return db.UserByID(ctx, userID)
}

// DeleteSession logs a browser out.
func (db *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteExpiredSessions is called by the maintenance loop.
func (db *DB) DeleteExpiredSessions(ctx context.Context) (int64, error) {
	res, err := db.sql.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, nowMillis())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// JoinToken is a one-time device enrolment code.
type JoinToken struct {
	CodeHash  string
	UserID    string
	Label     string
	CreatedAt time.Time
	ExpiresAt time.Time
	UsedAt    *time.Time
	DeviceID  string
}

// CreateJoinToken stores a hashed enrolment code.
func (db *DB) CreateJoinToken(ctx context.Context, userID, codeHash, label string, ttl time.Duration) (*JoinToken, error) {
	now := time.Now().UTC()
	jt := &JoinToken{
		CodeHash:  codeHash,
		UserID:    userID,
		Label:     truncate(label, 128),
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO join_tokens (code_hash, user_id, label, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		jt.CodeHash, jt.UserID, jt.Label, toMillis(jt.CreatedAt), toMillis(jt.ExpiresAt))
	if err != nil {
		return nil, err
	}
	return jt, nil
}

// ConsumeJoinToken atomically marks a valid code as used and returns its owner.
// The single UPDATE ... WHERE used_at IS NULL is what makes enrolment codes
// genuinely one-time even if two devices race.
func (db *DB) ConsumeJoinToken(ctx context.Context, codeHash, deviceID string) (userID string, err error) {
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var expires int64
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, expires_at FROM join_tokens WHERE code_hash = ? AND used_at IS NULL`,
		codeHash).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if expires < nowMillis() {
		return "", ErrNotFound
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE join_tokens SET used_at = ?, device_id = ? WHERE code_hash = ? AND used_at IS NULL`,
		nowMillis(), deviceID, codeHash)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return userID, nil
}

// ListJoinTokens returns the account's outstanding codes.
func (db *DB) ListJoinTokens(ctx context.Context, userID string) ([]JoinToken, error) {
	rows, err := db.sql.QueryContext(ctx,
		`SELECT code_hash, user_id, label, created_at, expires_at, used_at, COALESCE(device_id, '')
		 FROM join_tokens WHERE user_id = ? ORDER BY created_at DESC LIMIT 50`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JoinToken
	for rows.Next() {
		var (
			jt      JoinToken
			created int64
			expires int64
			used    sql.NullInt64
		)
		if err := rows.Scan(&jt.CodeHash, &jt.UserID, &jt.Label, &created, &expires, &used, &jt.DeviceID); err != nil {
			return nil, err
		}
		jt.CreatedAt = fromMillis(created)
		jt.ExpiresAt = fromMillis(expires)
		jt.UsedAt = nullTime(used)
		out = append(out, jt)
	}
	return out, rows.Err()
}

// DeleteExpiredJoinTokens removes stale codes.
func (db *DB) DeleteExpiredJoinTokens(ctx context.Context) (int64, error) {
	res, err := db.sql.ExecContext(ctx,
		`DELETE FROM join_tokens WHERE expires_at < ? AND used_at IS NULL`, nowMillis())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PutEscrow stores the passphrase-wrapped master key for an account.
func (db *DB) PutEscrow(ctx context.Context, userID string, e api.Escrow) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO key_escrow (user_id, key_id, wrapped_key, salt, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET key_id = excluded.key_id, wrapped_key = excluded.wrapped_key,
		 salt = excluded.salt, created_at = excluded.created_at`,
		userID, e.KeyID, e.WrappedKey, e.Salt, nowMillis())
	return err
}

// Escrow reads the wrapped master key.
func (db *DB) Escrow(ctx context.Context, userID string) (*api.Escrow, error) {
	var (
		e       api.Escrow
		created int64
	)
	err := db.sql.QueryRowContext(ctx,
		`SELECT key_id, wrapped_key, salt, created_at FROM key_escrow WHERE user_id = ?`, userID).
		Scan(&e.KeyID, &e.WrappedKey, &e.Salt, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.CreatedAt = fromMillis(created)
	return &e, nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// affected turns "UPDATE matched nothing" into ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
