package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/foisalislambd/openbackup/internal/agent/control"
	"github.com/foisalislambd/openbackup/internal/agent/restore"
	"github.com/foisalislambd/openbackup/internal/api"
)

// App is the bridge between the window and the agent.
//
// Every method here is exposed to the frontend, so each one is written as an
// answer to a question a person would ask ("what is my backup doing?", "get this
// file back"), not as a thin wrapper over a data structure. The real work lives
// in internal/agent/control, which the command line uses too: the window and the
// terminal cannot disagree about what an action does.
type App struct {
	ctx   context.Context
	log   *slog.Logger
	agent *control.Agent
	// tray is set once the notification area icon exists, so status updates reach
	// it without the two polling the agent separately.
	tray *tray

	// openErr records a configuration that could not be read. The window still
	// opens in that case and shows the reason, because a desktop app that exits
	// on startup leaves the user with nothing to act on.
	openErr error

	// mu guards the fields below, which the polling loop and the frontend both
	// touch.
	mu       sync.Mutex
	overview control.Overview
	restore  *restoreJob
}

// restoreJob tracks a running restore so the window can show progress and the
// user cannot start a second one on top of it.
type restoreJob struct {
	cancel   context.CancelFunc
	Running  bool   `json:"running"`
	Target   string `json:"target"`
	Path     string `json:"path"`
	Files    int64  `json:"files"`
	OfFiles  int64  `json:"of_files"`
	Bytes    int64  `json:"bytes"`
	Current  string `json:"current"`
	Finished bool   `json:"finished"`
	Error    string `json:"error,omitempty"`
	Restored int64  `json:"restored"`
	Skipped  int64  `json:"skipped"`
	Failed   int    `json:"failed"`
}

func newApp(log *slog.Logger) *App {
	app := &App{log: log}
	agent, err := control.Open("")
	if err != nil {
		app.openErr = err
		log.Error("open agent configuration", "error", err)
		return app
	}
	app.agent = agent
	return app
}

// startup is called by Wails once the window exists.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go a.pollStatus(ctx)
}

