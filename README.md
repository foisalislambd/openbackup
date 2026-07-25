# OpenBackup

Automatic backups of your own files to a server you own. Install it on a VPS with
one command, run one command on each computer, and stop thinking about it.

It backs up your documents, pictures, videos, music and desktop. It does not back
up Windows, `/usr`, installed programs, browser caches, or `node_modules` — and it
tells you exactly what it skipped and why.

```bash
# On your server
curl -fsSL https://raw.githubusercontent.com/openbackup/openbackup/main/scripts/install-server.sh | sh

# On each computer, using the code the dashboard gives you
curl -fsSL https://backup.example.com/install.sh | sh
openbackup connect --server https://backup.example.com --code XXXX-XXXX-XXXX
```

There is nothing else to configure. The agent finds your folders, watches them for
changes, and uploads only the parts of files that actually changed.

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

## How it works

Files are split into variable-sized chunks (FastCDC), each identified by its
BLAKE3 hash. The agent asks the server which chunks it does not already have, and
uploads only those, compressed with Zstandard. Identical data is stored once,
whether it repeats inside one file, across your machines, or across time — so
keeping ninety days of history usually costs a small fraction of ninety copies.

A backup ("snapshot") is a list of paths pointing at chunks. Snapshots after the
first are deltas against their parent, which is why a daily backup of a large home
directory takes seconds.

With end-to-end encryption enabled (`openbackup encrypt --enable`), chunks are
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

## What is not backed up

Excluded by default, with a reason attached to every rule (visible in
**Settings → What is not backed up**):

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
not skipped. Add anything back with `openbackup folders add <path>`.

## Server

One container, one volume. The database is SQLite inside the volume and the
dashboard is embedded in the binary, so there is no second service to run.

```bash
docker compose up -d
```

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

Run it behind a reverse proxy that terminates TLS. Device tokens travel in
headers, so plain HTTP is only acceptable on a network you control.

Other server commands:

```bash
openbackup-server invite            # print an enrolment code
openbackup-server check --fix       # verify stored data against the index
openbackup-server user add --email you@example.com
openbackup-server health            # used by the container healthcheck
```

## Agent

```bash
openbackup connect --server URL --code CODE   # one-time enrolment
openbackup status                             # what it is doing right now
openbackup folders                            # what it found, and what it skips
openbackup folders add ~/Projects             # include something else
openbackup backup                             # back up now, do not wait
openbackup snapshots                          # list backups
openbackup find "tax return"                  # search across backups
openbackup restore --snapshot ID --to ./out   # restore
openbackup encrypt --enable                   # end-to-end encryption
openbackup limit --upload 2MB                 # cap upload speed
openbackup pause 2h                           # stop for a while
openbackup service install                    # run in the background at login
openbackup doctor                             # diagnose problems
```

## Building from source

Needs Go 1.25+ and Node 22+.

```bash
make            # build the dashboard, embed it, build the binaries
make test       # run the tests
make check      # what CI runs: gofmt, vet, tests
make release    # cross-compiled binaries in ./dist
```

On Windows, `./scripts/build.ps1` does the same thing.

The dashboard lives in [`web/`](web/README.md) and is a Next.js static export
copied into `internal/server/web/dist`, where `//go:embed` picks it up. `go build`
alone produces a working server; it just uses whatever dashboard was last built.

## Layout

```
cmd/openbackup/          agent CLI and background service
cmd/openbackup-server/   server
internal/agent/          config, index, scanner, watcher, uploader, governor,
                         engine, restore, IPC
internal/server/         store (SQLite + blobs), httpapi, auth, maintenance
internal/chunk/          FastCDC content-defined chunking
internal/codec/          Zstandard + XChaCha20-Poly1305
internal/ignore/         exclusion rules and project detection
internal/userdirs/       per-platform personal folder detection
web/                     dashboard
```

## Status

The server, the agent and the dashboard work end to end: enrolment, full and
incremental backups, deduplication, quotas, retention, garbage collection,
integrity checking, browser restore and device restore. Android is not built yet.

## License

MIT — see [LICENSE](LICENSE).
