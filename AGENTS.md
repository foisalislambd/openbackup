# OpenBackup — agent notes

Quick reference for running and developing this repo locally. Deeper detail lives in [`docs/development.md`](docs/development.md).

## What this project is

Self-hosted backup: a **Go server**, a **Vite/React web dashboard**, a **CLI agent**, and an optional **Wails desktop app**.

| Piece | Path | Local URL / notes |
| --- | --- | --- |
| Server (API + embedded UI in prod) | `cmd/openbackup-server` | `http://localhost:18200` |
| Web dashboard (dev) | `web/` | `http://localhost:5173` (proxies `/api` → `:18200`) |
| CLI agent | `cmd/openbackup` | Talks to the server after enrol |
| Desktop app | `desktop/` | Separate Go module (Wails); needs a running server |

## Prerequisites

- Go **1.26+**
- Node **22+** (dashboard + desktop frontend)
- Optional: Wails CLI for the desktop window — `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

## Run the dev servers

You need **two processes**: API server and dashboard.

### 1. Backend (API)

From the repo root:

```bash
go run ./cmd/openbackup-server
```

Listens on **`:18200`**. Data defaults to `./data` (blobs + `openbackup.db`).

Throwaway data (recommended so you do not touch a real install):

```bash
# Unix / Git Bash
OPENBACKUP_DATA_DIR=/tmp/ob/data OPENBACKUP_ADDR=127.0.0.1:18200 go run ./cmd/openbackup-server
```

```powershell
# Windows PowerShell
$env:OPENBACKUP_DATA_DIR = "$env:TEMP\ob\data"
$env:OPENBACKUP_ADDR = "127.0.0.1:18200"
go run ./cmd/openbackup-server
```

### 2. Web dashboard

```bash
cd web
npm install          # first time only
npm run dev          # http://localhost:5173
```

Point the Vite proxy elsewhere with `OPENBACKUP_DEV_SERVER=http://host:port` if needed.

Or from the repo root (POSIX `make`): `make dev` starts server + dashboard together.

On Windows, prefer the two terminals above (or `./scripts/build.ps1` for builds — the Makefile expects a POSIX shell).

## Username and password

**There is no default login.**

- Fresh database → dashboard shows **first-run setup**. Create the admin email + password in the browser at `http://localhost:5173`.
- Or create an account from the CLI (same data dir the server uses):

```bash
# Unix
OPENBACKUP_DATA_DIR=/tmp/ob/data go run ./cmd/openbackup-server user add --email dev@example.com --password a-long-enough-password
```

```powershell
# Windows PowerShell (match OPENBACKUP_DATA_DIR to the running server)
$env:OPENBACKUP_DATA_DIR = "$env:TEMP\ob\data"
go run ./cmd/openbackup-server user add --email dev@example.com --password a-long-enough-password
```

Suggested local credentials (only for throwaway dev):

| Field | Value |
| --- | --- |
| Email | `dev@example.com` |
| Password | `a-long-enough-password` |

Optional env vars on an **empty** database also seed the first admin: `OPENBACKUP_ADMIN_EMAIL` + `OPENBACKUP_ADMIN_PASSWORD` (ignored once an account exists). See [`docs/configuration.md`](docs/configuration.md).

Check whether setup is still needed:

```bash
curl -s http://localhost:18200/api/v1/ui/bootstrap
# "needs_setup": true  → create account first
```

## Reset local data (dev only, when the user asks)

When the user asks to reset / wipe / clear local data in development:

1. **Stop the server first** (and the CLI agent / desktop app if they are running), so SQLite is not locked.
2. Delete the **server data directory** the running process used:
   - default: `./data` under the repo root (`openbackup.db`, `blobs/`, WAL/SHM sidecars)
   - or whatever `OPENBACKUP_DATA_DIR` was set to (e.g. `/tmp/ob/data`, `%TEMP%\ob\data`)
3. Optionally also wipe **agent/desktop** throwaway state so devices do not keep stale credentials:
   - `OPENBACKUP_CONFIG` file and `OPENBACKUP_STATE_DIR` (e.g. `/tmp/ob/config.json` + `/tmp/ob/state`, or `%TEMP%\ob\...`)
4. Start the server again. Bootstrap will show `needs_setup: true` — recreate the admin account (browser first-run or `user add`).
5. Create a new invite and re-enrol any agent/desktop if needed.

```bash
# Unix — default ./data
rm -rf ./data

# Unix — throwaway layout from the docs
rm -rf /tmp/ob
```

```powershell
# Windows PowerShell — default ./data (from repo root)
Remove-Item -Recurse -Force .\data -ErrorAction SilentlyContinue

# Windows — throwaway layout
Remove-Item -Recurse -Force "$env:TEMP\ob" -ErrorAction SilentlyContinue
```

Do **not** reset data unless the user explicitly asks. Never delete production / VPS paths (e.g. `/var/lib/openbackup`, Docker volumes) as part of a “dev reset”.

## Device connection code (agent / desktop)

After you have an account:

```bash
go run ./cmd/openbackup-server invite
# use the same OPENBACKUP_DATA_DIR as the running server
```

Enrol the CLI agent against a throwaway config:

```bash
# Unix
export OPENBACKUP_CONFIG=/tmp/ob/config.json
export OPENBACKUP_STATE_DIR=/tmp/ob/state
go run ./cmd/openbackup connect --server http://127.0.0.1:18200 --code CODE --name "Dev box"
```

```powershell
# Windows PowerShell
$env:OPENBACKUP_CONFIG = "$env:TEMP\ob\config.json"
$env:OPENBACKUP_STATE_DIR = "$env:TEMP\ob\state"
go run ./cmd/openbackup connect --server http://127.0.0.1:18200 --code CODE --name "Dev box"
```

## Desktop application (dev)

Separate module under `desktop/`. It needs a **running server** and should use throwaway `OPENBACKUP_CONFIG` / `OPENBACKUP_STATE_DIR` (same idea as the CLI agent).

```bash
make desktop-dev     # live-reloading Wails window
make desktop         # release build → desktop/build/bin
make desktop-check   # go vet + tsc
```

On Windows without `make`, use Wails from `desktop/` (see [`desktop/README.md`](desktop/README.md)). Build with **Wails**, not plain `go build`.

## Build / test (short)

```bash
make            # dashboard embed + binaries → ./bin
make test       # Go tests
make check      # what CI cares about locally
```

Windows:

```powershell
./scripts/build.ps1
./scripts/build.ps1 -SkipWeb -Test
./scripts/build.ps1 -Desktop
```

```bash
go test ./cmd/... ./internal/...
cd web && npm run typecheck && npm run lint
```

Avoid `go test ./...` — `web/node_modules` can contain unrelated Go files.

## Where code lives

| Area | Start here |
| --- | --- |
| Dashboard UI | `web/src/` |
| HTTP API | `internal/server/httpapi/` |
| Persistence / blobs | `internal/server/store/` |
| Agent backup engine | `internal/agent/` |
| Architecture overview | [`docs/architecture.md`](docs/architecture.md) |

## Agent behaviour

- Do not commit unless the user asks.
- Prefer throwaway `OPENBACKUP_DATA_DIR` / agent config dirs for local runs.
- Do not invent a default password — use first-run setup or `user add`.
- Reset / wipe local data **only when the user asks** (see section above); stop processes first, then delete the matching data dirs.
- Full docs: [`docs/development.md`](docs/development.md), [`web/README.md`](web/README.md), [`desktop/README.md`](desktop/README.md).
