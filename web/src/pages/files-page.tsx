/**
 * My files: computers first, then that device's folders and versions.
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

export default function FilesPage() {
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
    navigate(qs ? `/files?${qs}` : '/files')
  }

  if (error && !data) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <BackupsSkeleton />

  const complete = data.snapshots.filter((s) => s.status === 'complete')
  if (complete.length === 0) {
    const inProgress = data.snapshots.some((s) => s.status === 'running')
    return (
      <div className="admin-card">
        <Empty
          title="No files backed up yet"
          hint={
            inProgress
              ? 'Backup in progress — files appear when it finishes.'
              : 'Connect a device and finish the first backup.'
          }
        />
      </div>
    )
  }

  const deviceOptions = devicesWithBackups(data.devices, complete)
  const activeDevice = deviceOptions.find((d) => d.id === deviceId)
  const deviceSnaps = complete.filter((s) => s.device_id === deviceId)
  const latest = snapshotOverride
    ? deviceSnaps.find((s) => s.id === snapshotOverride) ||
      complete.find((s) => s.id === snapshotOverride)
    : deviceSnaps[0]

  const historySnaps = data.snapshots
    .filter((s) => !deviceId || s.device_id === deviceId)
    .filter((s) => s.status === 'complete' || s.status === 'failed' || s.status === 'running')

  return (
    <div className="space-y-4">
      {(actionError || error) && <ErrorNote>{actionError || error}</ErrorNote>}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant={view === 'files' ? 'primary' : 'ghost'}
            onClick={() =>
              setQuery({
                view: undefined,
                device: undefined,
                path: undefined,
                id: undefined,
                file: undefined,
              })
            }
          >
            My files
          </Button>
          <Button
            variant={view === 'history' ? 'primary' : 'ghost'}
            onClick={() =>
              setQuery({
                view: 'history',
                path: undefined,
                file: undefined,
                id: undefined,
              })
            }
          >
            Backup history
          </Button>
        </div>
      </div>

      {view === 'history' ? (
        <HistoryList
          snapshots={historySnaps}
          showDevice={!deviceId}
          filterDevice={activeDevice}
          busy={busy}
          onClearDevice={() => setQuery({ device: undefined })}
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
                'Delete this backup?\n\nLater backups that depend on it may also be removed.',
              )
            ) {
              return
            }
            void run(id, () => api.deleteSnapshot(id), reload)
          }}
        />
      ) : !deviceId ? (
        <DeviceFolders
          devices={deviceOptions}
          snapshots={complete}
          onOpen={(id) =>
            setQuery({ device: id, path: undefined, file: undefined, id: undefined })
          }
        />
      ) : !latest ? (
        <div className="admin-card">
          <Empty
            title="No completed backup for this device"
            hint="Pick another computer, or wait for the first backup."
          />
          <div className="border-t border-gray-200 dark:border-gray-800 px-5 py-4">
            <Button onClick={() => setQuery({ device: undefined, path: undefined, file: undefined })}>
              All computers
            </Button>
          </div>
        </div>
      ) : (
        <FileBrowser
          snapshot={latest}
          deviceName={activeDevice?.name || latest.device_name || 'This computer'}
          prefix={prefix}
          versionsPath={versionsPath}
          viewingOlder={Boolean(snapshotOverride && snapshotOverride !== deviceSnaps[0]?.id)}
          onBackToDevices={() =>
            setQuery({ device: undefined, path: undefined, file: undefined, id: undefined })
          }
          onNavigate={(path) => setQuery({ path: path || undefined, file: undefined })}
          onOpenVersions={(path) => setQuery({ file: path })}
          onCloseVersions={() => setQuery({ file: undefined })}
          onUseLatest={() => setQuery({ id: undefined, file: undefined })}
          deviceId={deviceId}
        />
      )}
    </div>
  )
}

/** Devices that have at least one completed backup, newest backup first. */
function devicesWithBackups(devices: Device[], complete: Snapshot[]): Device[] {
  const byId = new Map(devices.map((d) => [d.id, d]))
  const order: string[] = []
  const seen = new Set<string>()
  for (const snap of complete) {
    if (seen.has(snap.device_id)) continue
    seen.add(snap.device_id)
    order.push(snap.device_id)
  }
  return order.map((id) => {
    const known = byId.get(id)
    if (known) return known
    const snap = complete.find((s) => s.device_id === id)
    return {
      id,
      name: snap?.device_name || 'Removed device',
      hostname: '',
      platform: '',
      created_at: '',
      state: 'unknown',
      health: 'unknown',
    } satisfies Device
  })
}