// pollStatus keeps the window's status fresh and pushes changes to the frontend.
//
// Polling rather than a subscription keeps the agent's control channel simple,
// and two seconds is frequent enough to feel live while costing nothing
// measurable. The loop stops when the window closes.
func (a *App) pollStatus(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastHealth string
	for {
		if a.agent != nil {
			// The configuration can be changed by the command line, so it is
			// re-read before each poll; otherwise the window would show a stale
			// folder list.
			if err := a.agent.Reload(); err != nil {
				a.log.Debug("reload configuration", "error", err)
			}
			pollCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			overview := a.agent.Overview(pollCtx)
			cancel()

			a.mu.Lock()
			a.overview = overview
			a.mu.Unlock()

			runtime.EventsEmit(ctx, "status", overview)
			if overview.Health != lastHealth {
				a.announce(overview, lastHealth)
				lastHealth = overview.Health
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// announce raises a notification for the state changes worth interrupting
// someone for. Success is deliberately silent: a backup tool that congratulates
// itself every hour gets muted, and then the one message that mattered is missed
// too.
func (a *App) announce(o control.Overview, previous string) {
	if previous == "" {
		return
	}
	switch o.Health {
	case "agent_stopped":
		a.notify("Backups have stopped", "The OpenBackup background service is not running.")
	case "error":
		a.notify("A backup did not finish", o.Detail)
	case "stale":
		a.notify("Your backup is out of date", o.Detail)
	}
	a.setTrayState(o)
}

// -----------------------------------------------------------------------------
// Status
// -----------------------------------------------------------------------------

// Status returns the current overview for the window.
func (a *App) Status() (control.Overview, error) {
	if a.agent == nil {
		return control.Overview{}, a.configError()
	}
	a.mu.Lock()
	cached := a.overview
	a.mu.Unlock()
	// The very first call happens before the poll loop has run.
	if cached.Version == "" {
		cached = a.agent.Overview(a.context())
		a.mu.Lock()
		a.overview = cached
		a.mu.Unlock()
	}
	return cached, nil
}

// Diagnostics runs the same checks as 'openbackup doctor'.
func (a *App) Diagnostics() ([]control.Check, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Second)
	defer cancel()
	return a.agent.Diagnose(ctx), nil
}

// -----------------------------------------------------------------------------
// Setup
// -----------------------------------------------------------------------------

// Connect enrols this device from the onboarding screen.
func (a *App) Connect(req control.ConnectRequest) (*control.ConnectResult, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 90*time.Second)
	defer cancel()

	result, err := a.agent.Connect(ctx, req)
	if err != nil {
		return nil, err
	}
	// Being connected but not installed as a service is the most common way for
	// backups to quietly never happen, so the app installs it here rather than
	// asking the user to run a command.
	if err := installService(); err != nil {
		a.log.Warn("install background service", "error", err)
	}
	// Refresh the cached overview so Done → Status() does not replay a stale
	// disconnected snapshot. Do not emit a status event yet: that would unmount
	// the recovery-code screen before the user can copy the code.
	overview := a.agent.Overview(ctx)
	a.mu.Lock()
	a.overview = overview
	a.mu.Unlock()
	return result, nil
}

// Disconnect logs this device out locally so the window returns to onboarding.
// Server-side backups stay until the device is removed in the dashboard.
func (a *App) Disconnect() error {
	if a.agent == nil {
		return a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Second)
	defer cancel()
	if err := a.agent.Disconnect(ctx); err != nil {
		return err
	}
	// Push a fresh overview immediately so the window flips to onboarding without
	// waiting for the next poll tick.
	overview := a.agent.Overview(ctx)
	a.mu.Lock()
	a.overview = overview
	a.mu.Unlock()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status", overview)
	}
	a.notify("Logged out", "This device is no longer connected. Connect again with a new code when you are ready.")
	return nil
}

// -----------------------------------------------------------------------------
// Folders
// -----------------------------------------------------------------------------

// Folders lists the folders being backed up.
func (a *App) Folders() ([]control.Folder, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	return a.agent.Folders(), nil
}

// SuggestedFolders lists personal folders that are not backed up yet.
func (a *App) SuggestedFolders() ([]control.Folder, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	return a.agent.Suggestions(), nil
}

// ChooseFolder opens the native folder picker and starts backing up whatever the
// user chose. Doing both in one call is deliberate: a picker that only fills in a
// text box makes the user press a second button to mean the same thing.
func (a *App) ChooseFolder() (*control.Folder, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	home, _ := os.UserHomeDir()
	path, err := runtime.OpenDirectoryDialog(a.context(), runtime.OpenDialogOptions{
		Title:                "Choose a folder to back up",
		DefaultDirectory:     home,
		CanCreateDirectories: false,
	})
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil // the user cancelled, which is not an error
	}
	if err := a.AddFolder(path); err != nil {
		return nil, err
	}
	for _, folder := range a.agent.Folders() {
		if strings.EqualFold(folder.Path, filepath.Clean(path)) {
			return &folder, nil
		}
	}
	return nil, nil
}

// AddFolder starts backing up a folder.
func (a *App) AddFolder(path string) error {
	if a.agent == nil {
		return a.configError()
	}
	return a.agent.AddFolder(a.context(), path)
}

// RemoveFolder stops backing up a folder, keeping what is already backed up.
func (a *App) RemoveFolder(path string) error {
	if a.agent == nil {
		return a.configError()
	}
	return a.agent.RemoveFolder(a.context(), path)
}

// SetFolderEnabled pauses or resumes one folder.
func (a *App) SetFolderEnabled(path string, enabled bool) error {
	if a.agent == nil {
		return a.configError()
	}
	return a.agent.SetFolderEnabled(a.context(), path, enabled)
}

// RevealFolder opens a path in the system file manager.
func (a *App) RevealFolder(path string) error {
	return reveal(path)
}

// -----------------------------------------------------------------------------
// Running backups
// -----------------------------------------------------------------------------

// BackupNow asks the agent to back up immediately.
func (a *App) BackupNow() error {
	if a.agent == nil {
		return a.configError()
	}
	return a.agent.BackupNow(a.context())
}

// Pause stops backups for a number of minutes, or indefinitely when minutes is 0.
func (a *App) Pause(minutes int) error {
	if a.agent == nil {
		return a.configError()
	}
	return a.agent.Pause(a.context(), time.Duration(minutes)*time.Minute)
}

