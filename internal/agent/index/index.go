// Package index is the agent's local cache of what has already been backed up.
//
// It exists to answer one question cheaply: has this file changed since the last
// backup? Answering it from size and modification time means a routine backup
// reads almost nothing from disk, which is what keeps idle CPU near zero on a
// machine with a million files. Only files whose size or timestamp moved are
// re-read and re-chunked.
package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Index is the local file state cache.
type Index struct {
	db *sql.DB
}

// FileState is what the agent remembers about one file.
type FileState struct {
	Path string
	Size int64
	// ModTime is truncated to the second, because that is the precision every
	// filesystem and protocol agrees on.
	ModTime time.Time
	Digest  string
	Chunks  []string
	// Mode holds POSIX permission bits.
	Mode uint32
}

// Open opens or creates the index at path.
func Open(ctx context.Context, path string) (*Index, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// The index is a cache: losing the last transaction after a crash only means
	// re-hashing a few files, so durability can be relaxed for speed.
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(OFF)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("index: open %s: %w", path, err)
	}
	idx := &Index{db: db}
	if err := idx.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return idx, nil
}

// Close releases the index.
func (i *Index) Close() error { return i.db.Close() }

func (i *Index) migrate(ctx context.Context) error {
	_, err := i.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS files (
    path       TEXT PRIMARY KEY,
    size       INTEGER NOT NULL,
    mtime      INTEGER NOT NULL,
    digest     TEXT NOT NULL,
    chunks     TEXT NOT NULL,
    mode       INTEGER NOT NULL DEFAULT 0,
    generation INTEGER NOT NULL DEFAULT 0
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`)
	return err
}

// Lookup returns the remembered state of a path.
func (i *Index) Lookup(ctx context.Context, path string) (*FileState, bool) {
	var (
		state     FileState
		mtime     int64
		chunkJSON string
	)
	state.Path = path
	err := i.db.QueryRowContext(ctx,
		`SELECT size, mtime, digest, chunks, mode FROM files WHERE path = ?`, path).
		Scan(&state.Size, &mtime, &state.Digest, &chunkJSON, &state.Mode)
	if err != nil {
		return nil, false
	}
	state.ModTime = time.Unix(mtime, 0).UTC()
	if chunkJSON != "" {
		_ = json.Unmarshal([]byte(chunkJSON), &state.Chunks)
	}
	return &state, true
}

// Unchanged reports whether a file matches its remembered size and timestamp.
//
// Content hashing every file would be more certain but would read the entire
// disk on every pass. Size plus second-precision mtime is the trade-off every
// backup tool makes; the periodic full scan and the server-side verification
// catch the rare case where a file is edited in place without touching either.
func (i *Index) Unchanged(ctx context.Context, path string, size int64, modTime time.Time) (*FileState, bool) {
	state, ok := i.Lookup(ctx, path)
	if !ok {
		return nil, false
	}
	if state.Size != size {
		return state, false
	}
	if !state.ModTime.Equal(modTime.UTC().Truncate(time.Second)) {
		return state, false
	}
	if len(state.Chunks) == 0 && size > 0 {
		// A remembered file with no chunks cannot be restored, so treat it as
		// changed and re-upload it.
		return state, false
	}
	return state, true
}

// Put records a file's state.
func (i *Index) Put(ctx context.Context, state FileState, generation int64) error {
	chunkJSON := "[]"
	if len(state.Chunks) > 0 {
		raw, err := json.Marshal(state.Chunks)
		if err != nil {
			return err
		}
		chunkJSON = string(raw)
	}
	_, err := i.db.ExecContext(ctx, `
		INSERT INTO files (path, size, mtime, digest, chunks, mode, generation)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET size = excluded.size, mtime = excluded.mtime,
			digest = excluded.digest, chunks = excluded.chunks, mode = excluded.mode,
			generation = excluded.generation`,
		state.Path, state.Size, state.ModTime.UTC().Unix(), state.Digest, chunkJSON, state.Mode, generation)
	return err
}

// Touch marks a path as seen in the current scan without rewriting its content,
// which is the common case for unchanged files.
func (i *Index) Touch(ctx context.Context, path string, generation int64) error {
	_, err := i.db.ExecContext(ctx, `UPDATE files SET generation = ? WHERE path = ?`, generation, path)
	return err
}

// Delete forgets a path.
func (i *Index) Delete(ctx context.Context, path string) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM files WHERE path = ?`, path)
	return err
}

