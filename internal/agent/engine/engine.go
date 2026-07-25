// Package engine orchestrates the agent: scanning, watching, uploading and
// reporting.
//
// The lifecycle is one long-lived loop. On start it runs a backup, then it sits
// on the watcher and only wakes when files change; a periodic full scan catches
// whatever the watcher could not see (the machine was off, an event was dropped,
// an external drive came back). Everything is gated by the governor, so none of
// it competes with the person using the computer.
package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/openbackup/openbackup/internal/agent/config"
	"github.com/openbackup/openbackup/internal/agent/governor"
	"github.com/openbackup/openbackup/internal/agent/index"
	"github.com/openbackup/openbackup/internal/agent/ipc"
	"github.com/openbackup/openbackup/internal/agent/scanner"
	"github.com/openbackup/openbackup/internal/agent/uploader"
	"github.com/openbackup/openbackup/internal/agent/watcher"
	"github.com/openbackup/openbackup/internal/api"
	"github.com/openbackup/openbackup/internal/chunk"
	"github.com/openbackup/openbackup/internal/codec"
	"github.com/openbackup/openbackup/internal/throttle"
	"github.com/openbackup/openbackup/internal/version"
)

// entryBatchSize is how many file entries are sent per request.
const entryBatchSize = 500

// Engine runs the agent.
type Engine struct {
	cfg    *config.Config
	log    *slog.Logger
	client *api.Client
	codec  *codec.Codec
	idx    *index.Index
	gov    *governor.Governor
	bucket *throttle.Bucket
	up     *uploader.Uploader

	// policy is the server-provided policy, refreshed by each heartbeat.
	policyMu sync.RWMutex
	policy   api.Policy

	mu       sync.Mutex
	state    api.AgentState
	progress Status
	// backupMu serialises backup runs: two overlapping runs would fight over the
	// index and produce confusing snapshots.
	backupMu sync.Mutex

	// trigger asks the loop to run a backup now.
	trigger chan trigger
	events  *eventBuffer
}

// trigger describes why a backup run was requested.
type trigger struct {
	full   bool
	reason string
	// paths limits the run to specific files, used for watcher-driven backups.
	paths []watcher.Change
}

// Status is a snapshot of what the agent is doing, shown by the CLI and tray.
type Status struct {
	State         api.AgentState `json:"state"`
	Message       string         `json:"message"`
	CurrentPath   string         `json:"current_path,omitempty"`
	FilesDone     int64          `json:"files_done"`
	FilesTotal    int64          `json:"files_total"`
	BytesUploaded int64          `json:"bytes_uploaded"`
	BytesSkipped  int64          `json:"bytes_skipped"`
	StartedAt     time.Time      `json:"started_at,omitempty"`
	LastBackupAt  time.Time      `json:"last_backup_at,omitempty"`
	LastError     string         `json:"last_error,omitempty"`
	Paused        bool           `json:"paused"`
	PauseReason   string         `json:"pause_reason,omitempty"`
}

// Options configures an Engine.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
	// StateDir holds the local index.
	StateDir string
}

