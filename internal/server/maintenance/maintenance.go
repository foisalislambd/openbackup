// Package maintenance runs the server's background housekeeping: retention,
// garbage collection, and integrity checks.
package maintenance

import (
	"context"
	"log/slog"
	"time"

	"github.com/foisalislambd/openbackup/internal/server/store"
)

// staleSnapshotAge is how long a snapshot may stay "running" without any
// progress before it is assumed the agent died. It measures inactivity (the
// last entry batch), not total runtime, so a first full backup of a large disk
// on a slow link is never killed while it is steadily uploading.
const staleSnapshotAge = 6 * time.Hour

// eventRetention bounds the activity log.
const (
	eventRetention = 30 * 24 * time.Hour
	maxEventRows   = 200_000
)

// gcBatchSize bounds how many unreferenced chunks are deleted per pass, so a
// large cleanup never blocks uploads for long.
const gcBatchSize = 5000

// Runner performs maintenance passes.
type Runner struct {
	db    *store.DB
	blobs store.Blobs
	log   *slog.Logger
}

// New builds a Runner.
func New(db *store.DB, blobs store.Blobs, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{db: db, blobs: blobs, log: log}
}

// Report summarises what a pass did.
type Report struct {
	StaleSnapshots    int64         `json:"stale_snapshots"`
	ExpiredSnapshots  int           `json:"expired_snapshots"`
	DeletedChunks     int           `json:"deleted_chunks"`
	FreedBytes        int64         `json:"freed_bytes"`
	PrunedEvents      int64         `json:"pruned_events"`
	ExpiredSessions   int64         `json:"expired_sessions"`
	ExpiredJoinTokens int64         `json:"expired_join_tokens"`
	CheckedChunks     int           `json:"checked_chunks"`
	FailedChunks      int           `json:"failed_chunks"`
	Duration          time.Duration `json:"duration"`
}

// samplePerPass is how many blobs an automatic pass reads. A full walk stays
// on `openbackup-server check`; this is the ongoing spot check.
const samplePerPass = 24

// Run executes one maintenance pass, including the integrity sample.
func (r *Runner) Run(ctx context.Context) (Report, error) {
	return r.run(ctx, true)
}

// vacuumMinFree is how many reclaimed bytes justify rewriting the database.
// A handful of deleted rows is cheaper to leave for SQLite than to vacuum.
const vacuumMinFree int64 = 64 << 20

func (r *Runner) run(ctx context.Context, verify bool) (Report, error) {
	start := time.Now()
	var report Report

	if n, err := r.db.FailStaleSnapshots(ctx, staleSnapshotAge); err != nil {
		return report, err
	} else {
		report.StaleSnapshots = n
	}

	expired, err := r.applyRetention(ctx)
	if err != nil {
		return report, err
	}
	report.ExpiredSnapshots = expired

	deleted, freed, err := r.collectGarbage(ctx)
	if err != nil {
		return report, err
	}
	report.DeletedChunks = deleted
	report.FreedBytes = freed

	if n, err := r.db.PruneEvents(ctx, eventRetention, maxEventRows); err == nil {
		report.PrunedEvents = n
	} else {
		r.log.Warn("prune events", "error", err)
	}
	if n, err := r.db.DeleteExpiredSessions(ctx); err == nil {
		report.ExpiredSessions = n
	} else {
		r.log.Warn("delete expired sessions", "error", err)
	}
	if n, err := r.db.DeleteExpiredJoinTokens(ctx); err == nil {
		report.ExpiredJoinTokens = n
	} else {
		r.log.Warn("delete expired join tokens", "error", err)
	}
	if verify {
		if run, err := r.VerifySample(ctx, samplePerPass); err == nil {
			report.CheckedChunks = run.Checked
			report.FailedChunks = run.Failed
		} else if ctx.Err() == nil {
			r.log.Warn("integrity sample", "error", err)
		}
	}
	if err := r.reconcileAlerts(ctx); err != nil && ctx.Err() == nil {
		r.log.Warn("reconcile alerts", "error", err)
	}
	if report.FreedBytes >= vacuumMinFree {
		if err := r.db.Vacuum(ctx); err != nil {
			r.log.Debug("vacuum", "error", err)
		}
	}

	report.Duration = time.Since(start)
	return report, nil
}

// Start runs a pass immediately and then on every tick until ctx is cancelled.
func (r *Runner) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Hour
	}
	run := func(verify bool) {
		report, err := r.run(ctx, verify)
		if err != nil {
			if ctx.Err() == nil {
				r.log.Error("maintenance pass failed", "error", err)
			}
			return
		}
		if report.ExpiredSnapshots > 0 || report.DeletedChunks > 0 || report.StaleSnapshots > 0 {
			r.log.Info("maintenance pass",
				"expired_snapshots", report.ExpiredSnapshots,
				"deleted_chunks", report.DeletedChunks,
				"freed_bytes", report.FreedBytes,
				"stale_snapshots", report.StaleSnapshots,
				"duration", report.Duration)
		}
	}
	// The process just started; reading sample blobs immediately competes with
	// the first backup. The hourly pass still verifies.
	run(false)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run(true)
		}
	}
}