// DeleteTree forgets a path and everything beneath it, used when a folder is
// removed or a root is dropped. Paths are matched in both slash forms because
// Windows indexes historically stored backslashes while the range upper-bound
// trick only works with '/'.
func (i *Index) DeleteTree(ctx context.Context, prefix string) error {
	slash := strings.Trim(filepath.ToSlash(filepath.Clean(prefix)), "/")
	if slash == "" || slash == "." {
		return nil
	}
	// Rebuild a native absolute prefix for legacy rows.
	native := filepath.FromSlash(slash)
	if filepath.IsAbs(prefix) || strings.Contains(prefix, `:\`) || strings.HasPrefix(prefix, `\\`) {
		native = filepath.Clean(prefix)
	}
	_, err := i.db.ExecContext(ctx, `
		DELETE FROM files WHERE path = ? OR path = ?
		 OR path LIKE ? ESCAPE '\'
		 OR path LIKE ? ESCAPE '\'`,
		slash, native,
		likePrefix(slash+"/"),
		likePrefix(native+string(filepath.Separator)),
	)
	return err
}

func likePrefix(prefix string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(prefix) + "%"
}

// PathsUnder lists indexed paths at or under prefix (slash-normalized match plus
// native separator match for older Windows rows).
func (i *Index) PathsUnder(ctx context.Context, prefix string) ([]string, error) {
	slash := strings.Trim(filepath.ToSlash(filepath.Clean(prefix)), "/")
	native := filepath.FromSlash(slash)
	if filepath.IsAbs(prefix) || strings.Contains(prefix, `:\`) || strings.HasPrefix(prefix, `\\`) {
		native = filepath.Clean(prefix)
	}
	rows, err := i.db.QueryContext(ctx, `
		SELECT path FROM files WHERE path = ? OR path = ?
		 OR path LIKE ? ESCAPE '\'
		 OR path LIKE ? ESCAPE '\'`,
		slash, native,
		likePrefix(slash+"/"),
		likePrefix(native+string(filepath.Separator)),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Stale returns paths that were not seen in the given scan generation, i.e. the
// files that have been deleted since the last backup.
func (i *Index) Stale(ctx context.Context, generation int64, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := i.db.QueryContext(ctx,
		`SELECT path FROM files WHERE generation < ? LIMIT ?`, generation, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Stats summarises the index for the status output.
type Stats struct {
	Files int64
	Bytes int64
}

// Stats returns the number of tracked files and their total size.
func (i *Index) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	err := i.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM files`).Scan(&s.Files, &s.Bytes)
	return s, err
}

// Meta keys used by the engine.
const (
	MetaLastSnapshotID   = "last_snapshot_id"
	MetaLastFullScan     = "last_full_scan"
	MetaChainLength      = "delta_chain_length"
	MetaGeneration       = "scan_generation"
	MetaLastSnapshotTime = "last_snapshot_time"
	MetaKeyID            = "key_id"
)

// Meta reads a stored value.
func (i *Index) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := i.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetMeta writes a stored value.
func (i *Index) SetMeta(ctx context.Context, key, value string) error {
	_, err := i.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// NextGeneration increments and returns the scan generation counter, which is how
// deletions are detected: everything seen during a scan is stamped with the
// current generation, and whatever still carries an older one is gone.
func (i *Index) NextGeneration(ctx context.Context) (int64, error) {
	current, err := i.Meta(ctx, MetaGeneration)
	if err != nil {
		return 0, err
	}
	var gen int64
	if current != "" {
		if _, err := fmt.Sscanf(current, "%d", &gen); err != nil {
			gen = 0
		}
	}
	gen++
	if err := i.SetMeta(ctx, MetaGeneration, fmt.Sprintf("%d", gen)); err != nil {
		return 0, err
	}
	return gen, nil
}

// Generation returns the current scan generation.
func (i *Index) Generation(ctx context.Context) (int64, error) {
	raw, err := i.Meta(ctx, MetaGeneration)
	if err != nil || raw == "" {
		return 0, err
	}
	var gen int64
	_, err = fmt.Sscanf(raw, "%d", &gen)
	return gen, err
}

// Reset clears the cache, forcing the next backup to re-read everything. It is
// the recovery path for a corrupted or mistrusted index.
func (i *Index) Reset(ctx context.Context) error {
	_, err := i.db.ExecContext(ctx, `DELETE FROM files; DELETE FROM meta`)
	return err
}
