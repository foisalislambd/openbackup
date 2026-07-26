# Architecture

Two programs and a browser. The agent decides what to back up and does the
cryptography; the server stores blocks and metadata and knows nothing about how
they were produced; the dashboard is a static bundle inside the server binary.

```
┌──────────────────────────────┐        ┌─────────────────────────────────┐
│            agent             │        │             server              │
│                              │        │                                 │
│  watch → scan → chunk → hash │ HTTPS  │  SQLite: users, devices,        │
│      → compress → encrypt    │ ─────▶ │    snapshots, entries, chunks   │
│                              │  JSON  │  blobs: content-addressed store │
│  local index (SQLite)        │ ◀───── │  maintenance: retention, GC     │
│  governor: CPU/battery/net   │        │  dashboard (embedded static)    │
└──────────────────────────────┘        └─────────────────────────────────┘
```

Communication is HTTP with JSON bodies and raw binary chunk uploads. Not gRPC:
this has to work behind every corporate proxy, be debuggable with `curl`, and be
consumable by a browser without a translation layer. The wire protocol lives in
[`internal/api`](../internal/api) and is shared by both sides, so a change that
breaks compatibility breaks the build.

## Backing up a file

1. **Scan.** The scanner walks the configured roots, applying the ignore rules and
   detecting projects as it goes ([ignore-rules.md](ignore-rules.md)). It never
   leaves those roots and does not follow symbolic links out of them.
2. **Detect changes.** The local index — a SQLite file in the agent's state
   directory — remembers each file's size, modification time and content hash. A
   file whose size and mtime are unchanged is not read at all, which is what makes
   a rescan of a large disk cheap.
3. **Chunk.** Changed files are split with **FastCDC**, content-defined chunking:
   boundaries are chosen by a rolling hash of the content, so inserting a
   paragraph at the top of a document shifts one chunk rather than every chunk
   after it. Average chunk size is around 1 MiB.
4. **Hash.** Each chunk is identified by its **BLAKE3-256** digest. That digest is
   the chunk's name everywhere: in the index, in the request, on disk.
5. **Ask what is missing.** The agent sends a batch of digests and the server
   replies with the ones it does not have. This is where deduplication happens,
   and it is why the second laptop with the same photo library uploads almost
   nothing.
6. **Compress, then encrypt.** Zstandard, then XChaCha20-Poly1305 if end-to-end
   encryption is on. In that order, because encrypted data does not compress.
7. **Upload** the missing chunks, three at a time by default, through a token
   bucket that enforces the speed limit. An interrupted upload resumes: chunks
   already stored are simply not missing next time.
8. **Record the snapshot.** Entries — path, type, size, mode, mtime, digest list —
   are sent in batches of 500, then the snapshot is completed. A snapshot that is
   never completed is cleaned up by the server after six hours rather than left to
   rot.

## Snapshots are lists, not copies

A snapshot is metadata: paths pointing at chunk digests. Nothing about it copies
data.

The first snapshot for a device is **full**. Later ones are **deltas** — only what
changed, plus a list of deletions, and a pointer to the parent. Reading a delta
means walking its chain back to the full snapshot and letting newer entries shadow
older ones, with deletions removing them. That resolution happens at read time,
in one SQL query with a recursive chain, because materialising a million-file tree
per snapshot would cost more than the file data does.

The chain is bounded: after 24 deltas the agent takes a full snapshot again, so a
restore never walks an unbounded history and losing one snapshot cannot cost you
years of it.

Two consequences worth knowing:

- **Deleting a snapshot deletes the deltas that depend on it**, because a delta
  without its base cannot be restored.
- **Retention never removes the newest complete snapshot**, even when it is older
  than the window. An old backup beats no backup.

## The block store

Blocks live in a content-addressed store: the digest is the path, sharded by its
first bytes so no directory holds millions of entries. Writes go to a temporary
file and are renamed into place, so a crash mid-write cannot leave a partial block
under a name that claims to be complete. Reads verify nothing at the store level —
the agent verifies every chunk against its digest as it restores, which catches
corruption wherever it happened.