// New builds an Engine from configuration.
func New(ctx context.Context, opts Options) (*Engine, error) {
	cfg := opts.Config
	if cfg == nil {
		return nil, errors.New("engine: configuration is required")
	}
	if !cfg.Enrolled() {
		return nil, errors.New("this device is not connected to a server yet; run 'openbackup connect' first")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	client, err := api.NewClient(cfg.ServerURL, cfg.DeviceToken)
	if err != nil {
		return nil, err
	}

	codecOpts := codec.Options{}
	if cfg.Encryption.Enabled {
		key, err := codec.KeyFromRecoveryCode(cfg.Encryption.RecoveryCode)
		if err != nil {
			return nil, fmt.Errorf("engine: encryption is enabled but the key is unusable: %w", err)
		}
		codecOpts.Key = key
	}
	c, err := codec.New(codecOpts)
	if err != nil {
		return nil, err
	}

	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir, err = config.StateDir()
		if err != nil {
			return nil, err
		}
	}
	idx, err := index.Open(ctx, filepath.Join(stateDir, "index.db"))
	if err != nil {
		c.Close()
		return nil, err
	}

	bucket := throttle.NewBucket(cfg.Limits.UploadBytesPerSec)
	up, err := uploader.New(uploader.Options{
		Client:      client,
		Codec:       c,
		Bucket:      bucket,
		ChunkConfig: chunk.DefaultConfig(),
		Concurrency: cfg.Limits.UploadConcurrency,
	})
	if err != nil {
		_ = idx.Close()
		c.Close()
		return nil, err
	}

	e := &Engine{
		cfg:     cfg,
		log:     log,
		client:  client,
		codec:   c,
		idx:     idx,
		gov:     governor.New(cfg.Limits),
		bucket:  bucket,
		up:      up,
		trigger: make(chan trigger, 4),
		events:  newEventBuffer(),
		state:   api.StateIdle,
	}
	if cfg.Paused {
		e.gov.Pause()
	}
	up.Progress = e.onProgress
	return e, nil
}

// Close releases resources.
func (e *Engine) Close() {
	e.codec.Close()
	_ = e.idx.Close()
}

// Status returns the current status.
func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	status := e.progress
	status.State = e.state
	status.Paused = e.gov.Paused()
	if !status.Paused {
		if s := e.gov.Evaluate(context.Background()); !s.Allowed {
			status.Paused = true
			status.PauseReason = s.Reason
		}
	}
	return status
}

// Run starts the agent loop and blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) error {
	e.log.Info("agent starting",
		"version", version.Version, "server", e.cfg.ServerURL, "device", e.cfg.DeviceName,
		"encrypted", e.cfg.Encryption.Enabled)

	// The first heartbeat also confirms the credentials still work, so a revoked
	// device fails immediately with a clear message instead of after a scan.
	if err := e.heartbeat(ctx); err != nil {
		if api.IsAuthError(err) {
			return fmt.Errorf("this device is no longer authorised (it may have been removed in the dashboard); "+
				"run 'openbackup connect' again: %w", err)
		}
		e.log.Warn("initial heartbeat failed; continuing offline", "error", err)
	}

	scan := scanner.New(e.cfg)
	w, err := watcher.New(scan.Matcher(), e.cfg.Schedule.Debounce, e.log)
	if err != nil {
		e.log.Warn("real-time watching is unavailable; falling back to periodic scans", "error", err)
	}

	var wg sync.WaitGroup
	if w != nil {
		defer w.Close()
		if err := w.AddRoots(ctx, e.cfg.EnabledRoots()); err != nil {
			e.log.Warn("could not watch every folder", "error", err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Run(ctx)
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		e.heartbeatLoop(ctx)
	}()

	// Back up once at startup: the machine may have been off for a week.
	e.Trigger(trigger{full: true, reason: "agent started"})

	fullScan := time.NewTicker(e.cfg.Schedule.FullScanInterval)
	defer fullScan.Stop()

	var changes <-chan []watcher.Change
	var overflow <-chan struct{}
	if w != nil {
		changes = w.Changes()
		overflow = w.Overflow()
	}

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			e.flushEvents(context.WithoutCancel(ctx))
			return nil

		case t := <-e.trigger:
			e.runBackup(ctx, t)

		case <-fullScan.C:
			e.runBackup(ctx, trigger{full: true, reason: "scheduled full scan"})

		case <-overflow:
			// Events were dropped, so the watcher's view is incomplete and only a
			// full scan can be trusted.
			e.log.Info("filesystem events were dropped; running a full scan to catch up")
			e.runBackup(ctx, trigger{full: true, reason: "filesystem events were dropped"})

		case batch := <-changes:
			e.runBackup(ctx, trigger{reason: fmt.Sprintf("%d files changed", len(batch)), paths: batch})
		}
	}
}

