package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/energye/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/openbackup/openbackup/internal/agent/control"
)

//go:embed all:build/tray
var trayIcons embed.FS

// tray is the app's presence in the notification area.
//
// It exists because the window is not where this app lives. Once set up, a person
// should be able to forget OpenBackup entirely; the tray is the one place that
// answers "is my data safe?" without opening anything, and the way back in when
// the answer is no.
type tray struct {
	app *App
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	started bool
	status  *systray.MenuItem
	pause   *systray.MenuItem
	resume  *systray.MenuItem
	backup  *systray.MenuItem
	// lastIcon avoids redrawing the icon on every poll, which flickers on some
	// Windows builds.
	lastIcon string
}

func newTray(app *App, log *slog.Logger) *tray {
	return &tray{app: app, log: log}
}

// start puts the icon in the notification area and keeps it up to date.
func (t *tray) start(ctx context.Context) {
	t.ctx, t.cancel = context.WithCancel(ctx)
	systray.Run(func() { t.onReady(t.ctx) }, func() {})
}

// stop removes the icon.
func (t *tray) stop() {
	t.mu.Lock()
	started := t.started
	t.mu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	if started {
		systray.Quit()
	}
}

func (t *tray) onReady(ctx context.Context) {
	t.mu.Lock()
	t.started = true
	t.mu.Unlock()

	systray.SetIcon(icon("idle"))
	systray.SetTitle("OpenBackup")
	systray.SetTooltip("OpenBackup")

	// Clicking the icon shows the window, which is what everyone tries first.
	systray.SetOnClick(func(menu systray.IMenu) { t.show() })
	systray.SetOnRClick(func(menu systray.IMenu) { _ = menu.ShowMenu() })

	// The first item is the answer, not a command: a glance at the menu should
	// say whether the backup is healthy.
	t.status = systray.AddMenuItem("Checking...", "")
	t.status.Disable()
	systray.AddSeparator()

	open := systray.AddMenuItem("Open OpenBackup", "Show the window")
	t.backup = systray.AddMenuItem("Back up now", "Start a backup immediately")
	t.pause = systray.AddMenuItem("Pause for 1 hour", "Stop backing up for a while")
	t.resume = systray.AddMenuItem("Resume backups", "Start backing up again")
	t.resume.Hide()
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Close the window; backups keep running")

	open.Click(func() { t.show() })
	t.backup.Click(func() {
		if err := t.app.BackupNow(); err != nil {
			t.log.Warn("back up now from the tray", "error", err)
			t.app.notify("Could not start a backup", err.Error())
		}
	})
	t.pause.Click(func() {
		if err := t.app.Pause(60); err != nil {
			t.log.Warn("pause from the tray", "error", err)
		}
	})
	t.resume.Click(func() {
		if err := t.app.Resume(); err != nil {
			t.log.Warn("resume from the tray", "error", err)
		}
	})
	quit.Click(func() {
		// Quitting the window never stops the service, and the menu item says so
		// rather than silently leaving the user unprotected.
		wruntime.Quit(ctx)
	})

	go t.refreshLoop(ctx)
}

// refreshLoop keeps the tooltip, icon and menu in step with the agent.
func (t *tray) refreshLoop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if status, err := t.app.Status(); err == nil {
			t.setTrayState(status)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// setTrayState renders one overview into the icon and menu.
func (t *tray) setTrayState(o control.Overview) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		return
	}

	name := iconFor(o.Health)
	if name != t.lastIcon {
		systray.SetIcon(icon(name))
		t.lastIcon = name
	}

	headline := o.Headline
	if headline == "" {
		headline = "OpenBackup"
	}
	systray.SetTooltip(fmt.Sprintf("OpenBackup - %s", headline))
	if t.status != nil {
		t.status.SetTitle(headline)
	}
	if t.pause != nil && t.resume != nil {
		if o.Paused {
			t.pause.Hide()
			t.resume.Show()
		} else {
			t.resume.Hide()
			t.pause.Show()
		}
	}
	if t.backup != nil {
		if o.Connected && o.AgentRunning {
			t.backup.Enable()
		} else {
			t.backup.Disable()
		}
	}
}

// setTrayState on the App lets the status poller update the tray without the two
// keeping separate copies of the state.
func (a *App) setTrayState(o control.Overview) {
	if a.tray != nil {
		a.tray.setTrayState(o)
	}
}

func (t *tray) show() {
	if t.ctx == nil {
		return
	}
	wruntime.WindowShow(t.ctx)
	wruntime.WindowUnminimise(t.ctx)
}

// iconFor maps a health state to an icon. Three states are enough: fine, working,
// and needs attention. More would be decoration at 16 pixels.
func iconFor(health string) string {
	switch health {
	case "working":
		return "working"
	case "protected":
		return "ok"
	case "not_connected", "never_run", "paused":
		return "idle"
	default:
		return "warning"
	}
}

// icon reads an embedded tray icon, preferring the platform's native format.
func icon(name string) []byte {
	for _, ext := range trayExtensions {
		if raw, err := trayIcons.ReadFile("build/tray/" + name + ext); err == nil {
			return raw
		}
	}
	return notificationIcon
}
