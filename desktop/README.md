# OpenBackup desktop app

The window **and** the background agent in one binary: Go for the logic, React
and Tailwind for the interface, glued together by [Wails](https://wails.io).

## What it is

Open the app for the UI. The same executable is what Windows/systemd/launchd
runs when backups happen with the window closed (`service run`). Quitting the
window stops nothing; **Start service** registers this binary with the OS.

The UI still talks to the running agent through `internal/agent/control` (same
as the optional CLI). Service install/start uses `internal/agent/appsvc` on
**this** process — no second `openbackup.exe` download.

## Why a separate Go module

Wails links against the platform's webview — WebView2 on Windows, WebKitGTK on
Linux. Inside the main module, `go build ./...` on a headless Linux box would
fail on missing GTK headers, and the agent CLI and server have to stay pure-Go
cross-compiles so one machine can build every release target. The `replace`
directive in `go.mod` points at the repository, so the app always builds against
the agent code next to it.

## Layout

```
app.go              the methods the frontend calls, bound by Wails
main.go             window setup, single-instance lock, logging; agent-mode entry
agent_mode.go       service/run without opening the window
tray.go             notification-area icon, menu and state
platform.go         service install, notifications, revealing paths
process_*.go        per-platform "is this pid alive"
trayicon_*.go       per-platform icon format (Windows needs an ICO)
build/icons/        the generator for the app and tray icons
frontend/           Vite + React + TypeScript, mirroring web/'s design system
```

Types crossing the bridge are hand-written in `frontend/src/lib/types.ts` rather
than generated, so the frontend builds without running Wails first and a change
to a Go struct shows up as a TypeScript error.

## Working on it

```bash
make desktop-dev    # live-reloading window (uses webkit2_41 on Linux)
make desktop        # release build into build/bin
make desktop-check  # go vet and tsc
make desktop-linux-package  # Linux: release-named binary + .desktop + icon
```

On Linux the agent installer (`scripts/install-agent.sh`) also pulls the desktop
binary from GitHub Releases when available.

Icons are generated, not drawn:

```bash
go run ./build/icons
```

The window needs a server to talk to. The quickest setup is a local one:

```bash
go run ../cmd/openbackup-server                 # in a second terminal
go run ../cmd/openbackup-server invite          # gives a connection code
```

Point the app at a throwaway configuration so it cannot disturb a real backup:

```
OPENBACKUP_CONFIG=/tmp/ob-dev/config.json
OPENBACKUP_STATE_DIR=/tmp/ob-dev/state
```
