// Package control is the agent's operations layer: everything a user interface
// needs to ask of the agent, in one place.
//
// The command line and the desktop app are both thin shells over this package.
// That matters because the two must never disagree: if "add this folder" behaves
// differently in the window than in the terminal, one of them is wrong, and a
// backup tool cannot afford a UI that lies about what it is protecting.
//
// The daemon is the only process that backs up. This package therefore does one
// of three things depending on the operation:
//
//   - Ask the running daemon over the local control channel (status, pause,
//     back up now), because only it knows what is happening right now.
//   - Edit the on-disk configuration and tell the daemon to reload (folders,
//     limits), so a change applies without restarting anything.
//   - Talk to the server directly with this device's token (list backups,
//     browse, restore), because those need no coordination with the daemon and
//     work even when it is not running — which is exactly the situation someone
//     restoring a broken machine is in.
package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/agent/engine"
	"github.com/foisalislambd/openbackup/internal/agent/index"
	"github.com/foisalislambd/openbackup/internal/agent/ipc"
	"github.com/foisalislambd/openbackup/internal/agent/restore"
	"github.com/foisalislambd/openbackup/internal/api"
	"github.com/foisalislambd/openbackup/internal/codec"
	"github.com/foisalislambd/openbackup/internal/userdirs"
	"github.com/foisalislambd/openbackup/internal/version"
)

// ErrNotConnected means this device has not been enrolled yet.
var ErrNotConnected = errors.New("this device is not connected to a server yet")

// Agent is the operations layer over one agent installation.
type Agent struct {
	cfg      *config.Config
	stateDir string
}

// Open loads the agent's configuration. An empty path uses the default location.
// It succeeds even when the device is not enrolled, because the first thing a
// user interface has to do is show the connect screen.
func Open(configPath string) (*Agent, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	stateDir, err := config.StateDir()
	if err != nil {
		return nil, err
	}
	return &Agent{cfg: cfg, stateDir: stateDir}, nil
}

// Config exposes the loaded configuration.
func (a *Agent) Config() *config.Config { return a.cfg }

// Reload re-reads the configuration from disk, so a UI picks up a change made by
// the command line or by the daemon.
func (a *Agent) Reload() error {
	cfg, err := config.Load(a.cfg.Path())
	if err != nil {
		return err
	}
	a.cfg = cfg
	return nil
}

// Connected reports whether this device is enrolled.
func (a *Agent) Connected() bool { return a.cfg.Enrolled() }

// client builds an authenticated server client for this device.
func (a *Agent) client() (*api.Client, error) {
	if !a.cfg.Enrolled() {
		return nil, ErrNotConnected
	}
	return api.NewClient(a.cfg.ServerURL, a.cfg.DeviceToken)
}

// daemon dials the running agent, or reports ipc.ErrNotRunning.
func (a *Agent) daemon() (*ipc.Client, error) { return ipc.Dial(a.stateDir) }

// -----------------------------------------------------------------------------
// Status
// -----------------------------------------------------------------------------

// Overview is everything a home screen needs, in one call.
//
// It is deliberately forgiving: a UI must be able to render something honest
// when the daemon is stopped, when the server is unreachable, or both. Failures
// are reported as fields rather than as an error, so partial information still
// reaches the screen.
type Overview struct {
	Connected  bool   `json:"connected"`
	ServerURL  string `json:"server_url"`
	DeviceName string `json:"device_name"`
	DeviceID   string `json:"device_id"`
	Version    string `json:"version"`
	Platform   string `json:"platform"`

	// AgentRunning is false when the background service is not running, which is
	// the single most common reason for backups silently stopping.
	AgentRunning bool `json:"agent_running"`
	// Health is one of protected, working, paused, stale, error, never_run,
	// not_connected, agent_stopped. A UI can switch on it directly.
	Health string `json:"health"`
	// Headline and Detail are plain-language summaries of Health.
	Headline string `json:"headline"`
	Detail   string `json:"detail"`

	State       string `json:"state"`
	Paused      bool   `json:"paused"`
	PauseReason string `json:"pause_reason,omitempty"`
	CurrentPath string `json:"current_path,omitempty"`
	FilesDone   int64  `json:"files_done"`
	FilesTotal  int64  `json:"files_total"`
	BytesDone   int64  `json:"bytes_done"`
	LastError   string `json:"last_error,omitempty"`

	// TrackedFiles and TrackedBytes come from the local index, so they describe
	// this machine even with the server offline.
	TrackedFiles int64 `json:"tracked_files"`
	TrackedBytes int64 `json:"tracked_bytes"`

	LastBackupAt   *time.Time `json:"last_backup_at,omitempty"`
	LastBackupSize int64      `json:"last_backup_size"`
	LastBackupFile int64      `json:"last_backup_files"`
	SnapshotCount  int        `json:"snapshot_count"`
	ServerError    string     `json:"server_error,omitempty"`

	Encrypted    bool `json:"encrypted"`
	FolderCount  int  `json:"folder_count"`
	MissingCount int  `json:"missing_folders"`
}

