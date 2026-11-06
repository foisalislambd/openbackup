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

- a check that `VERSION.md` is valid semver, and that a changed version is newer than the latest release
- tests on Linux, macOS and Windows, plus the race detector on Linux
- `gofmt`, `go vet`, `go mod tidy` cleanliness, and `shellcheck` on the installers
- cross-compilation for every release target with `CGO_ENABLED=0`
- the dashboard's typecheck, lint and build, then a real embed into the server
- the desktop app on Windows and Linux, including the NSIS installer
- a container build with a smoke test that hits `/api/v1/health` and the dashboard

A green pipeline should mean a working local build, so if it diverges, that is a bug
in the pipeline.

## Release

[`VERSION.md`](../VERSION.md) at the repository root is the only release version.
The first non-empty, non-comment line is SemVer with no leading `v` (for example
`0.3.1`). Lines after it are notes. `make`, `scripts/build.ps1`, the container
build, and the desktop build all stamp that version into binaries (`-ldflags`),
the Windows product version, the dashboard, and image tags.

Pushing to `main` runs [`.github/workflows/release.yml`](../.github/workflows/release.yml).
It reads `VERSION.md`, checks that it is valid SemVer, and compares it with the
highest version among `v*` git tags and GitHub Releases. A release is published
only when `VERSION.md` is **strictly greater**. The same or a lower version skips
the release jobs and prints why; CI tests still run. When it is greater, the
workflow builds artifacts, tags `vX.Y.Z`, and creates a GitHub Release.

To publish:

1. Set the new version on the first line of `VERSION.md` (`0.3.1` → `0.4.0`).
2. Describe the change under `## Unreleased` in [`CHANGELOG.md`](../CHANGELOG.md).
3. Merge to `main`.

Each release includes:

- cross-compiled **agent + server** binaries (`make release`)
- **Windows** desktop app + NSIS installer
- **Linux** desktop app (`amd64`, built with `webkit2_41`)
- **Docker Hub** image `foisalislambd/openbackup` and **GHCR** image
  `ghcr.io/foisalislambd/openbackup` (`latest` + version tags)

Put `[skip release]` in the commit message to push a newer `VERSION.md` without
publishing. You can also run **Actions → Release → Run workflow**; it uses
`VERSION.md`, not a version typed into the form.

Pull requests fail when `VERSION.md` is not valid SemVer, and when the version
number changed but is not greater than the latest release. A pull request that
leaves the version unchanged only warns, so it can merge without cutting a release.

Locally:

```bash
make version                 # print vX.Y.Z from VERSION.md
make release                 # CLI binaries + SHA256SUMS in ./dist
make desktop                 # desktop for this machine
make desktop-linux-package   # Linux release-named binary + .desktop
make docker                  # container image (vX.Y.Z, X.Y.Z, and latest)
```

`make VERSION=v9.9.9` overrides a local build. CI does not. An unstamped
`go run` inside this checkout still reports `VERSION.md`, because the version
package reads that file when the binary was not linked with `-ldflags`.

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
