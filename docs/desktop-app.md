# The desktop app

One app for the machine being backed up — window, tray, and backup engine in a
**single binary** (Dropbox-style). Connect a device, see whether files are safe,
browse and restore, change limits and encryption, and run diagnostics.

Closing the window hides to the tray; backups keep running in the same process.
**Quit** from the tray stops backups until you open the app again (or sign in —
login autostart launches the app with `--background`).

Available for Windows, macOS and Linux. Same screens and design on every platform.

- **Windows:** download the installer (or the portable `.exe`) from the
  [releases page](https://github.com/foisalislambd/openbackup/releases). That
  single app is enough — you do **not** install a separate agent, and you do
  **not** need Run as administrator.
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

## One binary

| How you start it | What happens |
| --- | --- |
| Double-click / app menu | Opens the window; if connected, backups run in this process |
| Close the window | Hides to the tray — backups continue |
| Quit (tray) | Stops backups and exits |
| Sign in / login | App starts hidden (`--background`) and resumes backups |

Login autostart is a per-user entry (Windows Run key, Linux XDG autostart, macOS
LaunchAgent). Headless / server installs can still use `openbackup service`.

## What each screen does

| Screen | |
| --- | --- |
| **Overview** | The one answer that matters: everything is backed up, or here is what is wrong. Last backup, files protected, size, versions kept. Back up now, pause for an hour |
| **Folders** | What is backed up, with the folders found on this computer that are not yet included and a button for each. Pause or remove one, or open it in the file manager |
| **Restore** | Browse any backup one folder at a time, search by name, restore to a folder you pick, with progress and a cancel button |
| **Settings** | Upload ceiling, the CPU threshold, and the pause rules for metered connections, battery and full-screen apps. End-to-end encryption, including the recovery code. **Check for updates** downloads the latest installer from GitHub Releases. **Log out** to disconnect this device |
| **Diagnostics** | The same checks as `openbackup doctor`: configuration, service, server, folders, backups. Plus a link to the logs and to the dashboard |

## Updating on Windows

1. In the app: **Settings → Check for updates → Download update**, or get
   `openbackup-desktop-windows-amd64-setup.exe` from
   [Releases](https://github.com/foisalislambd/openbackup/releases).
2. Run the new installer. It stops the running app (window + agent), replaces
   the files, and keeps your connection settings.
3. **Uninstall** (Windows Apps or Start Menu) stops the app, removes login
   autostart, and deletes local OpenBackup data on that PC. Server-side backups
   are not deleted.

The installer and the in-app version string both use the GitHub release tag
(for example `v0.2.1` / product version `0.2.1`).

## The tray icon

The icon reflects state at a glance — everything backed up, working, idle, or needs
attention — and its menu offers Open, Back up now, Pause, Resume, and Quit.
Closing the window leaves it there; Quit stops the in-process engine and exits.

Notifications appear only when something needs you: backups stopped, an error,
or backups have gone stale. Not for successful backups, which would be a
notification every few minutes saying nothing happened.

## Resource use

While the app is open (or in the tray), it runs the backup engine in-process.
About tens of MB of RAM depending on activity; idle cost is low.

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