// Overview gathers the local and server views of this device.
func (a *Agent) Overview(ctx context.Context) Overview {
	o := Overview{
		Connected:  a.cfg.Enrolled(),
		ServerURL:  a.cfg.ServerURL,
		DeviceName: a.cfg.DeviceName,
		DeviceID:   a.cfg.DeviceID,
		Version:    version.Version,
		Platform:   runtime.GOOS,
		Paused:     a.cfg.Paused,
		Encrypted:  a.cfg.Encryption.Enabled,
	}
	o.FolderCount = len(a.cfg.EnabledRoots())
	o.MissingCount = len(a.cfg.MissingRoots())

	if !o.Connected {
		o.Health = "not_connected"
		o.Headline = "Not connected yet"
		o.Detail = "Enter the connection code from your dashboard to start backing up."
		return o
	}

	// Local view: what is the daemon doing right now?
	if client, err := a.daemon(); err == nil {
		var st engine.Status
		if err := client.Status(ctx, &st); err == nil {
			o.AgentRunning = true
			o.State = string(st.State)
			o.CurrentPath = st.CurrentPath
			o.FilesDone = st.FilesDone
			o.FilesTotal = st.FilesTotal
			o.BytesDone = st.BytesUploaded
			o.LastError = st.LastError
			o.Paused = st.Paused
			o.PauseReason = st.PauseReason
			if !st.LastBackupAt.IsZero() {
				at := st.LastBackupAt
				o.LastBackupAt = &at
			}
		}
	}

	// The index is this machine's own record, readable whether or not the daemon
	// is up, so the folder totals survive a stopped service.
	if stats, err := a.indexStats(ctx); err == nil {
		o.TrackedFiles = stats.Files
		o.TrackedBytes = stats.Bytes
	}

	// Server view: what actually arrived? This is the only trustworthy answer to
	// "am I backed up", since a local agent can believe it succeeded and be wrong.
	if client, err := a.client(); err == nil {
		snapshots, err := client.ListSnapshots(ctx)
		switch {
		case err != nil:
			o.ServerError = err.Error()
		default:
			o.SnapshotCount = len(snapshots)
			if newest := newestComplete(snapshots); newest != nil {
				at := newest.StartedAt
				o.LastBackupAt = &at
				o.LastBackupSize = newest.TotalBytes
				o.LastBackupFile = newest.FileCount
			}
		}
	}

	o.Health, o.Headline, o.Detail = describe(o)
	return o
}

