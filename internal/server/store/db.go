package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// Pure Go SQLite driver: the server must cross-compile and run in a
	// scratch container without CGO or a libc.
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned by lookups that find nothing.
var ErrNotFound = errors.New("store: not found")

// ErrConflict is returned when an operation is refused because of current state
// (for example writing entries into a completed snapshot).
var ErrConflict = errors.New("store: conflict")

// ErrInvalidPath is returned when a stored path is rejected (traversal, empty).
var ErrInvalidPath = errors.New("store: invalid path")

// DB wraps the metadata database.
type DB struct {
	sql  *sql.DB
	path string
}

// OpenDB opens (creating if needed) the SQLite database at path and applies
// migrations.
func OpenDB(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("store: database path must be set")
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	// WAL keeps readers (the dashboard) from blocking writers (agent uploads).
	// synchronous=NORMAL is the right trade-off with WAL: a crash can lose the
	// last transaction, but blobs are content-addressed so the agent simply
	// re-sends. busy_timeout absorbs the brief contention of many agents.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_txlock=immediate"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite serialises writes anyway; a single writer connection avoids
	// SQLITE_BUSY storms, while WAL still allows concurrent reads.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db := &DB{sql: sqlDB, path: path}
	if err := db.migrate(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the database.
func (db *DB) Close() error { return db.sql.Close() }

// SQL exposes the underlying handle for the few places that need custom queries.
func (db *DB) SQL() *sql.DB { return db.sql }

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// migrations are applied in order and recorded in schema_migrations. Each entry
// must be append-only: never edit a released migration.
var migrations = []struct {
	name string
	stmt string
}{
	{"0001_init", `
CREATE TABLE users (
    id             TEXT PRIMARY KEY,
    email          TEXT NOT NULL UNIQUE,
    password_hash  TEXT NOT NULL,
    is_admin       INTEGER NOT NULL DEFAULT 0,
    quota_bytes    INTEGER NOT NULL DEFAULT 0,
    retention_days INTEGER NOT NULL DEFAULT 30,
    created_at     INTEGER NOT NULL
);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE join_tokens (
    code_hash  TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label      TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at    INTEGER,
    device_id  TEXT
);
CREATE INDEX idx_join_tokens_user ON join_tokens(user_id);

CREATE TABLE devices (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    hostname      TEXT NOT NULL DEFAULT '',
    platform      TEXT NOT NULL,
    os_version    TEXT NOT NULL DEFAULT '',
    agent_version TEXT NOT NULL DEFAULT '',
    key_id        TEXT NOT NULL DEFAULT '',
    token_hash    TEXT NOT NULL UNIQUE,
    created_at    INTEGER NOT NULL,
    last_seen     INTEGER,
    state         TEXT NOT NULL DEFAULT 'idle',
    state_reason  TEXT NOT NULL DEFAULT '',
    queued_files  INTEGER NOT NULL DEFAULT 0,
    queued_bytes  INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT NOT NULL DEFAULT '',
    battery_pct   INTEGER NOT NULL DEFAULT 0,
    on_metered    INTEGER NOT NULL DEFAULT 0,
    revoked       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_devices_user ON devices(user_id);

CREATE TABLE snapshots (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id      TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind           TEXT NOT NULL,
    status         TEXT NOT NULL,
    parent_id      TEXT,
    started_at     INTEGER NOT NULL,
    completed_at   INTEGER,
    file_count     INTEGER NOT NULL DEFAULT 0,
    dir_count      INTEGER NOT NULL DEFAULT 0,
    total_bytes    INTEGER NOT NULL DEFAULT 0,
    uploaded_bytes INTEGER NOT NULL DEFAULT 0,
    skipped_count  INTEGER NOT NULL DEFAULT 0,
    roots          TEXT NOT NULL DEFAULT '[]',
    error          TEXT NOT NULL DEFAULT '',
    pinned         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_snapshots_device ON snapshots(device_id, started_at DESC);
CREATE INDEX idx_snapshots_user ON snapshots(user_id, started_at DESC);

CREATE TABLE entries (
    snapshot_id  TEXT NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    path         TEXT NOT NULL,
    type         TEXT NOT NULL,
    size         INTEGER NOT NULL DEFAULT 0,
    mode         INTEGER NOT NULL DEFAULT 0,
    mtime        INTEGER NOT NULL DEFAULT 0,
    digest       TEXT NOT NULL DEFAULT '',
    link_target  TEXT NOT NULL DEFAULT '',
    chunks       TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (snapshot_id, path)
) WITHOUT ROWID;

CREATE TABLE deletions (
    snapshot_id TEXT NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    path        TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, path)
) WITHOUT ROWID;

CREATE TABLE chunks (
    digest       TEXT PRIMARY KEY,
    stored_bytes INTEGER NOT NULL,
    plain_bytes  INTEGER NOT NULL,
    encrypted    INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL
) WITHOUT ROWID;

CREATE TABLE snapshot_chunks (
    snapshot_id TEXT NOT NULL REFERENCES snapshots(id) ON DELETE CASCADE,
    digest      TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, digest)
) WITHOUT ROWID;
CREATE INDEX idx_snapshot_chunks_digest ON snapshot_chunks(digest);

CREATE TABLE events (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id TEXT,
    at        INTEGER NOT NULL,
    level     TEXT NOT NULL DEFAULT 'info',
    message   TEXT NOT NULL,
    path      TEXT NOT NULL DEFAULT '',
    reason    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_events_user_at ON events(user_id, at DESC);

CREATE TABLE key_escrow (
    user_id     TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    key_id      TEXT NOT NULL,
    wrapped_key BLOB NOT NULL,
    salt        BLOB NOT NULL,
    created_at  INTEGER NOT NULL
);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`},

	// Upload speed and the encryption requirement started as server-wide flags,
	// but they are decisions an owner makes per account from the dashboard, and
	// agents receive them in the policy on every heartbeat.
	{"0002_account_policy", `
ALTER TABLE users ADD COLUMN max_upload_bytes_per_sec INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN require_encryption INTEGER NOT NULL DEFAULT 0;
`},

	// Live backup progress for the dashboard Logs page (which file is uploading).
	{"0003_device_progress", `
ALTER TABLE devices ADD COLUMN current_path TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN files_done INTEGER NOT NULL DEFAULT 0;
ALTER TABLE devices ADD COLUMN files_total INTEGER NOT NULL DEFAULT 0;
`},
}

func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.sql.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
        name TEXT PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("store: create migrations table: %w", err)
	}
	for _, m := range migrations {
		var exists int
		err := db.sql.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE name = ?`, m.name).Scan(&exists)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		tx, err := db.sql.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
			m.name, nowMillis()); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Vacuum reclaims space after large deletions. Called by the retention job.
func (db *DB) Vacuum(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `PRAGMA incremental_vacuum; PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// Setting reads a server setting.
func (db *DB) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := db.sql.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting writes a server setting.
func (db *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := db.sql.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// nowMillis is the single time source for stored timestamps.
func nowMillis() int64 { return time.Now().UTC().UnixMilli() }

// toMillis converts a time to the stored representation.
func toMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().UnixMilli()
}

// fromMillis converts a stored timestamp back to a time.
func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// nullTime converts a nullable stored timestamp.
func nullTime(ms sql.NullInt64) *time.Time {
	if !ms.Valid || ms.Int64 == 0 {
		return nil
	}
	t := fromMillis(ms.Int64)
	return &t
}