function DeviceFolders({
  devices,
  snapshots,
  onOpen,
}: {
  devices: Device[]
  snapshots: Snapshot[]
  onOpen: (deviceId: string) => void
}) {
  if (devices.length === 0) {
    return (
      <div className="admin-card">
        <Empty title="No computers with backups yet" />
      </div>
    )
  }

  return (
    <div className="admin-card overflow-hidden">
      <div className="border-b border-gray-200 dark:border-gray-800 px-5 py-4">
        <h2 className="text-sm font-semibold">Computers</h2>
        <p className="mt-1 text-xs text-gray-500 sm:text-sm">One folder per device.</p>
      </div>
      <div className="file-row border-b border-gray-200 dark:border-gray-800 text-[0.7rem] font-semibold uppercase tracking-[0.06em] text-gray-500">
        <span>Name</span>
        <span className="file-row-meta">Last backup</span>
        <span className="file-row-meta">Size</span>
        <span className="text-right">Actions</span>
      </div>
      <ul>
        {devices.map((device) => {
          const snaps = snapshots.filter((s) => s.device_id === device.id)
          const latest = snaps[0]
          return (
            <li key={device.id}>
              <button type="button" className="file-row w-full text-left" onClick={() => onOpen(device.id)}>
                <span className="flex min-w-0 items-center gap-3">
                  <FolderGlyph />
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-semibold">{device.name}</span>
                    <span className="mt-0.5 block text-xs text-gray-500">
                      {count(snaps.length)} backup{snaps.length === 1 ? '' : 's'}
                      {device.platform ? ` · ${device.platform}` : ''}
                    </span>
                  </span>
                </span>
                <span className="file-row-meta text-sm text-gray-500">
                  {latest ? absolute(latest.started_at) : '—'}
                </span>
                <span className="file-row-meta tabular text-sm text-gray-500">
                  {latest ? bytes(latest.total_bytes) : '—'}
                </span>
                <span className="text-right text-sm text-gray-500">Open</span>
              </button>
            </li>
          )
        })}
      </ul>
    </div>
  )
}

