/**
 * My files: browse the latest backup like a drive. Click a file to see older
 * versions. Snapshot history is secondary, not the main list.
 */

import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  api,
  downloadSnapshotArchive,
  downloadSnapshotFile,
  type Device,
  type Entry,
  type FileVersion,
  type Snapshot,
} from '@/lib/api'
import { absolute, baseName, bytes, count, parentPath, relative } from '@/lib/format'
import { message, useAction, useLoader } from '@/lib/use-loader'
import { Badge, Button, Empty, ErrorNote, FileGlyph, FolderGlyph } from '@/components/ui'
import { BackupsSkeleton, BrowseSkeleton } from '@/components/skeleton'

type Catalog = { devices: Device[]; snapshots: Snapshot[] }

export default function BackupsPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const view = params.get('view') === 'history' ? 'history' : 'files'
  const deviceId = params.get('device') ?? ''
  const prefix = params.get('path') ?? ''
  const versionsPath = params.get('file') ?? ''
  const snapshotOverride = params.get('id') ?? ''

  const { data, error, loading, reload } = useLoader<Catalog>(
    async () => {
      const [devices, snapshots] = await Promise.all([api.devices(), api.snapshots()])
      return { devices, snapshots }
    },
    { pollMs: 15000 },
  )
  const { busy, error: actionError, run } = useAction()

  const setQuery = (next: Record<string, string | undefined>) => {
    const q = new URLSearchParams()
    const merged = {
      view: view === 'history' ? 'history' : undefined,
      device: deviceId || undefined,
      path: prefix || undefined,
      file: versionsPath || undefined,
      id: snapshotOverride || undefined,
      ...next,
    }
    for (const [key, value] of Object.entries(merged)) {
      if (value) q.set(key, value)
    }
    const qs = q.toString()
    navigate(qs ? `/backups?${qs}` : '/backups')
  }

  if (error && !data) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <BackupsSkeleton />

  const complete = data.snapshots.filter((s) => s.status === 'complete')
  if (complete.length === 0) {
    const inProgress = data.snapshots.some((s) => s.status === 'running')
    return (
      <div className="panel">
        <Empty
          title="No files backed up yet"
          hint={
            inProgress
              ? 'A backup is in progress — files appear here when it finishes.'
              : 'Connect a device and let the first backup finish.'
          }
        />
      </div>
    )
  }

  const deviceIds = new Set(complete.map((s) => s.device_id))
  const deviceOptions = data.devices.filter((d) => deviceIds.has(d.id))
  const activeDevice = deviceId || deviceOptions[0]?.id || ''
  const deviceSnaps = complete.filter((s) => !activeDevice || s.device_id === activeDevice)
  const latest = snapshotOverride
    ? deviceSnaps.find((s) => s.id === snapshotOverride) || complete.find((s) => s.id === snapshotOverride)
    : deviceSnaps[0]

  const historySnaps = data.snapshots
    .filter((s) => !activeDevice || s.device_id === activeDevice)
    .filter((s) => s.status === 'complete' || s.status === 'failed' || s.status === 'running')

  return (
    <div className="space-y-4">
      {(actionError || error) && <ErrorNote>{actionError || error}</ErrorNote>}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant={view === 'files' ? 'primary' : 'ghost'}
            onClick={() => setQuery({ view: undefined, id: undefined, file: undefined })}
          >
            My files
          </Button>
          <Button
            variant={view === 'history' ? 'primary' : 'ghost'}
            onClick={() => setQuery({ view: 'history', path: undefined, file: undefined, id: undefined })}
          >
            Backup history
          </Button>
        </div>
        {(view === 'files' || view === 'history') && deviceOptions.length > 1 && (
          <select
            className="rounded-lg border border-[var(--color-border-subtle)] bg-[var(--color-surface)] px-3 py-2 text-sm"
            value={activeDevice}
            onChange={(e) =>
              setQuery({ device: e.target.value, path: undefined, file: undefined, id: undefined })
            }
          >
            {deviceOptions.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        )}
      </div>

      {view === 'history' ? (
        <HistoryList
          snapshots={historySnaps}
          busy={busy}
          onOpen={(snapshot) => {
            if (snapshot.status !== 'complete') return
            setQuery({
              view: undefined,
              id: snapshot.id,
              device: snapshot.device_id,
              path: undefined,
              file: undefined,
            })
          }}
          onDelete={(id) => {
            if (
              !confirm(
                'Delete this backup?\n\nLater backups that depend on it may also be removed. Files only held here will be gone for good.',
              )
            ) {
              return
            }
            void run(id, () => api.deleteSnapshot(id), reload)
          }}
        />
      ) : !latest ? (
        <div className="panel">
          <Empty title="No completed backup for this device" />
        </div>
      ) : (
        <FileBrowser
          snapshot={latest}
          prefix={prefix}
          versionsPath={versionsPath}
          viewingOlder={Boolean(snapshotOverride && snapshotOverride !== deviceSnaps[0]?.id)}
          onNavigate={(path) => setQuery({ path: path || undefined, file: undefined })}
          onOpenVersions={(path) => setQuery({ file: path })}
          onCloseVersions={() => setQuery({ file: undefined })}
          onUseLatest={() => setQuery({ id: undefined, file: undefined })}
          deviceId={activeDevice}
        />
      )}
    </div>
  )
}

