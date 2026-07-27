// Command openbackup-desktop is the OpenBackup app for Windows, macOS and Linux.
//
// One binary is the window and the backup engine (Dropbox-style): close the
// window and backups keep running in the tray; Quit stops them until you open
// the app again or sign in (login autostart uses --background).
package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

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

	args, startHidden := stripBackgroundFlag(os.Args[1:])

	// Headless agent mode for servers/CLI-style installs (`service` / `run`).
	// Normal desktop use embeds the engine in the GUI process instead.
	if agentMode(args) {
		if err := runAgentMode(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

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
	app.startHidden = startHidden
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
		OnBeforeClose: func(ctx context.Context) bool {
			app.mu.Lock()
			quit := app.forceQuit
			app.mu.Unlock()
			if quit {
				return false // allow real exit (tray Quit)
			}
			// Closing the window hides it instead of quitting, because backups
			// continue in this process and the tray icon is how the user gets back.
			app.SetWindowVisible(false)
			wruntime.WindowHide(ctx)
			return true
		},
		OnStartup: func(ctx context.Context) {
			app.startup(ctx)
			// The tray runs its own message loop, so it gets its own goroutine;
			// blocking here would stop the window from ever appearing.
			go trayIcon.start(ctx)
			listenForRaise(ctx, func() {
				app.SetWindowVisible(true)
				wruntime.WindowShow(ctx)
				wruntime.WindowUnminimise(ctx)
			})
		},
		OnShutdown: func(context.Context) {
			app.stopEmbeddedAgent()
			trayIcon.stop()
		},
		Bind: []any{app},
		Windows: &windows.Options{
			// Mica/Acrylic keep extra compositor buffers; plain backdrop uses less RAM.
			BackdropType:         windows.None,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			// GPU process is often the bulk of WebView2 working set on low-RAM PCs.
			WebviewGpuIsDisabled: true,
		},
		Linux: &linux.Options{
			Icon:                notificationIcon,
			WindowIsTranslucent: false,
			ProgramName:         "OpenBackup",
			WebviewGpuPolicy:    linux.WebviewGpuPolicyNever,
		},
	})
	if err != nil {
		log.Error("the OpenBackup window could not start", "error", err)
		os.Exit(1)
	}
}

// stripBackgroundFlag removes --background (login autostart) and reports it.
func stripBackgroundFlag(args []string) (rest []string, background bool) {
	rest = make([]string, 0, len(args))
	for _, a := range args {
		switch strings.TrimSpace(a) {
		case "--background", "-background":
			background = true
		default:
			rest = append(rest, a)
		}
	}
	return rest, background
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