function HistoryList({
  snapshots,
  showDevice,
  filterDevice,
  busy,
  onClearDevice,
  onOpen,
  onDelete,
}: {
  snapshots: Snapshot[]
  showDevice: boolean
  filterDevice?: Device
  busy?: string | null
  onClearDevice: () => void
  onOpen: (snapshot: Snapshot) => void
  onDelete: (id: string) => void
}) {
  if (snapshots.length === 0) {
    return (
      <div className="admin-card">
        <Empty title="No backups yet" />
      </div>
    )
  }

  return (
    <div className="admin-card overflow-hidden">
      <div className="border-b border-gray-200 dark:border-gray-800 px-5 py-4">
        <h2 className="text-sm font-semibold">Backup history</h2>
        <p className="mt-1 text-xs text-gray-500 sm:text-sm">
          {filterDevice ? `History for ${filterDevice.name}.` : 'Open a point in time to browse files.'}
        </p>
        {filterDevice && (
          <button
            type="button"
            className="mt-2 text-sm font-semibold text-brand-500 hover:underline"
            onClick={onClearDevice}
          >
            Show all computers
          </button>
        )}
      </div>
      <ul>
        {snapshots.map((snapshot, index) => {
          const complete = snapshot.status === 'complete'
          const firstComplete = snapshots.findIndex((s) => s.status === 'complete')
          const title =
            snapshot.status === 'running'
              ? 'Backup in progress'
              : snapshot.status === 'failed'
                ? 'Failed backup'
                : index === firstComplete
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
                    <span className="block truncate text-sm font-semibold group-hover:text-brand-500">
                      {title}
                      {(showDevice || filterDevice) && snapshot.device_name
                        ? ` · ${snapshot.device_name}`
                        : ''}
                    </span>
                    <span className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-gray-500">
                      {absolute(snapshot.started_at)}
                      {snapshot.kind === 'delta' && <Badge>changes only</Badge>}
                      {snapshot.kind === 'full' && <Badge>full</Badge>}
                      {snapshot.status === 'running' && <Badge tone="warn">running</Badge>}
                      {snapshot.status === 'failed' && <Badge tone="bad">failed</Badge>}
                    </span>
                  </span>
                </button>
                <span className="file-row-meta tabular text-sm text-gray-500">
                  {complete ? count(snapshot.file_count) : '—'}
                </span>
                <span className="file-row-meta tabular text-sm text-gray-500">
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
  deviceName,
  prefix,
  versionsPath,
  viewingOlder,
  onBackToDevices,
  onNavigate,
  onOpenVersions,
  onCloseVersions,
  onUseLatest,
  deviceId,
}: {
  snapshot: Snapshot
  deviceName: string
  prefix: string
  versionsPath: string
  viewingOlder: boolean
  onBackToDevices: () => void
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

  const goUp = () => {
    if (prefix) onNavigate(parentPath(prefix))
    else onBackToDevices()
  }

  return (
    <div className="space-y-4">
      {viewingOlder && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-warning-500/30 bg-warning-50 dark:bg-warning-500/10 px-4 py-3 text-sm">
          <span>
            Viewing older backup from {absolute(snapshot.started_at)}.
          </span>
          <Button onClick={onUseLatest}>Back to latest</Button>
        </div>
      )}

      {(dlError || error) && <ErrorNote>{dlError || error}</ErrorNote>}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-lg font-semibold tracking-tight">{deviceName}</div>
          <div className="mt-0.5 text-sm text-gray-500">
            As of {absolute(snapshot.started_at)} · {count(snapshot.file_count)} files ·{' '}
            {bytes(snapshot.total_bytes)}
          </div>
        </div>
        <Button onClick={() => void download(() => downloadSnapshotArchive(snapshotId, prefix))}>
          Download {prefix ? baseName(prefix) : 'all'} ZIP
        </Button>
      </div>

      <nav className="flex flex-wrap items-center gap-1 text-sm">
        <button
          type="button"
          className="rounded-lg px-2 py-1 font-semibold text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-500/15"
          onClick={onBackToDevices}
        >
          Computers
        </button>
        <span className="text-gray-500">/</span>
        {prefix ? (
          <button
            type="button"
            className="rounded-lg px-2 py-1 font-semibold text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-500/15"
            onClick={() => onNavigate('')}
          >
            {deviceName}
          </button>
        ) : (
          <span className="rounded-lg px-2 py-1 font-semibold">{deviceName}</span>
        )}
        {prefix
          .split('/')
          .filter(Boolean)
          .map((segment, index, all) => {
            const path = all.slice(0, index + 1).join('/')
            const isLast = index === all.length - 1
            return (
              <span key={path} className="flex items-center gap-1">
                <span className="text-gray-500">/</span>
                {isLast ? (
                  <span className="rounded-lg px-2 py-1 font-semibold">{segment}</span>
                ) : (
                  <button
                    type="button"
                    className="rounded-lg px-2 py-1 font-semibold text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-500/15"
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
        <div className="admin-card overflow-hidden">
          <div className="file-row border-b border-gray-200 dark:border-gray-800 text-[0.7rem] font-semibold uppercase tracking-[0.06em] text-gray-500">
            <span>Name</span>
            <span className="file-row-meta">Type</span>
            <span className="file-row-meta">Size</span>
            <span className="text-right">Actions</span>
          </div>
          <ul>
            <li>
              <button type="button" className="file-row w-full text-left" onClick={goUp}>
                <span className="flex items-center gap-3 text-sm font-semibold text-gray-500">
                  <span className="grid size-7 place-items-center rounded-lg bg-gray-100 dark:bg-gray-800">
                    ↑
                  </span>
                  {prefix ? 'Parent folder' : 'All computers'}
                </span>
              </button>
            </li>
            {folders.length === 0 && files.length === 0 ? (
              <li className="px-5 py-8">
                <Empty title="This folder is empty in the backup" />
              </li>
            ) : (
              <>
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
                      <span className="file-row-meta text-sm text-gray-500">Folder</span>
                      <span className="file-row-meta text-sm text-gray-500">—</span>
                      <span className="text-right text-sm text-gray-500">Open</span>
                    </button>
                  </li>
                ))}
                {files.map((file) => (
                  <li key={file.path}>
                    <div
                      className={`file-row ${versionsPath === file.path ? 'bg-brand-50 dark:bg-brand-500/15' : ''}`}
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
                          <span className="mt-0.5 block text-xs text-gray-500">
                            {file.type === 'symlink'
                              ? `Link → ${file.link_target || '?'}`
                              : `Changed ${relative(file.mtime)}`}
                          </span>
                        </span>
                      </button>
                      <span className="file-row-meta text-sm text-gray-500">
                        {file.type === 'symlink' ? 'Link' : 'File'}
                      </span>
                      <span className="file-row-meta tabular text-sm text-gray-500">
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
              </>
            )}
          </ul>
          {cursor && (
            <div className="border-t border-gray-200 dark:border-gray-800 px-5 py-4 text-center">
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
    <aside className="admin-card flex max-h-[70vh] flex-col overflow-hidden">
      <div className="border-b border-gray-200 dark:border-gray-800 px-4 py-3">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h2 className="truncate text-sm font-semibold">{baseName(path)}</h2>
            <p className="mt-0.5 truncate text-xs text-gray-500" title={path}>
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
        {loading && !data && <p className="text-sm text-gray-500">Loading versions…</p>}
        {data && data.length === 0 && (
          <Empty title="No versions found" hint="This path is not in any completed backup." />
        )}
        {data && data.length === 1 && (
          <p className="mb-2 text-xs text-gray-500">
            Only one version so far.
          </p>
        )}
        {data && data.length > 0 && (
          <ul className="space-y-2">
            {data.map((version, index) => (
              <li
                key={version.snapshot.id}
                className="rounded-xl border border-gray-200 dark:border-gray-800 bg-white dark:bg-gray-900 p-3"
              >
                <div className="flex items-center gap-2">
                  <span className="text-sm font-semibold">
                    {index === 0 ? 'Latest content' : `Older · ${absolute(version.snapshot.started_at)}`}
                  </span>
                  {version.snapshot.id === currentSnapshotId && <Badge>viewing</Badge>}
                </div>
                <p className="mt-1 text-xs text-gray-500">
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
                        `/files?id=${version.snapshot.id}&path=${encodeURIComponent(parentPath(path))}&file=${encodeURIComponent(path)}${deviceId ? `&device=${deviceId}` : ''}`,
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
