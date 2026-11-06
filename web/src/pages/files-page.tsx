/**
 * My files: computers first, then that device's folders and versions.
 */

import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, type Device, type Snapshot } from '@/lib/api'
import { absolute, bytes, count, relative } from '@/lib/format'
import { isEncryptedDevice } from '@/lib/file-kind'
import { useAction, useLoader } from '@/lib/use-loader'
import { Badge, Button, Empty, ErrorNote } from '@/components/ui'
import { useToast } from '@/lib/toast-context'
import { BackupsSkeleton } from '@/components/skeleton'
import { EntryIcon, LockBadge } from '@/components/file-icon'
import { FileBrowser } from '@/components/file-browser'

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
  const toast = useToast()

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

  // "Viewing older" must compare against the newest snapshot of the device the
  // viewed snapshot actually belongs to; when the override comes from another
  // device (deep link), comparing to this device's latest is simply wrong.
  const viewingOlderSnapshot =
    Boolean(snapshotOverride) && snapshotOverride !== latestByDeviceId(complete, snapshotOverride)

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
          onPin={(snapshot) => {
            const next = !snapshot.pinned
            void run(`pin-${snapshot.id}`, () => api.pinSnapshot(snapshot.id, next), () => {
              toast.push(next ? 'This backup will be kept' : 'This backup can expire again')
              reload()
            })
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
          key={`${latest.id}:${prefix}`}
          snapshot={latest}
          deviceName={activeDevice?.name || latest.device_name || 'This computer'}
          prefix={prefix}
          versionsPath={versionsPath}
          viewingOlder={viewingOlderSnapshot}
          encrypted={Boolean(latest.encrypted || isEncryptedDevice(activeDevice))}
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

/** The newest completed snapshot on the device that owns snapshotId. */
function latestByDeviceId(complete: Snapshot[], snapshotId: string): string | undefined {
  const snap = complete.find((s) => s.id === snapshotId)
  if (!snap) return undefined
  return complete.find((s) => s.device_id === snap.device_id)?.id
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
      <div className="border-b border-gray-200 px-5 py-4 dark:border-gray-800">
        <h2 className="text-sm font-semibold">Computers</h2>
        <p className="mt-1 text-xs text-gray-500 sm:text-sm">
          Open a computer like a folder. Encrypted devices show a lock — names are visible, contents are not.
        </p>
      </div>
      <div className="file-grid p-4">
        {devices.map((device) => {
          const snaps = snapshots.filter((s) => s.device_id === device.id)
          const latest = snaps[0]
          const locked = isEncryptedDevice(device) || Boolean(latest?.encrypted)
          return (
            <button
              key={device.id}
              type="button"
              className="file-tile"
              onClick={() => onOpen(device.id)}
            >
              <EntryIcon path={device.name} type="dir" locked={locked} large />
              <span className="min-w-0">
                <span className="block truncate text-sm font-semibold">{device.name}</span>
                <span className="mt-0.5 block text-xs text-gray-500">
                  {latest ? relative(latest.started_at) : 'No backup yet'}
                </span>
                {locked && (
                  <span className="mt-1 inline-flex">
                    <LockBadge />
                  </span>
                )}
              </span>
            </button>
          )
        })}
      </div>
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
  onPin,
}: {
  snapshots: Snapshot[]
  showDevice: boolean
  filterDevice?: Device
  busy?: string | null
  onClearDevice: () => void
  onOpen: (snapshot: Snapshot) => void
  onDelete: (id: string) => void
  onPin: (snapshot: Snapshot) => void
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
      <div className="border-b border-gray-200 px-5 py-4 dark:border-gray-800">
        <h2 className="text-sm font-semibold">Backup history</h2>
        <p className="mt-1 text-xs text-gray-500 sm:text-sm">
          {filterDevice
            ? `History for ${filterDevice.name}. “This backup” is new data uploaded that run.`
            : '“This backup” is new data uploaded that run — not the whole library again.'}
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
      <div className="file-row border-b border-gray-200 text-[0.7rem] font-semibold uppercase tracking-[0.06em] text-gray-500 dark:border-gray-800">
        <span>Name</span>
        <span className="file-row-meta">Files</span>
        <span className="file-row-meta">This backup</span>
        <span className="text-right">Actions</span>
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
          const uploaded = snapshot.uploaded_bytes ?? 0

          return (
            <li key={snapshot.id}>
              <div className="file-row group">
                <button
                  type="button"
                  className="flex min-w-0 items-center gap-3 text-left disabled:cursor-default"
                  disabled={!complete}
                  onClick={() => onOpen(snapshot)}
                >
                  <EntryIcon path={snapshot.device_name || 'backup'} type="dir" />
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
                      {snapshot.kind === 'full' && <Badge>full scan</Badge>}
                      {snapshot.status === 'running' && <Badge tone="warn">running</Badge>}
                      {snapshot.status === 'failed' && <Badge tone="bad">failed</Badge>}
                      {snapshot.pinned && <Badge tone="brand">kept</Badge>}
                    </span>
                  </span>
                </button>
                <span className="file-row-meta tabular text-sm text-gray-500">
                  {complete ? count(snapshot.file_count) : '—'}
                </span>
                <span className="file-row-meta min-w-0 text-sm text-gray-500">
                  {complete ? (
                    <span className="block text-right sm:text-left">
                      <span className="tabular font-medium text-gray-800 dark:text-gray-200">
                        {uploaded > 0 ? bytes(uploaded) : '0 B'}
                      </span>
                      <span className="mt-0.5 block text-[11px] text-gray-400">
                        new · library {bytes(snapshot.total_bytes)}
                      </span>
                    </span>
                  ) : (
                    '—'
                  )}
                </span>
                <div className="flex items-center justify-end gap-2">
                  {complete && (
                    <Button onClick={() => onPin(snapshot)}>{snapshot.pinned ? 'Unpin' : 'Keep'}</Button>
                  )}
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

