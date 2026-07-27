# Changelog

Notable changes, newest first. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[semantic versioning](https://semver.org/spec/v2.0.0.html) once 1.0 exists.

Until then, treat minor versions as able to change the wire protocol. The
snapshot format on the server is forward-compatible within a minor version: an
older agent keeps working against a newer server, so upgrade the server first.

## Unreleased

### Fixed

- After the agent can reach the server again, sticky connection errors are
  cleared from `/devices`, Home, and the desktop app (they remain in Logs).

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
