# The desktop app

A window for the machine being backed up, for people who would rather not use a
terminal. It connects a device, shows whether the files are safe, browses and
restores backups, changes limits and encryption, and runs the same diagnostics as
`openbackup doctor`.

Available for Windows, macOS and Linux. Same screens and design on every platform.

- **Windows:** download the installer from the
  [releases page](https://github.com/foisalislambd/openbackup/releases).
- **Linux:** install with one command (agent CLI + desktop window + app-menu entry):

  ```bash
  curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-desktop-linux.sh | sh
  ```

  Or build locally: `make desktop` then `./scripts/install-desktop-linux.sh`.
- **macOS:** build with `make desktop` for now (same Wails app).

## It is a client, not the backup

The backing up is done by the agent service in the background. The window is
optional: closing it stops nothing, and it hides to the notification area rather
than quitting. If the service is not installed, the app says so and offers to
install it — that is the correct behaviour, not a failure.

Anything changed in the window is applied to the running service straight away,
because the window and the CLI go through the same control layer. A folder added
here and a folder added with `openbackup folders add` take exactly the same path.

Installing, starting and stopping the service is the one thing the app does not
reimplement: it runs the `openbackup` command, found next to itself or on your
`PATH`. One implementation of "install the service" means one set of bugs, and the
window and the terminal stay in agreement.

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
NSIS. On Linux, `scripts/install-desktop-linux.sh` installs a built or released
binary into `~/.local` and adds an app-menu entry. Each platform's app is built
on that platform: there is no cross-compiling a native webview.

Built with [Wails](https://wails.io) — Go for the logic, React and TypeScript for
the window, sharing the dashboard's design system. It lives in
[`desktop/`](../desktop/README.md) as its own Go module so that the server and agent
stay pure-Go cross-compiles.