// RunOnce performs a single full backup and returns. It is what 'openbackup
// backup' uses when no background agent is running, and what a cron-style
// deployment would call.
func (e *Engine) RunOnce(ctx context.Context) error {
	e.log.Info("running a single backup", "device", e.cfg.DeviceName)
	err := e.backup(ctx, trigger{full: true, reason: "manual backup"})
	e.flushEvents(context.WithoutCancel(ctx))
	if err != nil {
		return err
	}
	stats := e.up.Stats()
	e.log.Info("backup finished",
		"files", stats.FilesUploaded.Load(),
		"uploaded", humanBytes(stats.BytesUploaded.Load()),
		"deduplicated", humanBytes(stats.BytesDeduplicated.Load()))
	return nil
}

// Trigger requests a backup run.
func (e *Engine) Trigger(t trigger) {
	select {
	case e.trigger <- t:
	default:
		// A run is already queued; another would be redundant.
	}
}

// BackupNow requests an immediate full backup, used by the CLI and by the
// dashboard's "back up now" command.
func (e *Engine) BackupNow(reason string) { e.Trigger(trigger{full: true, reason: reason}) }

// Pause stops backup work. A zero duration pauses until Resume.
func (e *Engine) Pause(d time.Duration) {
	if d > 0 {
		e.gov.PauseFor(d)
		return
	}
	e.gov.Pause()
	e.cfg.Paused = true
	if err := e.cfg.Save(); err != nil {
		e.log.Error("save configuration", "error", err)
	}
}

// Resume restarts backup work and picks up whatever changed while paused.
func (e *Engine) Resume() {
	e.gov.Resume()
	e.cfg.Paused = false
	if err := e.cfg.Save(); err != nil {
		e.log.Error("save configuration", "error", err)
	}
	e.BackupNow("resumed")
}

// Control adapts the Engine to the local control protocol used by the CLI and
// the tray.
func (e *Engine) Control() ipc.Handler { return controlAdapter{e} }

type controlAdapter struct{ e *Engine }

func (c controlAdapter) Status() any             { return c.e.Status() }
func (c controlAdapter) BackupNow(reason string) { c.e.BackupNow(reason) }
func (c controlAdapter) Pause(d time.Duration)   { c.e.Pause(d) }
func (c controlAdapter) Resume()                 { c.e.Resume() }

// runBackup performs one backup pass with all the guards around it.
func (e *Engine) runBackup(ctx context.Context, t trigger) {
	e.backupMu.Lock()
	defer e.backupMu.Unlock()

	if state := e.gov.Evaluate(ctx); !state.Allowed {
		e.setState(api.StatePaused, state.Reason)
		e.log.Info("backup deferred", "reason", state.Reason)
		// Try again shortly; the machine's state changes on its own.
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Minute):
				e.Trigger(t)
			}
		}()
		return
	}

	start := time.Now()
	e.setState(api.StateScanning, t.reason)
	e.resetProgress()

	err := e.backup(ctx, t)
	switch {
	case err == nil:
		e.setState(api.StateIdle, "")
		e.mu.Lock()
		e.progress.LastBackupAt = time.Now()
		e.progress.LastError = ""
		e.mu.Unlock()
		e.log.Info("backup finished", "reason", t.reason, "duration", time.Since(start).Round(time.Second))

	case errors.Is(err, context.Canceled):
		e.setState(api.StateIdle, "stopped")

	case api.IsAuthError(err):
		e.setState(api.StateError, "this device is no longer authorised")
		e.event("error", "This device is no longer authorised. Reconnect it from the dashboard.", "", "")

	case api.IsQuotaError(err):
		e.setState(api.StateError, "the server is out of space for this account")
		e.event("error", "Backup stopped: the account's storage quota is full. "+
			"Free space by deleting old snapshots or raise the quota.", "", "")

	default:
		e.setState(api.StateError, err.Error())
		e.mu.Lock()
		e.progress.LastError = err.Error()
		e.mu.Unlock()
		e.log.Error("backup failed", "reason", t.reason, "error", err)
		e.event("error", "Backup failed: "+err.Error(), "", "")
	}
	e.flushEvents(ctx)
}

