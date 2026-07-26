# Installing an agent

The agent runs on the computer whose files you want backed up. It finds your
personal folders, watches them for changes, and uploads what changed. Until you
connect it to a server it does nothing at all.

You need a connection code first: open your dashboard, go to **Devices**, and
create one. Codes look like `ABCD-EFGH-JKLM`, are single-use, and expire after 24
hours by default.

## Linux and macOS

```bash
curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh
```

On **Linux**, after the agent is installed the script asks if you also want the
desktop app (GUI). Answer `y` to download and set it up (`openbackup-desktop` +
app-menu entry), or `N` for the command line only. Then open the app, or:

```bash
openbackup connect --server https://backup.example.com --code ABCD-EFGH-JKLM
```

Without a prompt:

```bash
OPENBACKUP_DESKTOP=1 curl -fsSL …/install-agent.sh | sh   # always install GUI
OPENBACKUP_SKIP_DESKTOP=1 curl -fsSL …/install-agent.sh | sh   # never
```

Same pattern as the server installer: one script from this repo on GitHub. It
prefers a published release binary; if none exists yet, it clones the repository
and builds the agent (downloading a temporary Go toolchain if needed). Then it
registers a background service and stops.

Optional overrides: `OPENBACKUP_FORCE_BUILD=1`, `OPENBACKUP_REPO`, `OPENBACKUP_REF`
(default `main`), `OPENBACKUP_GO_VERSION`, `OPENBACKUP_VERSION`,
`OPENBACKUP_DESKTOP=1`, `OPENBACKUP_SKIP_DESKTOP=1`.

Where it installs depends on how you run it:

- **As root:** `/usr/local/bin/openbackup`, running as a system service. Use this
  for a machine with several users, or a server.
- **As yourself (no sudo):** `~/.local/bin/openbackup`, running as a user
  service. This is the better fit for a personal machine — the agent backs up
  your files, so it only needs your permissions.

If `~/.local/bin` is not on your `PATH`, the script says so and tells you what to
add.

## Windows

Download **one** of these from the
[releases page](https://github.com/foisalislambd/openbackup/releases):

- `openbackup-desktop-windows-amd64-setup.exe` (recommended installer), or
- `openbackup-desktop-windows-amd64.exe` (portable)

That single app is the window **and** the background agent. Open it, connect with
a dashboard code, then use **Start service** when asked — you do not install a
separate `openbackup.exe`.

If you prefer the terminal (or a headless machine), the optional CLI is still
available:

```powershell
openbackup connect --server https://backup.example.com --code ABCD-EFGH-JKLM
openbackup service install
```

## What connecting does

`openbackup connect` exchanges the code for a device token and writes the
configuration, including the folders to back up: on a fresh install Desktop,
Documents, Pictures, Videos, Music and Downloads, wherever those actually live on
your system — the agent asks the operating system rather than guessing at English
path names.

It does not start backing up on its own, and says so: install the service to have
it happen from now on, or run `openbackup backup` for one backup right now.

Useful flags:

```bash
openbackup connect --server URL --code CODE \
  --name "Work laptop" \        # what it is called in the dashboard
  --encrypt                     # turn on end-to-end encryption from the start
```

`--encrypt` prints a recovery code. Write it down before continuing; see
[encryption.md](encryption.md) for what it protects and what it costs.

## Check that it worked

```bash
openbackup status     # what it is doing right now
openbackup folders    # what it found, and what it skips
openbackup doctor     # the whole chain: config, service, server, folders, backups
```

`doctor` is the one to run when something looks wrong, and the one to paste into
an issue.

## Running in the background

The installers register a service for you. To do it by hand, or to check on it:

```bash
openbackup service install     # run from login onwards
openbackup service status
openbackup service restart
openbackup service uninstall
```

This uses the native mechanism on each platform: a Windows service, a systemd
unit, or a launchd agent. There is nothing OpenBackup-specific to learn, and
nothing left behind after `uninstall`.

To watch it work instead, stop the service and run `openbackup run` in a
terminal.

## Where things are stored

| | |
| --- | --- |
| Configuration | `%AppData%\OpenBackup\config.json` (Windows), `~/Library/Application Support/OpenBackup/config.json` (macOS), `~/.config/OpenBackup/config.json` (Linux) |
| Local index and logs | `%LocalAppData%\OpenBackup` (Windows), `~/Library/Caches/OpenBackup` (macOS), `~/.cache/OpenBackup` (Linux) |

The two are separate deliberately: the state directory is a cache, so you can
delete it to force a full rescan without losing your enrolment. The
configuration holds the device token and, if encryption is on, the recovery code —
it is written owner-readable only.

Override either with `OPENBACKUP_CONFIG` and `OPENBACKUP_STATE_DIR`, which is how
you run a second agent side by side for testing.

## Adding more computers

Repeat the same steps with a new code. Devices on one account deduplicate against
each other, so the second laptop with the same holiday photos costs almost
nothing, and any device can restore any other device's files — which is what you
want when the machine you are restoring is the one that died.

## Removing an agent

```bash
openbackup service uninstall
```

Then delete the binary, the configuration and the state directory. To remove the
device's backups too, delete it in the dashboard under **Devices**; the storage
comes back on the next garbage collection pass.

## Next

- [Backing up](backing-up.md) — adding folders, limits, and when it runs
- [What is not backed up](ignore-rules.md) — and how to override it
- [Restoring](restoring.md) — do this once now, not for the first time in an
  emergency