The default backend is the local filesystem. An S3-compatible backend exists behind
the same interface, and the database stays local either way.

Reference counting is per snapshot: a row per (snapshot, chunk). A chunk is
deletable when no snapshot references it, which the garbage collector determines
with a join rather than by scanning the disk. Cleanup runs in batches of 5000 so a
large deletion never blocks uploads for long.

## The agent's parts

| Package | Responsibility |
| --- | --- |
| [`scanner`](../internal/agent/scanner) | Walk roots, apply rules, emit candidates |
| [`watcher`](../internal/agent/watcher) | React to filesystem events, debounce, handle overflow by asking for a rescan |
| [`index`](../internal/agent/index) | Remember what is already backed up |
| [`uploader`](../internal/agent/uploader) | Chunk, ask what is missing, compress, encrypt, upload with throttling |
| [`governor`](../internal/agent/governor) | Decide whether now is a good time: CPU, battery, metered link, full-screen app |
| [`engine`](../internal/agent/engine) | Own the loop: scan, back up, heartbeat, obey server commands, reload settings |
| [`restore`](../internal/agent/restore) | Rebuild files, verify digests, resolve conflicts safely |
| [`control`](../internal/agent/control) | High-level operations shared by the CLI and the desktop app |
| [`ipc`](../internal/agent/ipc) | Loopback HTTP with a shared secret, so the CLI and the app can talk to the running service |

The **governor** is why this stays installed on people's machines: a backup tool
that competes with a game gets blamed for the game stuttering, and uninstalled.

The **control** layer exists so the CLI and the desktop app cannot drift apart. A
folder added in the window and a folder added in the terminal take exactly the
same path through the code, and both cause the running service to reload rather
than only editing a file that nothing has read yet.

## The server's parts

| Package | Responsibility |
| --- | --- |
| [`store`](../internal/server/store) | SQLite schema and queries, blob backends |
| [`httpapi`](../internal/server/httpapi) | The agent protocol, the dashboard API, the embedded UI, the served installer |
| [`auth`](../internal/server/auth) | Password hashing (Argon2id), token hashing, constant-time comparison |
| [`maintenance`](../internal/server/maintenance) | Retention, garbage collection, stale snapshots, log pruning, expired sessions |

SQLite, not PostgreSQL, and a pure-Go driver: one container, one volume, no
credentials to manage, and a database you can copy while it runs. The workload is
tiny metadata writes from a handful of devices, which is exactly what SQLite in
WAL mode is good at. The storage layer is behind interfaces, so a deployment that
outgrows it has somewhere to go.

Sessions are opaque cookies stored server-side, not JWTs, so signing out and
removing a device take effect immediately instead of at the end of a token's life.

## The dashboard

A Vite React SPA, copied into `internal/server/web/dist` and embedded with
`//go:embed`. The server therefore ships its UI: no second container, no CDN, no
version skew between the API and the pages calling it. `go build` alone produces a
working server — it just embeds whatever dashboard was last built.

## The desktop app

A separate Go module in [`desktop/`](../desktop/README.md), built with Wails. It
is a client of the agent, not a second implementation: it calls the same `control`
package and asks the running service to reload. It is a separate module because
Wails links against the platform's webview, and the server and agent must stay
pure-Go cross-compiles so one machine can build every release target.

## Invariants

These are the properties the design protects. Changing any of them is a breaking
change, not a refactor:

1. **Nothing outside a configured root is read.**
2. **A restore writes only inside its destination.** Paths from a snapshot are
   treated as untrusted input: absolute paths, `..` and symlink tricks are
   rejected.
3. **A chunk is what its digest says it is.** Verified on restore, always.
4. **An interrupted operation is safe.** Partial uploads are not referenced,
   partial snapshots are cleaned up, partial blocks never get a final name.
5. **The server never receives the encryption key.**
6. **An older agent keeps working against a newer server.** The wire protocol is
   additive within a major version.