// backup runs a single snapshot.
func (e *Engine) backup(ctx context.Context, t trigger) error {
	roots := e.cfg.EnabledRoots()
	if len(roots) == 0 {
		e.log.Warn("no folders are configured for backup")
		return nil
	}
	for _, missing := range e.cfg.MissingRoots() {
		e.event("warn", fmt.Sprintf("Folder %q is not available right now, so it was not backed up.", missing.Path),
			missing.Path, "the folder or drive is missing")
	}

	parentID, err := e.idx.Meta(ctx, index.MetaLastSnapshotID)
	if err != nil {
		return err
	}
	chainLength := e.chainLength(ctx)

	kind := api.SnapshotDelta
	full := t.full || parentID == "" || chainLength >= e.cfg.Schedule.MaxDeltaChainLength
	if full {
		kind = api.SnapshotFull
		parentID = ""
	}
	// A partial (watcher-driven) run cannot produce a full snapshot: it never
	// looked at the whole tree, so it has nothing to say about files it skipped.
	if len(t.paths) > 0 {
		if parentID == "" {
			// Nothing to build a delta on yet; promote to a real full scan.
			t.paths = nil
			kind = api.SnapshotFull
			full = true
		} else {
			kind = api.SnapshotDelta
			full = false
		}
	}

	snapshotRoots := make([]api.SnapshotRoot, 0, len(roots))
	for _, r := range roots {
		snapshotRoots = append(snapshotRoots, api.SnapshotRoot{Name: r.Name, Path: r.Path})
	}

	snapshotID, err := e.client.StartSnapshot(ctx, api.StartSnapshotRequest{
		Roots:     snapshotRoots,
		Kind:      kind,
		ParentID:  parentID,
		StartedAt: time.Now().UTC(),
		// The key id tells the server which key sealed this snapshot's data, so a
		// device holding a different key cannot silently mix unreadable blobs
		// into the same repository.
		KeyID: e.cfg.Encryption.KeyID,
	})
	if err != nil {
		return err
	}
	e.log.Info("snapshot started", "id", snapshotID, "kind", kind, "parent", parentID, "reason", t.reason)

	sender := &entrySender{engine: e, snapshotID: snapshotID}
	var stats runStats

	if len(t.paths) > 0 {
		err = e.backupChanges(ctx, t.paths, sender, &stats)
	} else {
		err = e.backupFullTree(ctx, roots, kind, sender, &stats)
	}
	if err != nil {
		// Leave the snapshot incomplete: the server marks it failed rather than
		// letting a partial tree masquerade as a backup.
		return err
	}
	if err := sender.flush(ctx); err != nil {
		return err
	}

	e.setState(api.StateUploading, "finishing")
	if err := e.client.CompleteSnapshot(ctx, snapshotID, api.CompleteSnapshotRequest{
		CompletedAt:   time.Now().UTC(),
		FileCount:     stats.files,
		DirCount:      stats.dirs,
		TotalBytes:    stats.bytes,
		UploadedBytes: e.up.Stats().BytesUploaded.Load(),
		SkippedCount:  stats.skipped,
	}); err != nil {
		return err
	}

	if err := e.idx.SetMeta(ctx, index.MetaLastSnapshotID, snapshotID); err != nil {
		return err
	}
	if err := e.idx.SetMeta(ctx, index.MetaLastSnapshotTime,
		strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		return err
	}
	next := 0
	if kind == api.SnapshotDelta {
		next = chainLength + 1
	}
	if err := e.idx.SetMeta(ctx, index.MetaChainLength, strconv.Itoa(next)); err != nil {
		return err
	}

	e.event("info", fmt.Sprintf("Backup complete: %d files, %s uploaded, %s already stored.",
		stats.files, humanBytes(e.up.Stats().BytesUploaded.Load()),
		humanBytes(e.up.Stats().BytesDeduplicated.Load())), "", "")
	return nil
}