// Resume restarts backups.
func (a *App) Resume() error {
	if a.agent == nil {
		return a.configError()
	}
	return a.agent.Resume(a.context())
}

// StartService starts or installs the background agent and waits until it answers.
func (a *App) StartService() error {
	if err := installService(); err != nil {
		return err
	}
	overview := a.agent.Overview(a.context())
	a.mu.Lock()
	a.overview = overview
	a.mu.Unlock()
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "status", overview)
	}
	if !overview.AgentRunning {
		return errors.New("the background agent did not come up — open Diagnostics or check the log")
	}
	return nil
}

// -----------------------------------------------------------------------------
// Browsing and restoring
// -----------------------------------------------------------------------------

// Snapshots lists the backups on the server, newest first.
func (a *App) Snapshots() ([]api.Snapshot, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Second)
	defer cancel()
	return a.agent.Snapshots(ctx)
}

// Browse lists one level of a backup. An empty snapshot means the most recent.
func (a *App) Browse(snapshot, prefix, cursor string) (*control.Page, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 60*time.Second)
	defer cancel()
	prefix = strings.Trim(strings.ReplaceAll(prefix, `\`, "/"), "/")
	return a.agent.Browse(ctx, snapshot, prefix, cursor, 200)
}

// Search finds files by name inside a backup.
func (a *App) Search(snapshot, query string) ([]api.Entry, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(a.context(), 60*time.Second)
	defer cancel()
	return a.agent.Search(ctx, snapshot, query, 200)
}

// FileVersions lists distinct versions of a file across completed backups.
func (a *App) FileVersions(path string) ([]control.FileVersion, error) {
	if a.agent == nil {
		return nil, a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 90*time.Second)
	defer cancel()
	versions, err := a.agent.FileVersions(ctx, path, 40)
	if err != nil {
		return nil, err
	}
	if versions == nil {
		return []control.FileVersion{}, nil
	}
	return versions, nil
}

// ChooseRestoreTarget asks where to restore, defaulting to the desktop so the
// restored copy lands somewhere the user will find it.
func (a *App) ChooseRestoreTarget() (string, error) {
	home, _ := os.UserHomeDir()
	return runtime.OpenDirectoryDialog(a.context(), runtime.OpenDialogOptions{
		Title:                "Where should the files be restored?",
		DefaultDirectory:     filepath.Join(home, "Desktop"),
		CanCreateDirectories: true,
	})
}

// StartRestore begins a restore in the background and reports progress through
// the "restore" event. It returns once the restore has started, so the window
// stays responsive during what can be a very long operation.
func (a *App) StartRestore(req control.RestoreRequest) error {
	if a.agent == nil {
		return a.configError()
	}
	if strings.TrimSpace(req.Target) == "" {
		return errors.New("choose a folder to restore into")
	}
	a.mu.Lock()
	if a.restore != nil && a.restore.Running {
		a.mu.Unlock()
		return errors.New("a restore is already running; wait for it to finish or cancel it")
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(a.context()))
	job := &restoreJob{cancel: cancel, Running: true, Target: req.Target, Path: req.Path}
	a.restore = job
	a.mu.Unlock()

	a.emitRestore()
	go func() {
		defer cancel()
		result, err := a.agent.Restore(ctx, req, func(p restore.Progress) {
			a.mu.Lock()
			job.Current = p.Path
			job.Files = p.FilesDone
			job.OfFiles = p.FilesTotal
			job.Bytes = p.BytesWritten
			a.mu.Unlock()
			a.emitRestore()
		})

		a.mu.Lock()
		job.Running = false
		job.Finished = true
		job.Current = ""
		switch {
		case err != nil:
			job.Error = err.Error()
		default:
			job.Restored = result.FilesRestored
			job.Skipped = result.FilesSkipped
			job.Bytes = result.BytesWritten
			job.Failed = len(result.Failed)
		}
		a.mu.Unlock()
		a.emitRestore()

		if err == nil {
			a.notify("Restore finished",
				fmt.Sprintf("%d files were restored to %s.", result.FilesRestored, req.Target))
		}
	}()
	return nil
}

// CancelRestore stops a running restore. Files already written stay where they
// are: deleting them would be a second surprise on top of the cancellation.
func (a *App) CancelRestore() {
	a.mu.Lock()
	job := a.restore
	a.mu.Unlock()
	if job != nil && job.cancel != nil {
		job.cancel()
	}
}

// RestoreProgress returns a copy of the current or last restore state so the
// frontend cannot race the background job mutating the live struct.
func (a *App) RestoreProgress() *restoreJob {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.restore == nil {
		return nil
	}
	copy := *a.restore
	copy.cancel = nil
	return &copy
}

func (a *App) emitRestore() {
	a.mu.Lock()
	var job *restoreJob
	if a.restore != nil {
		copy := *a.restore
		copy.cancel = nil
		job = &copy
	}
	a.mu.Unlock()
	if a.ctx != nil && job != nil {
		runtime.EventsEmit(a.ctx, "restore", job)
	}
}

// -----------------------------------------------------------------------------
// Settings
// -----------------------------------------------------------------------------

// Settings reads the current settings.
func (a *App) Settings() (control.Settings, error) {
	if a.agent == nil {
		return control.Settings{}, a.configError()
	}
	return a.agent.Settings(), nil
}

// UpdateSettings saves new settings and applies them to the running agent.
func (a *App) UpdateSettings(s control.Settings) (control.Settings, error) {
	if a.agent == nil {
		return control.Settings{}, a.configError()
	}
	return a.agent.UpdateSettings(a.context(), s)
}

// RecoveryCode reveals the encryption recovery code so the user can write it
// down.
func (a *App) RecoveryCode() (string, error) {
	if a.agent == nil {
		return "", a.configError()
	}
	return a.agent.RecoveryCode()
}

// EnableEncryption turns on end-to-end encryption and returns the recovery code.
func (a *App) EnableEncryption(recoveryCode string) (string, error) {
	if a.agent == nil {
		return "", a.configError()
	}
	ctx, cancel := context.WithTimeout(a.context(), 30*time.Second)
	defer cancel()
	return a.agent.EnableEncryption(ctx, recoveryCode)
}

// OpenDashboard opens the server's web dashboard in the default browser, which is
// where account-wide things (other devices, storage, users) belong.
func (a *App) OpenDashboard() error {
	status, err := a.Status()
	if err != nil {
		return err
	}
	if status.ServerURL == "" {
		return errors.New("this device is not connected to a server yet")
	}
	runtime.BrowserOpenURL(a.context(), status.ServerURL)
	return nil
}

// AppInfo describes the build, for the about screen and for bug reports.
type AppInfo struct {
	Version    string `json:"version"`
	Platform   string `json:"platform"`
	ConfigPath string `json:"config_path"`
	LogPath    string `json:"log_path"`
}

// Info returns build and path information.
func (a *App) Info() AppInfo {
	info := AppInfo{Version: appVersion, Platform: runtime.Environment(a.context()).Platform}
	if a.agent != nil {
		info.ConfigPath = a.agent.Config().Path()
	}
	info.LogPath = logPath()
	return info
}

// OpenLogFolder shows the agent's log in the file manager, which is what a
// support request needs.
func (a *App) OpenLogFolder() error { return reveal(filepath.Dir(logPath())) }

// -----------------------------------------------------------------------------
// Window behaviour
// -----------------------------------------------------------------------------

// MinimiseToTray hides the window. Backups keep running: the service is a
// separate process, and this app is only its face.
func (a *App) MinimiseToTray() { runtime.WindowHide(a.context()) }

// Quit closes the app. It does not stop backups, and the frontend says so before
// calling this.
func (a *App) Quit() { runtime.Quit(a.context()) }

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) configError() error {
	if a.openErr != nil {
		return fmt.Errorf("cannot read the OpenBackup configuration: %w", a.openErr)
	}
	return errors.New("cannot read the OpenBackup configuration")
}

// notify shows a system notification, falling back to nothing if the platform
// cannot.
func (a *App) notify(title, body string) {
	if a.ctx == nil {
		return
	}
	if err := showNotification(title, body); err != nil {
		a.log.Debug("notification", "error", err)
	}
}
