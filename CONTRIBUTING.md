# Contributing to OpenBackup

Thanks for looking. This is backup software: someone will trust it with the only
copy of their photographs, so the bar for changes is "would I stake a stranger's
data on this", not "does it compile". Everything below exists to make that bar
easy to clear.

If anything here is unclear or wrong, that is a bug in this file — please say so.

## Ways to help that are not code

- **Report what broke.** A backup that silently skipped a folder is a serious
  bug even if nothing crashed. Open an issue with the output of
  `openbackup doctor`.
- **Tell us what confused you.** If the dashboard or a CLI message made you
  guess, that is worth fixing. Wording changes are welcome pull requests.
- **Test a restore.** Restoring on an unusual setup (odd filesystem, network
  drive, locale, non-Latin filenames) finds things no unit test does.
- **Improve the docs.** [`docs/`](docs/README.md) is part of the product.

## Getting set up

You need **Go 1.26+** and **Node 22+**. Nothing else — no database server, no
CGO, no toolchain beyond that.

```bash
git clone https://github.com/foisalislambd/openbackup
cd openbackup
make            # build the dashboard, embed it, build the binaries
make test       # run the tests
```

On Windows, use `./scripts/build.ps1` and `./scripts/build.ps1 -Test`; the
Makefile needs a POSIX shell.

For a full development loop — server, dashboard with hot reload, an agent
pointed at throwaway folders — follow
[docs/development.md](docs/development.md). Do that before your first change:
the fastest way to understand this codebase is to watch a file get backed up.

## The change you are about to make

Small and focused beats large and thorough. A pull request that does one thing
can be read, argued with and merged; one that does five things stalls.

Before starting something substantial, open an issue and describe the problem you
hit. Not to ask permission — to avoid the case where two people solve the same
thing differently, or where a feature is declined after you built it. Reasons a
feature gets declined are usually one of these:

- It adds a decision the user has to make. Zero configuration is the point of
  this project; every new setting is a small failure we accepted for a good
  reason.
- It touches files outside the user's own data. The agent reads personal folders
  and writes to its own state directory. That boundary is not negotiable.
- It makes the agent heavier. The idle cost is a few megabytes and no measurable
  CPU, and a feature that changes that needs to be worth it.

## House style

The code is written to be read by whoever debugs it at 2am, possibly you.
`make fmt` handles formatting; the rest is about intent.

**Comments explain why, never what.** The code already says what it does. A
comment earns its place by recording a constraint, a trade-off, or a bug that
made the obvious approach wrong.

```go
// Range comparisons instead of LIKE: they use the primary key index and need no
// escaping of % or _ in user paths.
```

That is useful. `// query the entries table` is not — delete it. No comments
about the change itself either ("added this to fix the bug"): the next reader has
no idea which change you meant, and the git history already knows.

**Names say what a thing is** in the domain, not in the abstract:
`snapshot`, `chunk`, `root`, `governor`. No `data`, `info`, `manager`, `helper`.

**Errors are for the person who has to fix it.** Wrap with context, and where a
user will see it, write a sentence they can act on:

```go
return fmt.Errorf("config: read %s: %w", path, err)
```

**Messages users read are prose, not jargon.** "Everything is backed up", not
"SYNC_STATE=OK". No emoji, no exclamation marks.

**Tests describe behaviour.** `TestBrowsingListsOneFolderAtATime`, not
`TestTree2`. Prefer one test that proves a real guarantee (a delta snapshot
resolves against its parent, an unreadable file does not abort the run) over
several that restate the implementation.

**No new dependency without a reason** you can state in one sentence. The
server, the agent and everything they need cross-compile with `CGO_ENABLED=0`,
and that must stay true — it is why the SQLite driver is a pure-Go one.

## Before you open a pull request

```bash
make check      # gofmt, go vet, tests: what CI runs
```

Then, for anything that touches the agent or the wire protocol, do the thing that
actually matters:

1. Back up a folder.
2. Change a file, back up again, confirm only the changed part uploaded.
3. Restore it somewhere else and diff the result.

CI covers Linux, macOS and Windows, the race detector, cross-compilation for
every release target, the dashboard's types and lint, the desktop app on Windows
and Linux, and a container smoke test. It is thorough, but it cannot tell you
whether a restore produced the right bytes.

## Pull request expectations

- **Describe the problem first**, then the fix. If there is a reproduction, put
  it in the description.
- **Say how you tested it.** "make check plus a real backup and restore on
  Windows" is a complete answer.
- **Keep the history readable.** Any number of commits is fine; each should
  build. Write messages in the imperative — "fix flattened restore listing" —
  and explain why in the body if it is not obvious.
- **Update the docs in the same change.** A flag that exists only in code is a
  flag nobody finds.
- **Add a CHANGELOG entry** under `## Unreleased` for anything a user would
  notice.

Reviews look for correctness on the failure paths first: what happens when the
network dies mid-upload, when the disk fills, when a file is locked, when the
clock jumps. That is where backup software actually lives.

## Where things are

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
web/                     dashboard (Vite React SPA, embedded in the server)
desktop/                 Wails desktop app (its own Go module)
docs/                    the documentation
```

[docs/architecture.md](docs/architecture.md) explains how these fit together and
which invariants each part is responsible for. Read it before changing the
snapshot format, the chunk store or the ignore engine — those three have
consequences that outlive a release.

## Reporting security problems

Do not open a public issue. [SECURITY.md](SECURITY.md) explains how to report
privately.

## Conduct and licence

Be decent to people; [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md) has the detail.
Contributions are accepted under the [MIT licence](LICENSE), the same terms as
the project.