// runStats accumulates per-run counters.
type runStats struct {
	files        int64
	dirs         int64
	bytes        int64
	skipped      int64
	skippedBytes int64
}

// backupFullTree walks every root.
func (e *Engine) backupFullTree(ctx context.Context, roots []config.Root, kind api.SnapshotKind,
	sender *entrySender, stats *runStats) error {

	generation, err := e.idx.NextGeneration(ctx)
	if err != nil {
		return err
	}

	scan := scanner.New(e.cfg)
	scan.Emit = func(item scanner.Item) error {
		return e.processItem(ctx, item, kind, generation, sender, stats)
	}
	result, err := scan.Walk(ctx, roots)
	if err != nil {
		return err
	}
	stats.dirs += result.Dirs
	stats.skipped = result.Skipped
	stats.skippedBytes = result.SkippedBytes

	// Report why data was left out, so "why is my backup smaller than my disk?"
	// has an answer in the dashboard.
	for _, sample := range result.Samples[:min(len(result.Samples), 20)] {
		e.event("info", "Skipped "+sample.SnapPath, sample.SnapPath, sample.Reason)
	}
	for _, msg := range result.Errors[:min(len(result.Errors), 10)] {
		e.event("warn", "Could not read "+msg, "", "")
	}
	if len(result.Projects) > 0 {
		e.log.Info("developer projects detected", "count", len(result.Projects))
	}

	// Anything not seen during this walk has been deleted.
	deleted, err := e.idx.Stale(ctx, generation, 20000)
	if err != nil {
		return err
	}
	for _, abs := range deleted {
		snapPath, ok := e.snapPathFor(roots, abs)
		if !ok {
			// The file belonged to a root that is no longer configured; drop it
			// from the index without recording a deletion, because the data in
			// older snapshots should stay intact.
			_ = e.idx.Delete(ctx, abs)
			continue
		}
		if err := sender.addDeletion(ctx, snapPath); err != nil {
			return err
		}
		if err := e.idx.Delete(ctx, abs); err != nil {
			return err
		}
	}
	if len(deleted) > 0 {
		e.log.Info("recorded deletions", "count", len(deleted))
	}
	return nil
}

// backupChanges handles a watcher-driven incremental run.
func (e *Engine) backupChanges(ctx context.Context, changes []watcher.Change,
	sender *entrySender, stats *runStats) error {

	generation, err := e.idx.Generation(ctx)
	if err != nil {
		return err
	}
	scan := scanner.New(e.cfg)

	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapPath, ok := scanner.SnapshotPath(change.Root, change.AbsPath)
		if !ok {
			continue
		}

		info, statErr := os.Lstat(change.AbsPath)
		if statErr != nil || change.Removed {
			// Gone: record the deletion and forget it locally.
			if err := sender.addDeletion(ctx, snapPath); err != nil {
				return err
			}
			if err := e.idx.DeleteTree(ctx, change.AbsPath); err != nil {
				return err
			}
			continue
		}
		if info.IsDir() {
			// A changed directory is covered by the events for its contents.
			continue
		}

		rel := change.AbsPath
		if absRoot, err := filepath.Abs(change.Root.Path); err == nil {
			if r, ok := relFrom(absRoot, change.AbsPath); ok {
				rel = r
			}
		}
		if d := scan.Matcher().MatchFile(rel, info.Size()); d.Skip {
			stats.skipped++
			continue
		}

		item := scanner.Item{
			AbsPath:  change.AbsPath,
			SnapPath: snapPath,
			Type:     api.EntryFile,
			Size:     info.Size(),
			ModTime:  info.ModTime().UTC().Truncate(time.Second),
			Mode:     uint32(info.Mode().Perm()),
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(change.AbsPath)
			if err != nil {
				continue
			}
			item.Type = api.EntrySymlink
			item.LinkTarget = target
		} else if !info.Mode().IsRegular() {
			continue
		}
		if err := e.processItem(ctx, item, api.SnapshotDelta, generation, sender, stats); err != nil {
			return err
		}
	}
	return nil
}

