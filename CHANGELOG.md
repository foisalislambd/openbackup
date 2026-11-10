# Changelog

Notable changes, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html) once 1.0 exists.

Until then, treat minor versions as able to change the wire protocol. The
snapshot format on the server is forward-compatible within a minor version: an
older agent keeps working against a newer server, so upgrade the server first.

## Unreleased

### Added

- `.openbackupignore` files, gitignore-style, shared by the scanner, the watcher and delta backups. JSON include and exclude still outrank them, and a negation cannot undo a built-in rule.
- Pack files for blobs under 512 KiB, with a sidecar index that is rebuilt if it is missing. Larger blobs and S3 objects stay one file per digest. Existing loose files still restore.
- Agents upload several chunks in one request. Servers that do not know the route still receive one chunk at a time.
- Source-read pacing (`read_bytes_per_sec`), a process CPU budget (`agent_cpu_percent`), a soft memory target (`memory_limit_bytes`) and a background scheduling priority. The default priority is background.
- `make bench` / `scripts/bench` measures a full backup, an incremental backup and a restore.

### Changed

- The chunker reuses a bounded set of size-class buffers and releases them after a backup. Buffers of 8 MiB and larger are not kept. Chunk boundaries are unchanged.
- Zstandard encoders are reused per worker and idle encoders are closed after a backup. The blob format is unchanged.
- The local index records an inode when the filesystem has one, batches its writes, and is refilled from the newest full snapshot when the file is empty so unchanged files are not read again. File metadata is published in batches after the corresponding chunks are durable.
- Unambiguous dependency directories such as `node_modules` are skipped everywhere. Ambiguous names such as `dist` and `build` stay limited to detected projects, and that decision is the same one the watcher uses. The watcher does not list an excluded directory.
- The server admits uploads against a byte budget (`OPENBACKUP_UPLOAD_BUDGET`, default 64 MiB). That budget counts request bytes, not the process heap.
- `OPENBACKUP_SQLITE_SYNC=FULL` makes metadata commits durable across power loss. The default remains WAL with `synchronous=NORMAL`.
- Pack files now checksum each frame. A missing or truncated index is rebuilt from the pack, and a truncated pack keeps only complete frames. Deleting a packed blob records a checksummed tombstone and rewrites the pack so the bytes are reclaimed. Version 1 packs still restore.
- The desktop window polls every 15 seconds while idle and every 2 seconds while a backup is running, and re-reads configuration only when the file changes. Dashboard polls do not overlap. Maintenance skips the integrity sample on the startup pass and vacuums only after a large reclaim.
- `memory_limit_bytes` defaults to 128 MiB (`-1` disables it). The server sets the same target when `GOMEMLIMIT` is unset.
- The upload budget charges the larger of the wire size and the plaintext length in the blob header, across every device at once, before the blob is decoded. A caller-supplied length that understates the header does not reduce the charge. A request that cannot fit is rejected. Uploads stop when the blob volume has less than 8 MiB free.
- Windows no longer fails a pack write when the OS refuses to flush a directory handle. The pack file is still fsynced before it is renamed. Linux and macOS still fsync the directory as well.
- Completing a snapshot reads each referenced blob. A missing blob, or an unencrypted blob that does not match its digest, marks the snapshot failed.
- Changing `.openbackupignore` updates the watcher before the next event is queued.

### Fixed

- Garbage collection rechecks snapshot references, including a backup that is still running, and deletes a blob only while that check is locked. A completed snapshot can no longer lose a chunk to a concurrent collection.
- A failed chunk flush returns each pooled buffer once.
- Editing a file inside a project no longer backs up that project's build directory.
- A file larger than the size limit no longer schedules an empty snapshot.
- A replaced file with the same size and timestamp is read again when its inode changed.

### Added

- `VERSION.md` is the release version for the server, agent, desktop app, dashboard, and container image. A push to `main` publishes a GitHub Release only when that version is a strictly higher semver than the latest tag and GitHub Release.
- Dashboard overview shows backup health, per-device progress, storage, open alerts, and recent activity. The file browser searches a whole backup, and a backup can be pinned so retention will not remove it.
- Retention can thin history to a chosen number of daily, weekly, and monthly copies inside the existing window. Leaving the counts at zero keeps every snapshot in the window, as before.
- The server samples stored blocks during maintenance (and on demand from Settings), checks unencrypted blocks against their digest, and raises an alert when a sample fails. Encrypted blocks are checked for presence only; the agent still verifies them on restore.
- Settings can warn when a device has not finished a backup within a chosen number of hours, and can sign out other browsers. Setup and password changes are rate limited the same way sign-in already was.
- The desktop app uses the same type, colour, and light/dark treatment as the dashboard, with backup and restore progress shown on the status and activity screens. Connecting a device and running backups is unchanged.
- Release workflow also publishes the server image to GHCR as
  `ghcr.io/foisalislambd/openbackup` (`latest` + version tags), alongside Docker Hub.
- Desktop **Check for updates** (Settings → About): compares this build to the
  latest GitHub Release and opens the Windows installer download in the browser.

### Changed

- Release tags come from `VERSION.md`. Bump that file to cut the next release.

### Fixed

- After the agent can reach the server again, sticky connection errors are
  cleared from `/devices`, Home, and the desktop app (they remain in Logs).
  The server also ignores those sticky network errors on heartbeat, so the
  dashboard clears even before every agent is upgraded.
- `install-server.sh` always pulls `latest` on re-run (it used to keep a stale
  local image), recreates the container, honours HTTPS `OPENBACKUP_PUBLIC_URL`
  for bind address / firewall, and prints that URL after upgrade.
- Windows installer stops the running app before upgrade, and uninstall removes
  login autostart plus local OpenBackup data (the in-process agent goes with the
  app). Release builds set NSIS/file `productVersion` from `VERSION.md`.
- Device badges no longer show a doubled `vv` prefix when the agent version
  already includes `v`.

## [0.2.0] - 2026-07-26

### Changed

- Published server image is `foisalislambd/openbackup` (Compose, docs, and
  Makefile). Releases push `latest` plus the version tag to Docker Hub from CI
  for `linux/amd64` and `linux/arm64`.

### Fixed

- Release workflow only downloads `openbackup-*` artifacts, so Docker buildx
  cache artifacts no longer break GitHub Release creation.

### Added

- Desktop app for Windows, macOS and Linux (`desktop/`): connect a device, see
  what is protected, browse and restore files, change limits and encryption, and
  run diagnostics, all without a terminal. Closes to the notification area, where
  the tray icon reflects the current state.
- `internal/agent/control`, one set of high-level operations shared by the CLI
  and the desktop app, so both apply changes to a running background service
  instead of only to the config file.
- Live configuration reload: added folders, changed limits and pause state take
  effect without restarting the service.
- `children=1` on snapshot listings, for browsing one folder at a time.
- Documentation in [`docs/`](docs/README.md), plus contribution, security and
  support guides.

### Fixed

- The restore browser listed every descendant of a folder at the current level,
  so nested files appeared to live at the root.
- Removing the last backed-up folder no longer causes the automatically detected
  folders to reappear on the next start.
- Files inside an excluded directory are now excluded, rather than only the
  directory entry itself.
- `node_modules` is only skipped inside a directory that looks like a project, so
  a personal folder with that name is still backed up.
- The `invite` command printed a malformed URL when the server had no public URL
  configured.
