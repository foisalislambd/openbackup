# The desktop app

One app for the machine being backed up: the window **and** the background
agent. Connect a device, see whether files are safe, browse and restore, change
limits and encryption, and run diagnostics. Closing the window does not stop
backups — the same binary keeps running as an OS service.

Available for Windows, macOS and Linux. Same screens and design on every platform.

- **Windows:** download the installer (or the portable `.exe`) from the
  [releases page](https://github.com/foisalislambd/openbackup/releases). That
  single app is enough — you do **not** install a separate agent.
- **Linux:** the installer asks if you want the desktop app after the CLI is
  installed (CLI is optional for servers/headless):

  ```bash
  curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh
  # → Install desktop app? [y/N]
  openbackup-desktop
  ```

  Or set `OPENBACKUP_DESKTOP=1` / `OPENBACKUP_SKIP_DESKTOP=1`. Local build:
  `make desktop`.
- **macOS:** build with `make desktop` for now (same Wails app).

## One binary, two roles

| How you start it | What happens |
| --- | --- |
| Double-click / app menu | Opens the window |
| OS service (`… service run`) | Runs the backup agent with no window |
| **Start service** in the app | Registers **this** binary with Windows/systemd/launchd |

A backup that only happens while a window is open is not a backup — so the agent
keeps working when you close the UI. The tray icon is how you get back.

Anything changed in the window is applied to the running agent straight away
(same control layer as the optional `openbackup` CLI).

## What each screen does

| Screen | |
| --- | --- |
| **Overview** | The one answer that matters: everything is backed up, or here is what is wrong. Last backup, files protected, size, versions kept. Back up now, pause for an hour |
| **Folders** | What is backed up, with the folders found on this computer that are not yet included and a button for each. Pause or remove one, or open it in the file manager |
| **Restore** | Browse any backup one folder at a time, search by name, restore to a folder you pick, with progress and a cancel button |
| **Settings** | Upload ceiling, the CPU threshold, and the pause rules for metered connections, battery and full-screen apps. End-to-end encryption, including the recovery code. **Log out** to disconnect this device |
| **Diagnostics** | The same checks as `openbackup doctor`: configuration, service, server, folders, backups. Plus a link to the logs and to the dashboard |

## The tray icon

The icon reflects state at a glance — everything backed up, working, idle, or needs
attention — and its menu offers Open, Back up now, Pause and Resume. Closing the
window leaves it there; Quit from the menu closes the window for real, and still
does not stop the service.

Notifications appear only when something needs you: the service stopped, an error,
or backups have gone stale. Not for successful backups, which would be a
notification every few minutes saying nothing happened.

## Resource use

About 10 MB of RAM and no measurable CPU while open, because it holds no backup
state — it polls the agent for status and calls operations. Closed, it costs
nothing; the service does the work.

## Building it

```bash
make desktop                 # build for this machine
make desktop-dev             # live-reloading development window
make desktop-linux-package   # Linux only: release-named binary + .desktop + icon
```

On Windows, `./scripts/build.ps1 -Desktop`, or `-Installer` to package it with
NSIS. On Linux, `scripts/install-agent.sh` installs a released desktop binary
when one is published. Each platform's app is built on that platform: there is
no cross-compiling a native webview.

Built with [Wails](https://wails.io) — Go for the logic, React and TypeScript for
the window, sharing the dashboard's design system. It lives in
[`desktop/`](../desktop/README.md) as its own Go module so that the server and agent
CLI stay pure-Go cross-compiles.