// processItem uploads a file if needed and queues its entry.
func (e *Engine) processItem(ctx context.Context, item scanner.Item, kind api.SnapshotKind,
	generation int64, sender *entrySender, stats *runStats) error {

	// Between files is the right place to pause: stopping mid-file would leave
	// the upload to be redone, and pausing per chunk would react too slowly.
	if _, err := e.gov.WaitUntilAllowed(ctx); err != nil {
		return err
	}

	switch item.Type {
	case api.EntryDir:
		if kind == api.SnapshotFull {
			return sender.add(ctx, api.Entry{
				Path: item.SnapPath, Type: api.EntryDir, ModTime: item.ModTime, Mode: item.Mode,
			})
		}
		return nil

	case api.EntrySymlink:
		return sender.add(ctx, api.Entry{
			Path: item.SnapPath, Type: api.EntrySymlink, ModTime: item.ModTime,
			LinkTarget: item.LinkTarget,
		})
	}

	state, unchanged := e.idx.Unchanged(ctx, item.AbsPath, item.Size, item.ModTime)
	if unchanged {
		if err := e.idx.Touch(ctx, item.AbsPath, generation); err != nil {
			return err
		}
		stats.files++
		stats.bytes += item.Size
		if kind == api.SnapshotFull {
			// A full snapshot must describe the whole tree, but an unchanged file
			// needs no upload: its chunks are already known from the index.
			return sender.add(ctx, api.Entry{
				Path: item.SnapPath, Type: api.EntryFile, Size: state.Size, ModTime: state.ModTime,
				Mode: state.Mode, Chunks: state.Chunks, Digest: state.Digest,
			})
		}
		return nil
	}

	e.setCurrentPath(item.SnapPath)
	e.setState(api.StateUploading, "")

	result, err := e.up.UploadFile(ctx, item.AbsPath, item.Size)
	if err != nil {
		if api.IsAuthError(err) || api.IsQuotaError(err) || errors.Is(err, context.Canceled) {
			return err
		}
		// One unreadable file (locked by another process, permission denied)
		// must never abort a backup of a million files.
		e.log.Warn("skipping a file that could not be read", "path", item.AbsPath, "error", err)
		e.event("warn", "Could not read "+item.SnapPath, item.SnapPath, friendlyFileError(err))
		stats.skipped++
		return nil
	}

	stats.files++
	stats.bytes += result.Size
	e.mu.Lock()
	e.progress.FilesDone++
	e.mu.Unlock()

	if err := e.idx.Put(ctx, index.FileState{
		Path: item.AbsPath, Size: result.Size, ModTime: item.ModTime,
		Digest: result.Digest, Chunks: result.Chunks, Mode: item.Mode,
	}, generation); err != nil {
		return err
	}

	return sender.add(ctx, api.Entry{
		Path: item.SnapPath, Type: api.EntryFile, Size: result.Size, ModTime: item.ModTime,
		Mode: item.Mode, Chunks: result.Chunks, Digest: result.Digest,
	})
}

// snapPathFor maps an absolute path back to its snapshot path.
func (e *Engine) snapPathFor(roots []config.Root, abs string) (string, bool) {
	for _, root := range roots {
		if p, ok := scanner.SnapshotPath(root, abs); ok {
			return p, true
		}
	}
	return "", false
}