// describe turns the raw state into the sentence a worried person wants to read.
// Ordering is by severity: a stopped agent matters more than a stale backup,
// which matters more than an error from a run that later succeeded.
func describe(o Overview) (health, headline, detail string) {
	switch {
	case !o.AgentRunning:
		return "agent_stopped", "Backups are not running",
			"The OpenBackup background service is not running, so nothing is being backed up. Start it to continue."
	case o.Paused:
		reason := o.PauseReason
		if reason == "" {
			reason = "Backups are paused until you resume them."
		}
		return "paused", "Paused", reason
	case o.LastError != "":
		return "error", "The last backup did not finish", o.LastError
	case o.State == string(api.StateUploading) || o.State == string(api.StateScanning):
		detail := "Checking your folders for changes."
		if o.State == string(api.StateUploading) {
			detail = fmt.Sprintf("Uploading %s so far.", humanBytes(o.BytesDone))
			if o.FilesTotal > 0 {
				detail = fmt.Sprintf("File %d of %d, %s uploaded.", o.FilesDone, o.FilesTotal, humanBytes(o.BytesDone))
			}
		}
		return "working", "Backing up now", detail
	case o.LastBackupAt == nil:
		return "never_run", "No backup yet",
			"The first backup has not finished. It can take a while, and it will appear here when it does."
	case time.Since(*o.LastBackupAt) > 48*time.Hour:
		return "stale", "Your backup is out of date",
			fmt.Sprintf("The last successful backup was %s ago.", roughly(time.Since(*o.LastBackupAt)))
	default:
		return "protected", "Everything is backed up",
			fmt.Sprintf("Last backup %s ago, %s across %s files.",
				roughly(time.Since(*o.LastBackupAt)), humanBytes(o.LastBackupSize), thousands(o.LastBackupFile))
	}
}

func (a *Agent) indexStats(ctx context.Context) (index.Stats, error) {
	// Opening the index read-only alongside a running daemon is safe: SQLite is
	// in WAL mode, so a reader never blocks the writer.
	ix, err := index.Open(ctx, indexPath(a.stateDir))
	if err != nil {
		return index.Stats{}, err
	}
	defer ix.Close()
	return ix.Stats(ctx)
}

func newestComplete(snapshots []api.Snapshot) *api.Snapshot {
	var newest *api.Snapshot
	for i := range snapshots {
		if snapshots[i].Status != api.SnapshotStatusComplete {
			continue
		}
		if newest == nil || snapshots[i].StartedAt.After(newest.StartedAt) {
			newest = &snapshots[i]
		}
	}
	return newest
}

// -----------------------------------------------------------------------------
// Enrolment
// -----------------------------------------------------------------------------

// ConnectRequest is what the connect screen collects.
type ConnectRequest struct {
	ServerURL  string `json:"server_url"`
	Code       string `json:"code"`
	DeviceName string `json:"device_name,omitempty"`
	// Encrypt turns on end-to-end encryption. It can only be decided here,
	// before the first backup, because switching later would leave earlier
	// backups readable and misrepresent what is protected.
	Encrypt bool `json:"encrypt"`
	// RecoveryCode joins an account that is already encrypted. Every device in an
	// account must hold the same key, or each one could only read its own
	// backups. Supplying a code implies Encrypt.
	RecoveryCode string `json:"recovery_code,omitempty"`
}

// ConnectResult reports what happened, including the recovery code the user must
// write down. It is returned exactly once, at enrolment.
type ConnectResult struct {
	DeviceName   string   `json:"device_name"`
	ServerURL    string   `json:"server_url"`
	Folders      []Folder `json:"folders"`
	RecoveryCode string   `json:"recovery_code,omitempty"`
}