function HistoryList({
  snapshots,
  busy,
  onOpen,
  onDelete,
}: {
  snapshots: Snapshot[]
  busy?: string | null
  onOpen: (snapshot: Snapshot) => void
  onDelete: (id: string) => void
}) {
  if (snapshots.length === 0) {
    return (
      <div className="panel">
        <Empty title="No backups for this device yet" />
      </div>
    )
  }

  return (
    <div className="panel overflow-hidden">
      <div className="border-b border-[var(--color-border-subtle)] px-5 py-4">
        <h2 className="text-sm font-semibold">Backup history</h2>
        <p className="mt-1 text-sm text-[var(--color-ink-muted)]">
          Each row is a point in time for this computer — not a separate folder.
        </p>
      </div>
      <ul>
        {snapshots.map((snapshot, index) => {
          const complete = snapshot.status === 'complete'
          const title =
            snapshot.status === 'running'
              ? 'Backup in progress'
              : snapshot.status === 'failed'
                ? 'Failed backup'
                : index === 0 || snapshots.findIndex((s) => s.status === 'complete') === index
                  ? 'Latest backup'
                  : 'Earlier backup'

          return (
            <li key={snapshot.id}>
              <div className="file-row group">
                <button
                  type="button"
                  className="flex min-w-0 items-center gap-3 text-left disabled:cursor-default"
                  disabled={!complete}
                  onClick={() => onOpen(snapshot)}
                >
                  <FolderGlyph />
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-semibold group-hover:text-[var(--color-brand)]">
                      {title}
                      {snapshot.device_name ? ` · ${snapshot.device_name}` : ''}
                    </span>
                    <span className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-[var(--color-ink-muted)]">
                      {absolute(snapshot.started_at)}
                      {snapshot.kind === 'delta' && <Badge>changes only</Badge>}
                      {snapshot.kind === 'full' && <Badge>full</Badge>}
                      {snapshot.status === 'running' && <Badge tone="warn">running</Badge>}
                      {snapshot.status === 'failed' && <Badge tone="bad">failed</Badge>}
                    </span>
                  </span>
                </button>
                <span className="file-row-meta tabular text-sm text-[var(--color-ink-muted)]">
                  {complete ? count(snapshot.file_count) : '—'}
                </span>
                <span className="file-row-meta tabular text-sm text-[var(--color-ink-muted)]">
                  {complete ? bytes(snapshot.total_bytes) : '—'}
                </span>
                <div className="flex items-center justify-end gap-2">
                  {complete && <Button onClick={() => onOpen(snapshot)}>Browse</Button>}
                  <Button variant="danger" disabled={busy === snapshot.id} onClick={() => onDelete(snapshot.id)}>
                    Delete
                  </Button>
                </div>
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function FileBrowser({
  snapshot,
  prefix,
  versionsPath,
  viewingOlder,
  onNavigate,
  onOpenVersions,
  onCloseVersions,
  onUseLatest,
  deviceId,
}: {
  snapshot: Snapshot
  prefix: string
  versionsPath: string
  viewingOlder: boolean
  onNavigate: (path: string) => void
  onOpenVersions: (path: string) => void
  onCloseVersions: () => void
  onUseLatest: () => void
  deviceId: string
}) {
  const snapshotId = snapshot.id
  const { data, error, loading } = useLoader(
    async () => {
      const page = await api.browse(snapshotId, prefix)
      return { entries: page.entries ?? [], cursor: page.next_cursor ?? '' }
    },
    { deps: [snapshotId, prefix] },
  )
  const [extra, setExtra] = useState<{ key: string; entries: Entry[]; cursor: string }>()
  const [dlError, setDlError] = useState<string>()
  const more = useAction()

  if (error && !data) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <BrowseSkeleton />

  const key = `${snapshotId}:${prefix}`
  const appended = extra?.key === key ? extra.entries : []
  const cursor = extra?.key === key ? extra.cursor : data.cursor
  // children=1 returns immediate children only — no nested-path folding needed.
  const entries = [...data.entries, ...appended]
  const folders = entries
    .filter((e) => e.type === 'dir')
    .slice()
    .sort((a, b) => a.path.localeCompare(b.path))
  const files = entries
    .filter((e) => e.type !== 'dir')
    .slice()
    .sort((a, b) => a.path.localeCompare(b.path))

  const download = async (fn: () => Promise<void>) => {
    setDlError(undefined)
    try {
      await fn()
    } catch (err) {
      setDlError(message(err, 'Download failed'))
    }
  }

  return (
    <div className="space-y-4">
      {viewingOlder && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-[var(--color-warn)]/30 bg-[var(--color-warn)]/10 px-4 py-3 text-sm">
          <span>
            Viewing an earlier backup from {absolute(snapshot.started_at)}. Your current files may differ.
          </span>
          <Button onClick={onUseLatest}>Back to latest</Button>
        </div>
      )}

      {(dlError || error) && <ErrorNote>{dlError || error}</ErrorNote>}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-lg font-semibold tracking-tight">
            {snapshot.device_name ?? 'My files'}
          </div>
          <div className="mt-0.5 text-sm text-[var(--color-ink-muted)]">
            As of {absolute(snapshot.started_at)} · {count(snapshot.file_count)} files ·{' '}
            {bytes(snapshot.total_bytes)}
          </div>
        </div>
        <Button onClick={() => void download(() => downloadSnapshotArchive(snapshotId, prefix))}>
          Download {prefix ? `“${baseName(prefix)}”` : 'everything'} ZIP
        </Button>
      </div>

      <nav className="flex flex-wrap items-center gap-1 text-sm">
        <button
          type="button"
          className="rounded-lg px-2 py-1 font-semibold text-[var(--color-brand)] hover:bg-[var(--color-brand-soft)]"
          onClick={() => onNavigate('')}
        >
          All folders
        </button>
        {prefix
          .split('/')
          .filter(Boolean)
          .map((segment, index, all) => {
            const path = all.slice(0, index + 1).join('/')
            const isLast = index === all.length - 1
            return (
              <span key={path} className="flex items-center gap-1">
                <span className="text-[var(--color-ink-muted)]">/</span>
                {isLast ? (
                  <span className="rounded-lg px-2 py-1 font-semibold">{segment}</span>
                ) : (
                  <button
                    type="button"
                    className="rounded-lg px-2 py-1 font-semibold text-[var(--color-brand)] hover:bg-[var(--color-brand-soft)]"
                    onClick={() => onNavigate(path)}
                  >
                    {segment}
                  </button>
                )}
              </span>
            )
          })}
      </nav>

      <div className={`grid gap-4 ${versionsPath ? 'xl:grid-cols-[1fr_20rem]' : ''}`}>
        <div className="panel overflow-hidden">
          {folders.length === 0 && files.length === 0 ? (
            <Empty title="This folder is empty in the backup" />
          ) : (
            <>
              <div className="file-row border-b border-[var(--color-border-subtle)] text-[0.7rem] font-semibold uppercase tracking-[0.06em] text-[var(--color-ink-muted)]">
                <span>Name</span>
                <span className="file-row-meta">Type</span>
                <span className="file-row-meta">Size</span>
                <span className="text-right">Actions</span>
              </div>
              <ul>
                {prefix && (
                  <li>
                    <button
                      type="button"
                      className="file-row w-full text-left"
                      onClick={() => onNavigate(parentPath(prefix))}
                    >
                      <span className="flex items-center gap-3 text-sm font-semibold text-[var(--color-ink-muted)]">
                        <span className="grid size-7 place-items-center rounded-lg bg-[var(--color-surface-muted)]">
                          ↑
                        </span>
                        Parent folder
                      </span>
                    </button>
                  </li>
                )}
                {folders.map((folder) => (
                  <li key={folder.path}>
                    <button
                      type="button"
                      className="file-row w-full text-left"
                      onClick={() => onNavigate(folder.path)}
                    >
                      <span className="flex min-w-0 items-center gap-3">
                        <FolderGlyph />
                        <span className="truncate text-sm font-semibold">{baseName(folder.path)}</span>
                      </span>
                      <span className="file-row-meta text-sm text-[var(--color-ink-muted)]">Folder</span>
                      <span className="file-row-meta text-sm text-[var(--color-ink-muted)]">—</span>
                      <span className="text-right text-sm text-[var(--color-ink-muted)]">Open</span>
                    </button>
                  </li>
                ))}
                {files.map((file) => (
                  <li key={file.path}>
                    <div
                      className={`file-row ${versionsPath === file.path ? 'bg-[var(--color-brand-soft)]' : ''}`}
                    >
                      <button
                        type="button"
                        className="flex min-w-0 items-center gap-3 text-left"
                        onClick={() => (file.type === 'file' ? onOpenVersions(file.path) : undefined)}
                        disabled={file.type !== 'file'}
                      >
                        <FileGlyph />
                        <span className="min-w-0">
                          <span className="block truncate text-sm font-semibold">{baseName(file.path)}</span>
                          <span className="mt-0.5 block text-xs text-[var(--color-ink-muted)]">
                            {file.type === 'symlink'
                              ? `Link → ${file.link_target || '?'}`
                              : `Changed ${relative(file.mtime)}`}
                          </span>
                        </span>
                      </button>
                      <span className="file-row-meta text-sm text-[var(--color-ink-muted)]">
                        {file.type === 'symlink' ? 'Link' : 'File'}
                      </span>
                      <span className="file-row-meta tabular text-sm text-[var(--color-ink-muted)]">
                        {file.type === 'symlink' ? '—' : bytes(file.size)}
                      </span>
                      <div className="flex items-center justify-end gap-2">
                        {file.type === 'file' && (
                          <>
                            <Button onClick={() => onOpenVersions(file.path)}>Versions</Button>
                            <Button
                              onClick={() =>
                                void download(() => downloadSnapshotFile(snapshotId, file.path))
                              }
                            >
                              Download
                            </Button>
                          </>
                        )}
                      </div>
                    </div>
                  </li>
                ))}
              </ul>
            </>
          )}
          {cursor && (
            <div className="border-t border-[var(--color-border-subtle)] px-5 py-4 text-center">
              <Button
                disabled={more.busy === 'more'}
                onClick={() => {
                  void more.run('more', async () => {
                    const page = await api.browse(snapshotId, prefix, cursor)
                    setExtra({
                      key,
                      entries: [...appended, ...(page.entries ?? [])],
                      cursor: page.next_cursor ?? '',
                    })
                  })
                }}
              >
                {more.busy === 'more' ? 'Loading…' : 'Show more'}
              </Button>
            </div>
          )}
        </div>

        {versionsPath && (
          <VersionPanel
            path={versionsPath}
            deviceId={deviceId}
            currentSnapshotId={snapshotId}
            onClose={onCloseVersions}
          />
        )}
      </div>
    </div>
  )
}

function VersionPanel({
  path,
  deviceId,
  currentSnapshotId,
  onClose,
}: {
  path: string
  deviceId: string
  currentSnapshotId: string
  onClose: () => void
}) {
  const navigate = useNavigate()
  const [dlError, setDlError] = useState<string>()
  const { data, error, loading } = useLoader<FileVersion[]>(
    () => api.fileVersions(path, deviceId || undefined),
    { deps: [path, deviceId] },
  )

  return (
    <aside className="panel flex max-h-[70vh] flex-col overflow-hidden">
      <div className="border-b border-[var(--color-border-subtle)] px-4 py-3">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold">{baseName(path)}</h2>
            <p className="mt-0.5 truncate text-xs text-[var(--color-ink-muted)]" title={path}>
              Version history
            </p>
          </div>
          <Button variant="ghost" onClick={onClose}>
            Close
          </Button>
        </div>
      </div>
      <div className="flex-1 overflow-y-auto p-3">
        {(error || dlError) && <ErrorNote>{dlError || error}</ErrorNote>}
        {loading && !data && <p className="text-sm text-[var(--color-ink-muted)]">Loading versions…</p>}
        {data && data.length === 0 && (
          <Empty title="No versions found" hint="This path is not in any completed backup." />
        )}
        {data && data.length === 1 && (
          <p className="mb-2 text-xs text-[var(--color-ink-muted)]">
            Only one version — the file has not changed across backups yet.
          </p>
        )}
        {data && data.length > 0 && (
          <ul className="space-y-2">
            {data.map((version, index) => (
              <li
                key={version.snapshot.id}
                className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)] p-3"
              >
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold">
                    {index === 0 ? 'Latest content' : `Older · ${absolute(version.snapshot.started_at)}`}
                  </span>
                  {version.snapshot.id === currentSnapshotId && <Badge>viewing</Badge>}
                </div>
                <p className="mt-1 text-xs text-[var(--color-ink-muted)]">
                  {absolute(version.snapshot.started_at)} · {bytes(version.entry.size)}
                </p>
                <div className="mt-3 flex flex-wrap gap-2">
                  <Button
                    onClick={() => {
                      setDlError(undefined)
                      void downloadSnapshotFile(version.snapshot.id, path).catch((err) =>
                        setDlError(message(err, 'Download failed')),
                      )
                    }}
                  >
                    Download
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() =>
                      navigate(
                        `/backups?id=${version.snapshot.id}&path=${encodeURIComponent(parentPath(path))}&file=${encodeURIComponent(path)}${deviceId ? `&device=${deviceId}` : ''}`,
                      )
                    }
                  >
                    Browse then
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  )
}