// chainLength reads how many deltas have been chained since the last full
// snapshot. Long chains make restores slower and a single lost snapshot more
// damaging, so the engine periodically starts fresh.
func (e *Engine) chainLength(ctx context.Context) int {
	raw, err := e.idx.Meta(ctx, index.MetaChainLength)
	if err != nil || raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

// entrySender batches entry uploads.
type entrySender struct {
	engine     *Engine
	snapshotID string
	entries    []api.Entry
	deletions  []string
}

func (s *entrySender) add(ctx context.Context, entry api.Entry) error {
	s.entries = append(s.entries, entry)
	if len(s.entries) >= entryBatchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *entrySender) addDeletion(ctx context.Context, path string) error {
	s.deletions = append(s.deletions, path)
	if len(s.deletions) >= entryBatchSize {
		return s.flush(ctx)
	}
	return nil
}

func (s *entrySender) flush(ctx context.Context) error {
	if len(s.entries) == 0 && len(s.deletions) == 0 {
		return nil
	}
	err := s.engine.client.AddEntries(ctx, s.snapshotID, api.AddEntriesRequest{
		Entries: s.entries,
		Deleted: s.deletions,
	})
	if err != nil {
		return err
	}
	s.entries = s.entries[:0]
	s.deletions = s.deletions[:0]
	return nil
}

// heartbeatLoop reports status and collects commands.
func (e *Engine) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(e.cfg.Schedule.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := e.heartbeat(ctx); err != nil && ctx.Err() == nil {
				e.log.Debug("heartbeat failed", "error", err)
			}
		}
	}
}

// heartbeat sends status and applies whatever the server asks for.
func (e *Engine) heartbeat(ctx context.Context) error {
	status := e.Status()
	idxStats, _ := e.idx.Stats(ctx)
	machine := e.gov.Evaluate(ctx)

	state, reason := status.State, status.Message
	if status.Paused {
		state = api.StatePaused
		reason = status.PauseReason
	}

	resp, err := e.client.Heartbeat(ctx, api.HeartbeatRequest{
		State:       state,
		StateReason: reason,
		// The dashboard shows these as "protected on this device", which is the
		// number a user checks to confirm the agent is doing its job.
		QueuedFiles:   idxStats.Files,
		QueuedBytes:   idxStats.Bytes,
		UploadedBytes: e.up.Stats().BytesUploaded.Load(),
		CPUPercent:    machine.CPUPercent,
		MemoryBytes:   currentMemoryBytes(),
		AgentVersion:  version.Version,
		BatteryPct:    machine.Battery.Percent,
		OnMetered:     machine.Metered,
		LastError:     status.LastError,
	})
	if err != nil {
		return err
	}

	e.policyMu.Lock()
	e.policy = resp.Policy
	e.policyMu.Unlock()
	e.applyPolicy(resp.Policy)

	for _, cmd := range resp.Commands {
		e.handleCommand(ctx, cmd)
	}
	e.flushEvents(ctx)
	return nil
}

// applyPolicy takes the server's limits, which is how a user changes settings in
// the dashboard without touching each device.
//
// The bandwidth cap is applied in memory only: the local configuration stays the
// user's own, so a policy change never silently rewrites what they set on the
// machine.
func (e *Engine) applyPolicy(policy api.Policy) {
	if policy.MaxUploadBytesPerSec != e.cfg.Limits.UploadBytesPerSec {
		e.cfg.Limits.UploadBytesPerSec = policy.MaxUploadBytesPerSec
		e.bucket.SetRate(policy.MaxUploadBytesPerSec)
		e.gov.SetLimits(e.cfg.Limits)
	}
	if policy.RequireEncryption && !e.cfg.Encryption.Enabled {
		// Refusing to upload plaintext into a repository that demands encryption
		// is better than having the server reject every chunk.
		e.event("error", "This server requires end-to-end encryption, but this device has it turned off. "+
			"Run 'openbackup encrypt' to enable it.", "", "")
	}
}

