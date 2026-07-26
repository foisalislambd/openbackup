# Development

How to build this, run it locally, and test a change. For style and pull request
expectations, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Prerequisites

- **Go 1.26+**
- **Node 22+** for the dashboard and the desktop window
- Optional: the [Wails CLI](https://wails.io) for the desktop app,
  `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

No CGO, no database server, no code generation step.

## Build

```bash
make            # dashboard + embed + binaries into ./bin
make build      # binaries only, reusing the embedded dashboard
make web        # dashboard only
make test       # tests
make check      # gofmt, vet, tests: what CI runs
make clean
```

On Windows the Makefile needs a POSIX shell, so use the PowerShell script:

```powershell
./scripts/build.ps1                 # dashboard + binaries
./scripts/build.ps1 -SkipWeb -Test  # binaries and tests only
./scripts/build.ps1 -Desktop        # also the desktop app
./scripts/build.ps1 -Installer      # ... packaged with NSIS
```

`go build ./cmd/...` on its own works too: it embeds whatever dashboard was last
built, or a placeholder page telling you to run `make web`.

## A local end-to-end setup

Three terminals, and a configuration that cannot disturb a real backup.

**1. The server**, against a throwaway data directory:

```bash
OPENBACKUP_DATA_DIR=/tmp/ob/data \
OPENBACKUP_ADDR=127.0.0.1:18200 \
go run ./cmd/openbackup-server
```

Then create an account and a connection code:

```bash
OPENBACKUP_DATA_DIR=/tmp/ob/data go run ./cmd/openbackup-server user add --email dev@example.com --password a-long-enough-password
OPENBACKUP_DATA_DIR=/tmp/ob/data go run ./cmd/openbackup-server invite
```

**2. An agent**, pointed at its own config and a folder of test files:

```bash
export OPENBACKUP_CONFIG=/tmp/ob/config.json
export OPENBACKUP_STATE_DIR=/tmp/ob/state

mkdir -p /tmp/ob/home/Documents && echo hello > /tmp/ob/home/Documents/a.txt

go run ./cmd/openbackup connect --server http://127.0.0.1:18200 --code CODE --name "Dev box"
go run ./cmd/openbackup folders add /tmp/ob/home
go run ./cmd/openbackup backup
go run ./cmd/openbackup run          # the daemon, in the foreground
```

Those two environment variables are the whole isolation story: the agent touches
nothing else, and deleting `/tmp/ob` resets everything.

**3. The dashboard**, with hot reload, proxying the API to the server:

```bash
cd web && npm install && npm run dev      # http://localhost:5173
```

Or `make dev`, which starts the server and the dashboard together.

## The desktop app

```bash
make desktop-dev     # live-reloading window
make desktop         # release build into desktop/build/bin
make desktop-check   # go vet and tsc
```

Point it at the same throwaway config with `OPENBACKUP_CONFIG` and
`OPENBACKUP_STATE_DIR`. It needs a server to talk to, and it expects the agent
service to be running — without one it says so, which is the correct behaviour, not
a bug.

Build it with `wails build`, never plain `go build`: Wails refuses to run a binary
built without its tags, and the failure is a dialog rather than a compile error.
[`desktop/README.md`](../desktop/README.md) has the details, including why it is a
separate Go module.

## Tests

```bash
go test ./cmd/... ./internal/...      # or: make test
go test -race ./internal/...          # make test-race
go test ./internal/server/httpapi/ -run TestBrowsing -v
```

`./...` is avoided on purpose: `web/node_modules` can contain Go files shipped by
npm packages, and those are not ours to test.

The suite is mostly behavioural rather than unit-level. The server tests run a real
server, enrol a real agent client and perform real backups against a temporary
directory, because that is where the interesting bugs are: delta resolution, quota
enforcement, retention, browsing, restore paths. When adding a test, prove a
guarantee ("a delta snapshot resolves against its parent", "an unreadable file does
not abort the run") rather than restating the implementation.

What tests cannot tell you is whether a restore produced the right bytes. For
anything touching the agent or the protocol, do the round trip by hand: back up,
change a file, back up again, restore elsewhere, `diff`.

## Continuous integration

[`.github/workflows/ci.yml`](../.github/workflows/ci.yml) runs:

- tests on Linux, macOS and Windows, plus the race detector on Linux
- `gofmt`, `go vet`, `go mod tidy` cleanliness, and `shellcheck` on the installers
- cross-compilation for every release target with `CGO_ENABLED=0`
- the dashboard's typecheck, lint and build, then a real embed into the server
- the desktop app on Windows and Linux, including the NSIS installer
- a container build with a smoke test that hits `/api/v1/health` and the dashboard

A green pipeline should mean a working local build, so if it diverges, that is a bug
in the pipeline.

## Release

Version metadata is injected at link time, so nothing generated lives in the tree:

```bash
make release        # cross-compiled binaries and SHA256SUMS in ./dist
make docker         # container image
```

Tag with a semantic version; `git describe` supplies it to the build. The desktop
apps are built per platform (there is no cross-compiling a native webview), which CI
does for Windows and Linux. Update [`CHANGELOG.md`](../CHANGELOG.md) as part of the
release, not afterwards.

## Where to start reading

[architecture.md](architecture.md) explains how the pieces fit and which invariants
each one owns. After that, the most instructive path through the code is the one a
file takes:

```
internal/agent/scanner    → what to back up
internal/agent/uploader   → chunk, hash, ask, compress, encrypt, send
internal/server/httpapi   → the endpoints receiving it
internal/server/store     → what gets written where
internal/agent/restore    → and how it comes back
```

Read `internal/ignore` too. It is small, and it is the part users notice most.