// Connect enrols this device.
func (a *Agent) Connect(ctx context.Context, req ConnectRequest) (*ConnectResult, error) {
	if a.cfg.Enrolled() {
		return nil, fmt.Errorf("this device is already connected to %s; remove it in the dashboard first",
			a.cfg.ServerURL)
	}
	req.ServerURL = strings.TrimSpace(req.ServerURL)
	req.Code = strings.TrimSpace(req.Code)
	if req.ServerURL == "" || req.Code == "" {
		return nil, errors.New("both the server address and the connection code are required")
	}

	client, err := api.NewClient(req.ServerURL, "")
	if err != nil {
		return nil, err
	}
	// Check the server first, so a mistyped address fails with "cannot reach
	// that address" instead of "enrolment failed".
	if err := client.Health(ctx); err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", req.ServerURL, err)
	}

	name := strings.TrimSpace(req.DeviceName)
	if name == "" {
		name = config.DefaultDeviceName()
	}

	// The key is generated here, before enrolment, so the server never sees it.
	var keyID, recoveryCode string
	if req.Encrypt || req.RecoveryCode != "" {
		key, err := deriveKey(req.RecoveryCode)
		if err != nil {
			return nil, err
		}
		keyID = key.ID()
		recoveryCode = key.RecoveryCode()
	}

	hostname, _ := os.Hostname()
	resp, err := client.Enroll(ctx, api.EnrollRequest{
		JoinToken:    req.Code,
		DeviceName:   name,
		Hostname:     hostname,
		Platform:     Platform(),
		OSVersion:    runtime.GOOS + " " + runtime.GOARCH,
		AgentVersion: version.Version,
		KeyID:        keyID,
	})
	if err != nil {
		return nil, err
	}

	serverURL := client.BaseURL()
	if err := a.cfg.Update(func(c *config.Config) error {
		c.ServerURL = serverURL
		c.DeviceID = resp.DeviceID
		c.DeviceToken = resp.DeviceToken
		c.DeviceName = name
		c.RefreshDetectedRoots()
		if keyID != "" {
			c.Encryption = config.Encryption{Enabled: true, KeyID: keyID, RecoveryCode: recoveryCode}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return &ConnectResult{
		DeviceName:   name,
		ServerURL:    serverURL,
		Folders:      a.Folders(),
		RecoveryCode: recoveryCode,
	}, nil
}

// Disconnect clears this device's enrolment from the local config so the app
// returns to the connect screen. It does not delete backups on the server —
// remove the device in the dashboard if you want that too. The recovery code is
// wiped from disk with the rest of the credentials; write it down first if you
// still need it.
func (a *Agent) Disconnect(ctx context.Context) error {
	if !a.cfg.Enrolled() {
		return ErrNotConnected
	}
	// Best-effort: stop in-flight work so the daemon does not keep using stale
	// credentials after we wipe them.
	_ = a.Pause(ctx, 24*time.Hour)

	if err := a.cfg.Update(func(c *config.Config) error {
		c.ServerURL = ""
		c.DeviceID = ""
		c.DeviceToken = ""
		c.DeviceName = ""
		c.Encryption = config.Encryption{}
		return nil
	}); err != nil {
		return err
	}
	_ = a.applyToDaemon(ctx)
	return nil
}

// deriveKey reproduces an existing key from a recovery code, or makes a new one.
func deriveKey(recoveryCode string) (*codec.Key, error) {
	if code := strings.TrimSpace(recoveryCode); code != "" {
		key, err := codec.KeyFromRecoveryCode(code)
		if err != nil {
			return nil, fmt.Errorf("that recovery code is not valid: %w", err)
		}
		return key, nil
	}
	return codec.NewRandomKey()
}

// Platform reports this machine's platform in the protocol's terms.
func Platform() api.Platform {
	switch runtime.GOOS {
	case "windows":
		return api.PlatformWindows
	case "darwin":
		return api.PlatformDarwin
	case "android":
		return api.PlatformAndroid
	default:
		return api.PlatformLinux
	}
}

// -----------------------------------------------------------------------------
// Folders
// -----------------------------------------------------------------------------

// Folder is a configured backup root as a UI needs to see it.
type Folder struct {
	Name string `json:"name"`
	// Label is the name to show a person: "Pictures" rather than "pictures".
	Label   string `json:"label"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	// Detected marks folders found automatically, which a UI can present
	// differently from ones the user chose.
	Detected bool `json:"detected"`
	// Exists is false for a folder that has been moved, renamed or is on a drive
	// that is not plugged in. Saying so is important: otherwise a folder silently
	// stops being backed up.
	Exists bool `json:"exists"`
}

// Folders lists the configured roots.
func (a *Agent) Folders() []Folder {
	roots := a.cfg.Settings().Roots
	out := make([]Folder, 0, len(roots))
	for _, r := range roots {
		info, err := os.Stat(r.Path)
		out = append(out, Folder{
			Name:     r.Name,
			Label:    folderLabel(r.Name, r.Path),
			Path:     r.Path,
			Enabled:  r.Enabled,
			Detected: r.Detected,
			Exists:   err == nil && info.IsDir(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// folderLabel gives a detected folder its proper display name and falls back to
// the folder's own name on disk for one the user added.
func folderLabel(name, path string) string {
	for _, d := range userdirs.Detect() {
		if strings.EqualFold(string(d.Kind), name) {
			return d.Label
		}
	}
	if base := filepath.Base(path); base != "" && base != "." {
		return base
	}
	return name
}

// Suggestions lists personal folders that are not configured yet, so a UI can
// offer them instead of making the user hunt through a file picker.
func (a *Agent) Suggestions() []Folder {
	roots := a.cfg.Settings().Roots
	configured := make(map[string]bool, len(roots))
	for _, r := range roots {
		configured[strings.ToLower(r.Path)] = true
	}
	var out []Folder
	for _, d := range userdirs.Detect() {
		if !d.Exists || configured[strings.ToLower(d.Path)] {
			continue
		}
		out = append(out, Folder{Name: string(d.Kind), Label: d.Label, Path: d.Path, Detected: true, Exists: true})
	}
	return out
}

// AddFolder starts backing up a folder.
func (a *Agent) AddFolder(ctx context.Context, path string) error {
	if err := a.cfg.Update(func(c *config.Config) error { return c.AddRoot(path) }); err != nil {
		return err
	}
	return a.applyToDaemon(ctx)
}

// RemoveFolder stops backing up a folder. Existing backups are untouched: this
// changes what happens next, not what already happened.
func (a *Agent) RemoveFolder(ctx context.Context, pathOrName string) error {
	if err := a.cfg.Update(func(c *config.Config) error { return c.RemoveRoot(pathOrName) }); err != nil {
		return err
	}
	return a.applyToDaemon(ctx)
}

// SetFolderEnabled pauses or resumes one folder without forgetting it.
func (a *Agent) SetFolderEnabled(ctx context.Context, pathOrName string, enabled bool) error {
	err := a.cfg.Update(func(c *config.Config) error {
		for i := range c.Roots {
			r := &c.Roots[i]
			if strings.EqualFold(r.Path, pathOrName) || strings.EqualFold(r.Name, pathOrName) {
				r.Enabled = enabled
				return nil
			}
		}
		return fmt.Errorf("no folder called %q is configured", pathOrName)
	})
	if err != nil {
		return err
	}
	return a.applyToDaemon(ctx)
}

// applyToDaemon tells a running daemon to re-read the configuration it has just
// been given.
//
// A stopped daemon is not a failure: it reads the file when it starts, so the
// change is already saved and will apply. A running daemon that refuses the
// reload is a failure, and the reason is passed on rather than swallowed.
func (a *Agent) applyToDaemon(ctx context.Context) error {
	client, err := a.daemon()
	if err != nil {
		return nil
	}
	return client.Reload(ctx)
}

// -----------------------------------------------------------------------------
// Running backups
// -----------------------------------------------------------------------------

// BackupNow asks the daemon to back up immediately.
func (a *Agent) BackupNow(ctx context.Context) error {
	if !a.cfg.Enrolled() {
		return ErrNotConnected
	}
	client, err := a.daemon()
	if err != nil {
		return err
	}
	return client.BackupNow(ctx)
}

// Pause stops backups, for a period or until resumed. The choice is written to
// the configuration as well as sent to the daemon, so it survives a restart.
func (a *Agent) Pause(ctx context.Context, d time.Duration) error {
	if client, err := a.daemon(); err == nil {
		if err := client.Pause(ctx, d); err != nil {
			return err
		}
	}
	// An indefinite pause is a lasting decision; a timed one is not, and writing
	// it down would leave the agent paused after a reboot.
	if d == 0 {
		return a.cfg.Update(func(c *config.Config) error {
			c.Paused = true
			return nil
		})
	}
	return nil
}

// Resume restarts backups.
func (a *Agent) Resume(ctx context.Context) error {
	if client, err := a.daemon(); err == nil {
		if err := client.Resume(ctx); err != nil {
			return err
		}
	}
	return a.cfg.Update(func(c *config.Config) error {
		c.Paused = false
		return nil
	})
}

// -----------------------------------------------------------------------------
// Browsing and restoring
// -----------------------------------------------------------------------------

// Snapshots lists this device's backups, newest first.
func (a *Agent) Snapshots(ctx context.Context) ([]api.Snapshot, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	snapshots, err := client.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].StartedAt.After(snapshots[j].StartedAt) })
	return snapshots, nil
}

// Page is one level of a snapshot's contents.
type Page struct {
	Entries    []api.Entry `json:"entries"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// Browse lists the immediate contents of a folder inside a backup. An empty
// snapshot reference means the most recent completed backup, which is what
// someone looking for a lost file almost always wants.
func (a *Agent) Browse(ctx context.Context, snapshotRef, prefix, cursor string, limit int) (*Page, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	snap, err := a.resolveSnapshot(ctx, client, snapshotRef)
	if err != nil {
		return nil, err
	}
	entries, next, err := client.SnapshotEntries(ctx, snap.ID, api.EntryQuery{
		Prefix: prefix, Cursor: cursor, Limit: limit, DirectOnly: true,
	})
	if err != nil {
		return nil, err
	}
	return &Page{Entries: entries, NextCursor: next}, nil
}

// Search finds files by name inside a backup.
func (a *Agent) Search(ctx context.Context, snapshotRef, query string, limit int) ([]api.Entry, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	snap, err := a.resolveSnapshot(ctx, client, snapshotRef)
	if err != nil {
		return nil, err
	}
	return restore.Search(ctx, client, snap.ID, query, limit)
}

// FileVersion is one distinct appearance of a path across backups.
type FileVersion = api.FileVersion

// FileVersions lists how a path looked across completed backups, newest first.
// Identical consecutive content is collapsed so the UI shows real changes.
func (a *Agent) FileVersions(ctx context.Context, path string, limit int) ([]FileVersion, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	return client.FileVersions(ctx, path, limit)
}

func (a *Agent) resolveSnapshot(ctx context.Context, client *api.Client, ref string) (*api.Snapshot, error) {
	if ref == "" {
		ref = "latest"
	}
	return restore.FindSnapshot(ctx, client, ref)
}

// RestoreRequest describes a restore.
type RestoreRequest struct {
	Snapshot string `json:"snapshot"`
	// Path limits the restore to one file or folder; empty restores everything.
	Path string `json:"path"`
	// Target is the directory to write into.
	Target string `json:"target"`
	// Conflict is skip, overwrite or rename. Skip is the default because a
	// restore must never be the thing that destroys the file someone still had.
	Conflict string `json:"conflict"`
	DryRun   bool   `json:"dry_run"`
}

// Restore rebuilds files from a backup onto this machine. Progress is reported
// through the callback, which a UI can forward to a progress bar.
func (a *Agent) Restore(ctx context.Context, req RestoreRequest, progress func(restore.Progress)) (*restore.Result, error) {
	client, err := a.client()
	if err != nil {
		return nil, err
	}
	snap, err := a.resolveSnapshot(ctx, client, req.Snapshot)
	if err != nil {
		return nil, err
	}

	c, err := a.codec()
	if err != nil {
		return nil, err
	}
	defer c.Close()

	target := req.Target
	if target == "" {
		return nil, errors.New("choose a folder to restore into")
	}
	prefix := strings.Trim(strings.ReplaceAll(req.Path, `\`, "/"), "/")
	conflict := restore.ConflictSkip
	switch req.Conflict {
	case "", string(restore.ConflictSkip):
		conflict = restore.ConflictSkip
	case string(restore.ConflictOverwrite):
		conflict = restore.ConflictOverwrite
	case string(restore.ConflictRename):
		conflict = restore.ConflictRename
	default:
		return nil, fmt.Errorf("unknown conflict mode %q", req.Conflict)
	}

	return restore.Run(ctx, restore.Options{
		Client:     client,
		Codec:      c,
		SnapshotID: snap.ID,
		Prefix:     prefix,
		Target:     target,
		Conflict:   conflict,
		DryRun:     req.DryRun,
		Progress:   progress,
	})
}

// codec builds the codec for this device, including its encryption key when
// end-to-end encryption is on.
func (a *Agent) codec() (*codec.Codec, error) {
	opts := codec.Options{}
	if a.cfg.Encryption.Enabled {
		key, err := codec.KeyFromRecoveryCode(a.cfg.Encryption.RecoveryCode)
		if err != nil {
			return nil, fmt.Errorf("cannot use this device's encryption key: %w", err)
		}
		opts.Key = key
	}
	return codec.New(opts)
}

// -----------------------------------------------------------------------------
// Settings
// -----------------------------------------------------------------------------

// Settings is the subset of configuration a user should ever change.
type Settings struct {
	UploadBytesPerSec    int64   `json:"upload_bytes_per_sec"`
	MaxCPUPercent        float64 `json:"max_cpu_percent"`
	PauseOnMetered       bool    `json:"pause_on_metered"`
	PauseOnBattery       bool    `json:"pause_on_battery"`
	PauseWhileFullscreen bool    `json:"pause_while_fullscreen"`
	FullScanMinutes      int     `json:"full_scan_minutes"`
	Encrypted            bool    `json:"encrypted"`
	// RecoveryCode is only filled in when a UI explicitly asks to reveal it.
	RecoveryCode string `json:"recovery_code,omitempty"`
}

// Settings reads the current settings.
func (a *Agent) Settings() Settings {
	set := a.cfg.Settings()
	return Settings{
		UploadBytesPerSec:    set.Limits.UploadBytesPerSec,
		MaxCPUPercent:        set.Limits.MaxCPUPercent,
		PauseOnMetered:       set.Limits.PauseOnMetered,
		PauseOnBattery:       set.Limits.PauseOnBattery,
		PauseWhileFullscreen: set.Limits.PauseWhileFullscreen,
		FullScanMinutes:      int(set.Schedule.FullScanInterval / time.Minute),
		Encrypted:            set.Encryption.Enabled,
	}
}

// UpdateSettings writes new settings and tells the daemon to reload them.
func (a *Agent) UpdateSettings(ctx context.Context, s Settings) (Settings, error) {
	if s.UploadBytesPerSec < 0 {
		return a.Settings(), errors.New("the upload limit cannot be negative")
	}
	if s.MaxCPUPercent < 0 || s.MaxCPUPercent > 100 {
		return a.Settings(), errors.New("the CPU limit must be between 0 and 100")
	}
	if s.FullScanMinutes < 0 {
		return a.Settings(), errors.New("the scan interval cannot be negative")
	}

	err := a.cfg.Update(func(c *config.Config) error {
		c.Limits.UploadBytesPerSec = s.UploadBytesPerSec
		c.Limits.MaxCPUPercent = s.MaxCPUPercent
		c.Limits.PauseOnMetered = s.PauseOnMetered
		c.Limits.PauseOnBattery = s.PauseOnBattery
		c.Limits.PauseWhileFullscreen = s.PauseWhileFullscreen
		if s.FullScanMinutes > 0 {
			c.Schedule.FullScanInterval = time.Duration(s.FullScanMinutes) * time.Minute
		}
		return nil
	})
	if err != nil {
		return a.Settings(), err
	}
	if err := a.applyToDaemon(ctx); err != nil {
		return a.Settings(), err
	}
	return a.Settings(), nil
}

// RecoveryCode reveals the encryption recovery code, for a user who is writing it
// down. It is a separate call so the code is never included in routine state a UI
// might log or cache.
func (a *Agent) RecoveryCode() (string, error) {
	enc := a.cfg.Settings().Encryption
	if !enc.Enabled {
		return "", errors.New("end-to-end encryption is not on for this device")
	}
	return enc.RecoveryCode, nil
}

// EnableEncryption turns on end-to-end encryption and returns the recovery code.
// An existing account's recovery code may be supplied so this device shares the
// key its siblings already use.
//
// It refuses once backups exist, because from that point on some data on the
// server would be readable and some not, and no honest UI could describe that
// state in one sentence.
func (a *Agent) EnableEncryption(ctx context.Context, recoveryCode string) (string, error) {
	if enc := a.cfg.Settings().Encryption; enc.Enabled {
		return enc.RecoveryCode, nil
	}
	if !a.cfg.Enrolled() {
		return "", ErrNotConnected
	}
	snapshots, err := a.Snapshots(ctx)
	if err == nil && len(snapshots) > 0 {
		return "", errors.New("this device has already backed up unencrypted data; " +
			"remove the device in the dashboard and connect it again with encryption turned on")
	}
	key, err := deriveKey(recoveryCode)
	if err != nil {
		return "", err
	}
	enc := config.Encryption{
		Enabled:      true,
		KeyID:        key.ID(),
		RecoveryCode: key.RecoveryCode(),
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Encryption = enc
		return nil
	}); err != nil {
		return "", err
	}
	// The running daemon holds the old (empty) key, so it has to be restarted
	// rather than reloaded. Saying nothing here would leave the user believing
	// their next backup is encrypted when it is not.
	if _, err := a.daemon(); err == nil {
		return enc.RecoveryCode, errors.New("encryption is on for future backups; " +
			"restart the OpenBackup service so the running agent picks up the new key")
	}
	return enc.RecoveryCode, nil
}

// -----------------------------------------------------------------------------
// Diagnostics
// -----------------------------------------------------------------------------

// Check is one diagnostic result.
type Check struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	// Detail explains the result, and for a failure says what to do about it.
	Detail string `json:"detail"`
}

// Diagnose runs the checks behind the doctor command, in the order a person would
// debug: is it configured, is it running, can it reach the server, can it read
// the folders, and has anything actually arrived.
func (a *Agent) Diagnose(ctx context.Context) []Check {
	checks := make([]Check, 0, 6)

	checks = append(checks, Check{
		Name:   "Configuration",
		OK:     true,
		Detail: a.cfg.Path(),
	})

	if !a.cfg.Enrolled() {
		return append(checks, Check{
			Name:   "Connection",
			OK:     false,
			Detail: "This device is not connected to a server yet.",
		})
	}

	if _, err := a.daemon(); err == nil {
		checks = append(checks, Check{Name: "Background service", OK: true, Detail: "Running."})
	} else {
		checks = append(checks, Check{
			Name:   "Background service",
			OK:     false,
			Detail: "Not running, so nothing is being backed up. Install it with 'openbackup service install'.",
		})
	}

	client, err := a.client()
	if err == nil {
		if err := client.Health(ctx); err == nil {
			checks = append(checks, Check{Name: "Server", OK: true, Detail: a.cfg.ServerURL + " is reachable."})
		} else {
			checks = append(checks, Check{Name: "Server", OK: false,
				Detail: fmt.Sprintf("Cannot reach %s: %v", a.cfg.ServerURL, err)})
		}
	}

	// A folder that has moved is the most common cause of a backup that quietly
	// covers less than the user thinks.
	if missing := a.cfg.MissingRoots(); len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, r := range missing {
			names = append(names, r.Path)
		}
		checks = append(checks, Check{Name: "Folders", OK: false,
			Detail: "These folders no longer exist: " + strings.Join(names, ", ")})
	} else {
		checks = append(checks, Check{Name: "Folders", OK: true,
			Detail: fmt.Sprintf("%s configured.", plural(len(a.cfg.EnabledRoots()), "folder"))})
	}

	if snapshots, err := a.Snapshots(ctx); err == nil {
		if newest := newestComplete(snapshots); newest != nil {
			checks = append(checks, Check{Name: "Backups", OK: true,
				Detail: fmt.Sprintf("%s on the server, most recent %s ago.",
					plural(len(snapshots), "backup"), roughly(time.Since(newest.StartedAt)))})
		} else {
			checks = append(checks, Check{Name: "Backups", OK: false,
				Detail: "No completed backup on the server yet."})
		}
	}
	return checks
}

// -----------------------------------------------------------------------------
// Formatting shared by the CLI and the UI
// -----------------------------------------------------------------------------

func indexPath(stateDir string) string { return filepath.Join(stateDir, "index.db") }

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KB", "MB", "GB", "TB", "PB"} {
		value /= unit
		if value < unit {
			if value < 10 {
				return fmt.Sprintf("%.1f %s", value, suffix)
			}
			return fmt.Sprintf("%.0f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f EB", value)
}

// roughly renders a duration the way a person would say it out loud.
func roughly(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "less than a minute"
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour")
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	default:
		return plural(int(d.Hours()/24/30), "month")
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func thousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