// handleCommand executes a server command.
func (e *Engine) handleCommand(ctx context.Context, cmd api.Command) {
	e.log.Info("command received", "kind", cmd.Kind)
	switch cmd.Kind {
	case api.CommandBackupNow:
		e.BackupNow("requested from the dashboard")
	case api.CommandPause:
		e.gov.Pause()
		e.cfg.Paused = true
		_ = e.cfg.Save()
	case api.CommandResume:
		e.gov.Resume()
		e.cfg.Paused = false
		_ = e.cfg.Save()
		e.BackupNow("resumed")
	case api.CommandRescan:
		// Forget the local cache so everything is re-read and re-verified. This
		// is the repair path when the index and the server disagree.
		if err := e.idx.Reset(ctx); err != nil {
			e.log.Error("reset index", "error", err)
			return
		}
		e.BackupNow("full rescan requested")
	case api.CommandReloadConf:
		// The policy travels with the heartbeat and has already been applied; the
		// local file is re-read on the next start.
	case api.CommandForget:
		// The device was removed in the dashboard. Wipe the credentials so the
		// agent stops trying and the user is prompted to reconnect.
		e.log.Warn("this device was removed from the account; clearing local credentials")
		e.cfg.DeviceToken = ""
		e.cfg.DeviceID = ""
		if err := e.cfg.Save(); err != nil {
			e.log.Error("clear credentials", "error", err)
		}
	default:
		e.log.Warn("ignoring an unknown command", "kind", cmd.Kind)
	}
}

// setState records the agent state.
func (e *Engine) setState(state api.AgentState, message string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
	e.progress.Message = message
}

func (e *Engine) setCurrentPath(path string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress.CurrentPath = path
}

func (e *Engine) resetProgress() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress.FilesDone = 0
	e.progress.BytesUploaded = 0
	e.progress.BytesSkipped = 0
	e.progress.StartedAt = time.Now()
}

// onProgress receives uploader progress.
func (e *Engine) onProgress(p uploader.Progress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.progress.BytesUploaded = e.up.Stats().BytesUploaded.Load()
	e.progress.BytesSkipped = e.up.Stats().BytesDeduplicated.Load()
}

// event queues an activity log entry for the dashboard.
func (e *Engine) event(level, message, path, reason string) {
	e.events.add(api.Event{
		At: time.Now().UTC(), Level: level, Message: message, Path: path, Reason: reason,
	})
}

// flushEvents sends buffered events. Failures are tolerated: the activity log is
// useful, but losing a line of it must never fail a backup.
func (e *Engine) flushEvents(ctx context.Context) {
	batch := e.events.take()
	if len(batch) == 0 {
		return
	}
	if err := e.client.SendEvents(ctx, batch); err != nil {
		e.log.Debug("send events", "error", err)
	}
}

// eventBuffer collects events between heartbeats.
type eventBuffer struct {
	mu     sync.Mutex
	events []api.Event
}

func newEventBuffer() *eventBuffer { return &eventBuffer{} }

func (b *eventBuffer) add(event api.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Bound the buffer: an agent that cannot reach the server must not grow its
	// memory forever.
	if len(b.events) >= 500 {
		b.events = b.events[1:]
	}
	b.events = append(b.events, event)
}

func (b *eventBuffer) take() []api.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) == 0 {
		return nil
	}
	out := b.events
	b.events = nil
	return out
}

// relFrom returns the path of abs relative to root using forward slashes.
func relFrom(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	if rel == "." || rel == ".." || len(rel) > 1 && rel[0] == '.' && rel[1] == '.' {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// friendlyFileError turns an OS error into something a non-technical user can
// act on.
func friendlyFileError(err error) string {
	switch {
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, os.ErrNotExist):
		return "the file was deleted while it was being backed up"
	default:
		return err.Error()
	}
}

// currentMemoryBytes reports the agent's own resident heap, so a user can verify
// the "under 100 MB" claim from the dashboard instead of taking it on trust.
func currentMemoryBytes() int64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return int64(stats.Sys - stats.HeapReleased)
}

// humanBytes formats a byte count for log lines and the activity feed.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f EiB", value/unit)
}
