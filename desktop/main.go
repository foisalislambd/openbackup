// Command openbackup-desktop is the OpenBackup window for Windows, macOS and
// Linux.
//
// It is a client, not the backup engine. The engine runs as a background service
// that keeps working when this window is closed, or never opened at all; this app
// shows what it is doing and lets a person change it. That separation is the
// whole point: a backup that only happens while an app is open is not a backup.
package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/foisalislambd/openbackup/internal/agent/config"
	"github.com/foisalislambd/openbackup/internal/logx"
	"github.com/foisalislambd/openbackup/internal/version"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var notificationIcon []byte

// appVersion is the version shown in the window, kept in step with the agent's.
var appVersion = version.Version

func main() {
	log := newLogger()

	// A production Windows build has no console, so a panic would otherwise leave
	// the user with a window that vanished and no explanation anywhere.
	defer func() {
		if rec := recover(); rec != nil {
			log.Error("the OpenBackup window stopped unexpectedly",
				"panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
			os.Exit(2)
		}
	}()

	release, only := singleInstance()
	if !only {
		// Another window is already open — ask it to come forward, then exit.
		if signalRaise() {
			log.Info("raised the existing OpenBackup window")
		} else {
			log.Info("another OpenBackup window is already open")
		}
		return
	}
	defer release()

	app := newApp(log)
	trayIcon := newTray(app, log)
	app.tray = trayIcon

	err := wails.Run(&options.App{
		Title:  "OpenBackup",
		Width:  1040,
		Height: 720,
		// Small enough to fit a laptop screen, large enough that the folder list
		// and the restore browser are both usable.
		MinWidth:  880,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			// The tray runs its own message loop, so it gets its own goroutine;
			// blocking here would stop the window from ever appearing.
			go trayIcon.start(ctx)
			listenForRaise(ctx, func() {
				wruntime.WindowShow(ctx)
				wruntime.WindowUnminimise(ctx)
			})
		},
		OnBeforeClose: func(ctx context.Context) bool {
			// Closing the window hides it instead of quitting, because backups
			// continue and the tray icon is how the user gets back to them.
			// Quitting is an explicit choice from the tray menu.
			wruntime.WindowHide(ctx)
			return true
		},
		OnShutdown: func(context.Context) {
			trayIcon.stop()
		},
		Bind: []any{app},
		Windows: &windows.Options{
			// The system backdrop makes the window feel native on Windows 11 and
			// degrades to a plain background on older builds.
			BackdropType:         windows.Mica,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
		},
		Linux: &linux.Options{
			Icon:                notificationIcon,
			WindowIsTranslucent: false,
			// Matches the .desktop StartupWMClass so the window groups with its
			// launcher icon on GNOME/KDE.
			ProgramName: "OpenBackup",
			// Default when Linux options are set; Never caused blank windows on
			// some NVIDIA/Wayland setups — OnDemand is the safer middle ground.
			WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
		},
	})
	if err != nil {
		log.Error("the OpenBackup window could not start", "error", err)
		os.Exit(1)
	}
}

// newLogger writes the app's own log next to the agent's, so a support request
// only has to collect one folder.
func newLogger() *slog.Logger {
	stateDir, err := config.StateDir()
	if err != nil {
		return slog.Default()
	}
	return logx.New(logx.Options{
		Level: "info",
		File:  filepath.Join(stateDir, "desktop.log"),
	})
}
