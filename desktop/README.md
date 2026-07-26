# OpenBackup desktop app

The window: Go for the logic, React and Tailwind for the interface, glued
together by [Wails](https://wails.io).

## What it is, and is not

It is a *client*. Backups are done by the `openbackup` agent running as a
background service, and everything the window does goes through
`internal/agent/control`, the same package the CLI uses. Quitting the window
stops nothing; installing the service is what starts backups.

That split is why the window can stay small. It holds no backup state, so there
is nothing to keep in sync: it polls the agent for status, calls control
operations, and asks the running daemon to reload after a change so a new folder
or a new speed limit applies without a restart.

## Why a separate Go module

Wails links against the platform's webview — WebView2 on Windows, WebKitGTK on
Linux. Inside the main module, `go build ./...` on a headless Linux box would
fail on missing GTK headers, and the agent and server have to stay pure-Go
cross-compiles so one machine can build every release target. The `replace`
directive in `go.mod` points at the repository, so the app always builds against
the agent code next to it.

## Layout

```
app.go              the methods the frontend calls, bound by Wails
main.go             window setup, single-instance lock, logging
tray.go             notification-area icon, menu and state
platform.go         locating the CLI, services, notifications, revealing paths
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

On Linux you can also install a local or release build into `~/.local`:

```bash
../scripts/install-desktop-linux.sh
```

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
