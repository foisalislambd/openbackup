# OpenBackup

Automatic backups of your own files to a server you own. Install it on a VPS with
one command, run one command on each computer, and stop thinking about it.

It backs up your documents, pictures, videos, music and desktop. It does not back
up Windows, `/usr`, installed programs, browser caches, or `node_modules` — and it
tells you exactly what it skipped and why.

Free, MIT licensed, no paid tier, nothing phoning home. Windows, macOS and Linux,
with a desktop app for people who would rather not use a terminal.

```bash
# On your server
curl -fsSL https://raw.githubusercontent.com/openbackup/openbackup/main/scripts/install-server.sh | sh

# On each computer, using the code the dashboard gives you
curl -fsSL https://backup.example.com/install.sh | sh
openbackup connect --server https://backup.example.com --code XXXX-XXXX-XXXX
```

There is nothing else to configure. The agent finds your folders, watches them for
changes, and uploads only the parts of files that actually changed.

**New here?** [Install the server](docs/install-server.md) →
[install an agent](docs/install-agent.md) → [restore something](docs/restoring.md)
today, while nothing is wrong. Full documentation is in
[`docs/`](docs/README.md).

## Contents

- [Why another backup tool](#why-another-backup-tool)
- [Getting started](#getting-started)
- [How it works](#how-it-works)
- [What is not backed up](#what-is-not-backed-up)
- [Everyday use](#everyday-use)
- [Restoring](#restoring)
- [End-to-end encryption](#end-to-end-encryption)
- [The desktop app](#the-desktop-app)
- [Running the server](#running-the-server)
- [Documentation](#documentation)
- [Building from source](#building-from-source)
- [Project layout](#project-layout)
- [Status](#status)
- [Contributing](#contributing)
- [Licence](#licence)

## Why another backup tool

Most backup tools are either a subscription with your files on someone else's
computer, or a toolkit that expects you to design a backup strategy, write a
schedule, and remember to test restores. This aims at the gap between them: your
own server, and no decisions required to get a correct backup.

The parts that usually go wrong are the ones handled by default:

- **It backs up the right things.** Personal folders are detected per platform.
  System directories are excluded, and so are caches and build output. A folder
  named `build` inside a photo library is backed up; the same name inside a
  project with a `package.json` beside it is not.
- **It does not get in your way.** The agent pauses while you are gaming or on
  battery, throttles itself on a metered connection, and yields when the CPU is
  busy. Idle cost is a few megabytes of RAM and no measurable CPU.
- **It survives being interrupted.** Uploads resume, a half-finished backup is
  cleaned up rather than left to rot, and every block is verified against its
  hash.
- **Restores are the point.** Browse any backup in the dashboard, download a file
  or a folder as a ZIP, or restore on the device with `openbackup restore`.

## Getting started

You need a machine with Docker that your computers can reach — a small VPS is
plenty — and enough disk for what you keep.

**1. The server.** One container, one volume. The database is SQLite inside that
volume and the dashboard is embedded in the binary, so there is no second service
to run.

```bash
curl -fsSL https://raw.githubusercontent.com/openbackup/openbackup/main/scripts/install-server.sh | sh
```

It writes a Compose file to `/opt/openbackup`, generates an admin password, waits
until the server actually answers, and prints the dashboard address and
credentials. Prefer to do it by hand, or put TLS in front of it (you should):
[docs/install-server.md](docs/install-server.md).

**2. A connection code.** Sign in, open **Devices**, create one. Single-use, valid
for 24 hours.

**3. Each computer.** On Linux and macOS the installer is served by your own
server, so the download and the address the agent will talk to are the same host:

```bash
curl -fsSL https://backup.example.com/install.sh | sh
openbackup connect --server https://backup.example.com --code ABCD-EFGH-JKLM
```

On Windows, download the installer from the
[releases page](https://github.com/openbackup/openbackup/releases) and let the app
walk you through it.

**4. Check it.** `openbackup status` says whether your files are safe;
`openbackup doctor` checks the whole chain. Then read
[docs/restoring.md](docs/restoring.md) and restore one file — a backup nobody has
restored is a hypothesis.

## How it works

Files are split into variable-sized chunks (FastCDC), each identified by its
BLAKE3 hash. The agent asks the server which chunks it does not already have, and
uploads only those, compressed with Zstandard. Identical data is stored once,
whether it repeats inside one file, across your machines, or across time — so
keeping ninety days of history usually costs a small fraction of ninety copies.

A backup ("snapshot") is a list of paths pointing at chunks. Snapshots after the
first are deltas against their parent, which is why a daily backup of a large home
directory takes seconds.

With end-to-end encryption enabled (`openbackup encrypt`), chunks are
encrypted on the device with XChaCha20-Poly1305 before upload. The server then
holds data it cannot read, which also means browser-side restore is unavailable
for those backups — the key never leaves your machines, so only they can decrypt.

```
┌──────────────┐   HTTPS + JSON     ┌──────────────────────────┐
│    agent     │ ─────────────────▶ │          server          │
│              │                    │                          │
│ watch → chunk│  "which of these   │  SQLite metadata         │
│ → hash → zip │   hashes are new?" │  content-addressed blobs │
│ → encrypt    │ ◀───────────────── │  retention + GC          │
└──────────────┘   only new chunks  │  dashboard (embedded)    │
                                    └──────────────────────────┘
```

[docs/architecture.md](docs/architecture.md) has the rest: the local index, delta
resolution, the block store, and the invariants the design protects.

## What is not backed up

Excluded by default, with a reason attached to every rule (visible in
**Settings → What is not backed up**, or with `openbackup rules`):

| Category | Examples |
| --- | --- |
| Operating system | `C:\Windows`, `Program Files`, `/proc`, `/sys`, `/usr`, `/System` |
| Junk | `$Recycle.Bin`, `.Trash`, `Thumbs.db`, `.DS_Store` |
| Caches | browser caches, `~/.cache`, package manager caches |
| Developer | `node_modules`, `vendor`, `target`, `dist`, `.next`, `venv`, `__pycache__` |
| Virtual machines | `.vdi`, `.vmdk`, `.qcow2`, Docker images |
| Temporary | `*.tmp`, `*.part`, crash dumps, swap files |

Developer exclusions are scoped: they apply inside a directory that looks like a
project (one of 32 marker files such as `package.json`, `go.mod`, `Cargo.toml`),
so source code is always backed up and a coincidentally named personal folder is
not skipped. Add anything back with `openbackup folders add <path>`, or turn off a
whole category — [docs/ignore-rules.md](docs/ignore-rules.md).

## Everyday use

```bash
openbackup status                             # is my data backed up?
openbackup backup                             # back up right now
openbackup folders                            # what it found, and what it skips
openbackup folders add ~/Projects             # include something else
openbackup pause --for 2h                     # stop for a while
openbackup resume
openbackup limit --upload 5MB                 # cap the upload speed
openbackup doctor                             # check that everything works
```

Changes apply to the running background service immediately. Every command is in
[docs/cli.md](docs/cli.md); what the agent decides on its own — and why an idle
agent is usually correct rather than broken — is in
[docs/backing-up.md](docs/backing-up.md).

## Restoring

Three routes, and all of them refuse to overwrite an existing file unless you say
so:

```bash
openbackup find "tax return"                        # where did it live?
openbackup restore --path Documents/report.docx --to .
openbackup restore --snapshot snp_06fss... --to ./recovered
```

From the **dashboard**, browse any point in time and download a file or a folder
as a ZIP — nothing installed, works from someone else's computer. From the
**desktop app**, browse and restore with a native folder picker.

Restoring onto a *new* machine works without the old one: any device on the
account can read the account's backups, which is the case that actually matters.
[docs/restoring.md](docs/restoring.md).

## End-to-end encryption

```bash
openbackup encrypt
```

Chunks are encrypted on the device before upload, and the printed recovery code is
the only key. Write it down somewhere other than the machine it protects: with
encryption on, a lost code means a lost backup, and there is no escrow and no
reset. Deduplication still works, including across your devices.

What you give up is browser-based restore, because the server genuinely cannot
read the data. [docs/encryption.md](docs/encryption.md), and
[docs/security-model.md](docs/security-model.md) for what the system does and does
not defend against.

## The desktop app

![The desktop app's overview screen](docs/images/desktop-overview.png)

A window for the machine being backed up: connect a device, see whether your files
are safe, browse and restore backups, change limits and encryption, run
diagnostics. It closes to the notification area, where the tray icon shows the
current state and offers Back up now, Pause and Resume.

It is a thin client — the background service does the work, so closing the window
stops nothing, and a change made here reaches the running service straight away.
Idle it costs about 10 MB of RAM.

Built with [Wails](https://wails.io): Go for the logic, React and TypeScript for
the window. [docs/desktop-app.md](docs/desktop-app.md).

## Running the server

Configuration is environment variables, all optional:

| Variable | Default | Purpose |
| --- | --- | --- |
| `OPENBACKUP_ADDR` | `:8080` | Listen address |
| `OPENBACKUP_DATA_DIR` | `./data` | Database and blobs |
| `OPENBACKUP_PUBLIC_URL` | — | External URL; enables Secure cookies when https |
| `OPENBACKUP_ADMIN_EMAIL` / `_PASSWORD` | — | Create the first account on an empty database |
| `OPENBACKUP_TRUST_PROXY` | `false` | Honour `X-Forwarded-For` (set when proxied) |
| `OPENBACKUP_RETENTION_DAYS` | `30` | Default retention; `0` keeps everything |
| `OPENBACKUP_QUOTA_BYTES` | `0` | Per-account limit, e.g. `500GB` |
| `OPENBACKUP_REQUIRE_ENCRYPTION` | `false` | Refuse unencrypted uploads |
| `OPENBACKUP_S3_*` | — | Store blobs in S3/MinIO instead of the volume |

The full list is in [docs/configuration.md](docs/configuration.md). Run it behind
a reverse proxy that terminates TLS: device tokens travel in headers, so plain
HTTP is only acceptable on a network you control.

```bash
openbackup-server invite            # print an enrolment code
openbackup-server check --fix       # verify stored data against the index
openbackup-server user add --email you@example.com
openbackup-server health            # used by the container healthcheck
```

Retention, garbage collection, stale snapshot cleanup and log pruning run
themselves once an hour. What you do have to do is back up the server's volume —
it holds the only copy of your backups.
[docs/operations.md](docs/operations.md).

## Documentation

| | |
| --- | --- |
| [Install the server](docs/install-server.md) | VPS, Compose, TLS, S3 |
| [Install an agent](docs/install-agent.md) | Windows, macOS, Linux, services |
| [Backing up](docs/backing-up.md) | Folders, timing, limits, retention |
| [Restoring](docs/restoring.md) | Dashboard, CLI, app, a lost machine |
| [What is not backed up](docs/ignore-rules.md) | Rules, project detection, overrides |
| [Encryption](docs/encryption.md) | Recovery codes and trade-offs |
| [Configuration](docs/configuration.md) | Every server variable and agent setting |
| [CLI reference](docs/cli.md) | Both commands, flag by flag |
| [HTTP API](docs/api.md) | Endpoints and the auth model |
| [Architecture](docs/architecture.md) | How the pieces fit |
| [Security model](docs/security-model.md) | Threats, keys, hardening |
| [Operating a server](docs/operations.md) | Upgrades, backups, monitoring |
| [Troubleshooting](docs/troubleshooting.md) | When something is wrong |
| [FAQ](docs/faq.md) | Short answers, including the awkward ones |
| [Development](docs/development.md) | Build it, test it, release it |

## Building from source

Needs Go 1.26+ and Node 22+.

```bash
make            # build the dashboard, embed it, build the binaries
make test       # run the tests
make check      # what CI runs: gofmt, vet, tests
make release    # cross-compiled binaries in ./dist
make desktop    # the desktop app for this machine (needs the Wails CLI)
```

On Windows, `./scripts/build.ps1` does the same thing.

The dashboard lives in [`web/`](web/README.md) and is a Next.js static export
copied into `internal/server/web/dist`, where `//go:embed` picks it up. `go build`
alone produces a working server; it just uses whatever dashboard was last built.
More in [docs/development.md](docs/development.md).

## Project layout

```
cmd/openbackup/          agent CLI and background service
cmd/openbackup-server/   server
internal/agent/          config, index, scanner, watcher, uploader, governor,
                         engine, restore, IPC, control
internal/server/         store (SQLite + blobs), httpapi, auth, maintenance
internal/api/            the wire protocol, shared by both sides
internal/chunk/          FastCDC content-defined chunking
internal/codec/          Zstandard + XChaCha20-Poly1305
internal/ignore/         exclusion rules and project detection
internal/userdirs/       per-platform personal folder detection
web/                     dashboard
desktop/                 Wails desktop app (its own module)
docs/                    documentation
```

## Status

The server, the agent, the dashboard and the desktop app work end to end:
enrolment, full and incremental backups, deduplication, quotas, retention,
garbage collection, integrity checking, browser restore and device restore.
Android is not built yet. See [CHANGELOG.md](CHANGELOG.md) for what changed.

## Contributing

Bug reports, restore stories from unusual setups, and wording fixes are all
welcome — [CONTRIBUTING.md](CONTRIBUTING.md) explains the house style and what a
good pull request looks like. Questions belong in
[Discussions](https://github.com/openbackup/openbackup/discussions); see
[SUPPORT.md](SUPPORT.md).

Found a security problem? Report it privately: [SECURITY.md](SECURITY.md).

## Licence

MIT — see [LICENSE](LICENSE).