// applyRetention deletes snapshots past their retention window.
//
// The rules exist to make "delete old backups" safe rather than merely correct:
//
//   - a pinned snapshot is never deleted;
//   - the newest snapshot of every device is never deleted, so a machine that
//     has been off for a year still has something to restore from;
//   - a snapshot that a kept snapshot depends on is never deleted, because a
//     delta without its base full snapshot cannot be restored;
//   - when the account sets daily, weekly or monthly counts, only those
//     buckets are kept inside the window. Counts of zero keep every snapshot
//     in the window, which is the historical behaviour.
func (r *Runner) applyRetention(ctx context.Context) (int, error) {
	rows, err := r.db.SnapshotsForRetention(ctx)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	now := time.Now().UTC()
	keep := planRetention(rows, now)

	var deleted int
	deletedIDs := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, kept := keep[row.ID]; kept {
			continue
		}
		// An earlier cascade already removed this row together with its
		// descendants; counting it again would inflate the report.
		if _, done := deletedIDs[row.ID]; done {
			continue
		}
		if ctx.Err() != nil {
			return deleted, ctx.Err()
		}
		n, err := r.db.DeleteSnapshotCascade(ctx, row.ID)
		if err != nil {
			r.log.Error("delete expired snapshot", "snapshot", row.ID, "error", err)
			continue
		}
		deleted += n
		deletedIDs[row.ID] = struct{}{}
		r.log.Debug("expired snapshot deleted",
			"snapshot", row.ID, "device", row.DeviceID, "age_days", int(now.Sub(row.StartedAt).Hours()/24))
	}
	return deleted, nil
}

// collectGarbage deletes blobs that no snapshot references any more.
//
// Order matters: the blob goes first, then the metadata row. A crash in between
// leaves a row whose blob is missing, which the next pass sees as unreferenced
// and cleans up. The reverse order would leave an unreachable blob on disk
// forever, since nothing would remember it exists.
func (r *Runner) collectGarbage(ctx context.Context) (deleted int, freed int64, err error) {
	for {
		chunks, err := r.db.UnreferencedChunks(ctx, gcBatchSize)
		if err != nil {
			return deleted, freed, err
		}
		if len(chunks) == 0 {
			return deleted, freed, nil
		}
		passDeleted := 0
		removed := make([]string, 0, len(chunks))
		freedInPass := make(map[string]int64, len(chunks))
		for _, chunk := range chunks {
			if ctx.Err() != nil {
				return deleted, freed, ctx.Err()
			}
			if err := r.blobs.Delete(ctx, chunk.Digest); err != nil {
				r.log.Error("delete blob", "digest", chunk.Digest, "error", err)
				continue
			}
			removed = append(removed, chunk.Digest)
			freedInPass[chunk.Digest] = chunk.StoredBytes
		}
		if err := r.db.DeleteChunkRows(ctx, removed); err != nil {
			r.log.Error("delete chunk rows", "error", err)
			for _, digest := range removed {
				if err := r.db.DeleteChunkRow(ctx, digest); err != nil {
					r.log.Error("delete chunk row", "digest", digest, "error", err)
					continue
				}
				passDeleted++
				deleted++
				freed += freedInPass[digest]
			}
		} else {
			for _, digest := range removed {
				passDeleted++
				deleted++
				freed += freedInPass[digest]
			}
		}
		if passDeleted == 0 && len(chunks) >= gcBatchSize {
			// A full batch that made zero progress would be refetched forever;
			// stop and let the operator fix the underlying storage problem.
			r.log.Warn("garbage collection made no progress; stopping this pass",
				"blocked_chunks", len(chunks))
			return deleted, freed, nil
		}
		if len(chunks) < gcBatchSize {
			return deleted, freed, nil
		}
	}
}

// CheckResult reports on repository integrity.
type CheckResult struct {
	// MissingBlobs are chunks the database knows about but storage does not.
	// Any non-zero value means some restores would fail.
	MissingBlobs []string `json:"missing_blobs"`
	// OrphanBlobs are stored objects with no metadata row, usually the result of
	// a crash between the two writes. They are safe to delete.
	OrphanBlobs []string `json:"orphan_blobs"`
	// UnrestorableSnapshots reference at least one missing chunk.
	UnrestorableSnapshots []string      `json:"unrestorable_snapshots"`
	ScannedBlobs          int64         `json:"scanned_blobs"`
	Duration              time.Duration `json:"duration"`
}

// Check verifies that metadata and storage agree.
//
// A backup system that has never been verified is a hope, not a backup, so this
// runs from the CLI ('openbackup-server check') and can be scheduled.
func (r *Runner) Check(ctx context.Context, deleteOrphans bool) (CheckResult, error) {
	start := time.Now()
	var result CheckResult

	// Storage to database: find objects nothing references.
	err := r.blobs.Walk(ctx, func(digest string, size int64) error {
		result.ScannedBlobs++
		known, err := r.db.IsChunkKnown(ctx, digest)
		if err != nil {
			return err
		}
		if !known {
			if len(result.OrphanBlobs) < 1000 {
				result.OrphanBlobs = append(result.OrphanBlobs, digest)
			}
			if deleteOrphans {
				if err := r.blobs.Delete(ctx, digest); err != nil {
					r.log.Error("delete orphan blob", "digest", digest, "error", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	// Database to storage: find chunks whose data is gone, and the snapshots
	// that would fail to restore because of them.
	snapshots, err := r.db.SnapshotsForRetention(ctx)
	if err != nil {
		return result, err
	}
	seen := make(map[string]struct{}, len(snapshots))
	for _, snap := range snapshots {
		missing, err := r.db.MissingSnapshotChunks(ctx, snap.ID, 5)
		if err != nil {
			return result, err
		}
		if len(missing) == 0 {
			continue
		}
		result.UnrestorableSnapshots = append(result.UnrestorableSnapshots, snap.ID)
		for _, digest := range missing {
			if _, dup := seen[digest]; dup {
				continue
			}
			seen[digest] = struct{}{}
			if len(result.MissingBlobs) < 1000 {
				result.MissingBlobs = append(result.MissingBlobs, digest)
			}
		}
	}

	result.Duration = time.Since(start)
	return result, nil
}
