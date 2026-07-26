# OpenBackup dashboard

The web dashboard: devices, storage, backup health, activity, settings, and
restore. It is a Vite + React SPA embedded into the Go server binary, so a
self-hosted install stays one binary with no Node runtime in production.

## Layout

```
src/pages/       one file per route
src/components/  shell (navigation and the auth gate) and the UI primitives
src/lib/         the typed API client, the data-loading hook, formatting helpers
```

There is no state management library and no component library. Every page loads
what it needs through `useLoader` in `src/lib/use-loader.ts` and renders it; the
server is the state.

## Developing

The dashboard needs the API, so run both:

```bash
# terminal 1 — the API on :18200
go run ./cmd/openbackup-server

# terminal 2 — the dashboard on :5173, proxying /api to :18200
cd web && npm install && npm run dev
```

Point the proxy somewhere else with `OPENBACKUP_DEV_SERVER=http://host:port`.

## Building

`make web` from the repository root builds the SPA and copies it into
`internal/server/web/dist`, where `//go:embed` picks it up. On Windows use
`./scripts/build.ps1`, which does the same and then builds the binaries.

Before pushing:

```bash
npm run typecheck
npm run lint
```

## Conventions worth knowing

- **Authentication is a cookie.** Nothing here stores a token. The session cookie
  is `HttpOnly`, so a script cannot read it, and requests use
  `credentials: 'same-origin'`.
- **`/api/v1/ui/bootstrap` runs first.** A static SPA cannot redirect on the
  server, so `Shell` asks the server whether to show first-run setup, the sign-in
  form, or the dashboard.
- **Colours come from theme tokens** in `src/styles.css`, referenced as
  `var(--color-ink)` and friends. Dark mode re-points those variables; no
  component needs a `dark:` variant.
- **Restore downloads use authenticated fetch** (`downloadSnapshotFile` /
  `downloadSnapshotArchive`) so encrypted or missing-file errors show in the UI
  instead of a blank browser download. End-to-end encrypted backups cannot be
  restored from the browser, because the server has no key — the server returns
  a clear error saying to use `openbackup restore` on the device instead.
