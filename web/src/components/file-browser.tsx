import { useEffect, useState } from 'react'
import { LayoutGrid, LayoutList, Search } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import {
  api,
  downloadSnapshotArchive,
  downloadSnapshotFile,
  type Entry,
  type FileVersion,
  type Snapshot,
} from '@/lib/api'
import { absolute, baseName, bytes, count, parentPath, relative } from '@/lib/format'
import { fileKind, kindLabel } from '@/lib/file-kind'
import { message, useAction, useLoader } from '@/lib/use-loader'
import { Badge, Button, Empty, ErrorNote } from '@/components/ui'
import { BrowseSkeleton } from '@/components/skeleton'
import { EntryIcon, LockBadge } from '@/components/file-icon'
import { FilePreview } from '@/components/file-preview'

const VIEW_KEY = 'openbackup-files-layout'

export function FileBrowser({
  snapshot,
  deviceName,
  prefix,
  versionsPath,
  viewingOlder,
  encrypted,
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
  encrypted: boolean
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
      return {
        entries: page.entries ?? [],
        cursor: page.next_cursor ?? '',
        encrypted: page.encrypted,
      }
    },
    { deps: [snapshotId, prefix] },
  )
  const [extra, setExtra] = useState<{ key: string; entries: Entry[]; cursor: string }>()
  const [dlError, setDlError] = useState<string>()
  const [query, setQuery] = useState('')
  const [remote, setRemote] = useState<{
    q: string
    entries: Entry[]
    loading: boolean
    error?: string
  }>()
  const [layout, setLayout] = useState<'grid' | 'list'>(() =>
    typeof window !== 'undefined' && localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'grid',
  )
  const [previewPath, setPreviewPath] = useState<string>()
  const more = useAction()

  useEffect(() => {
    localStorage.setItem(VIEW_KEY, layout)
  }, [layout])

  useEffect(() => {
    const needle = query.trim()
    if (needle.length < 2) return
    let cancel = false
    const timer = window.setTimeout(() => {
      setRemote({ q: needle, entries: [], loading: true })
      api.searchSnapshot(snapshotId, needle).then(
        (entries) => {
          if (!cancel) setRemote({ q: needle, entries, loading: false })
        },
        (err: unknown) => {
          if (!cancel) setRemote({ q: needle, entries: [], loading: false, error: message(err, 'Search failed') })
        },
      )
    }, 250)
    return () => {
      cancel = true
      window.clearTimeout(timer)
    }
  }, [query, snapshotId])

  if (error && !data) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <BrowseSkeleton />

  const locked = encrypted || Boolean(data.encrypted)
  const key = `${snapshotId}:${prefix}`
  const appended = extra?.key === key ? extra.entries : []
  const cursor = extra?.key === key ? extra.cursor : data.cursor
  const entries = [...data.entries, ...appended]
  const needle = query.trim()
  const searching = needle.length >= 2
  const remoteReady = searching && remote?.q === needle && !remote.loading
  const visible = searching
    ? remoteReady
      ? remote.entries
      : []
    : needle
      ? entries.filter((entry) => baseName(entry.path).toLowerCase().includes(needle.toLowerCase()))
      : entries
  const folders = visible
    .filter((e) => e.type === 'dir')
    .slice()
    .sort((a, b) => a.path.localeCompare(b.path))
  const files = visible
    .filter((e) => e.type !== 'dir')
    .slice()
    .sort((a, b) => a.path.localeCompare(b.path))
  const previewEntry = files.find((f) => f.path === previewPath)

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

  const openEntry = (entry: Entry) => {
    if (entry.type === 'dir') onNavigate(entry.path)
    else if (entry.type === 'file') setPreviewPath(entry.path)
  }

  return (
    <div className="space-y-4">
      {viewingOlder && (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-warning-500/30 bg-warning-50 px-4 py-3 text-sm dark:bg-warning-500/10">
          <span>You are looking at an older backup from {absolute(snapshot.started_at)}.</span>
          <Button onClick={onUseLatest}>Back to latest</Button>
        </div>
      )}

      {locked && (
        <div className="flex flex-wrap items-start gap-3 rounded-xl border border-gray-200 bg-gray-50 px-4 py-3 text-sm dark:border-gray-800 dark:bg-gray-900/60">
          <LockBadge />
          <p className="min-w-0 flex-1 text-gray-600 dark:text-gray-400">
            Names are visible. File contents are locked on this server. Restore from the desktop app on a
            computer that has the recovery code.
          </p>
        </div>
      )}

      {(dlError || error) && <ErrorNote>{dlError || error}</ErrorNote>}

      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-lg font-semibold tracking-tight">{deviceName}</div>
          <div className="mt-0.5 text-sm text-gray-500">
            As of {absolute(snapshot.started_at)} · {count(snapshot.file_count)} files · library{' '}
            {bytes(snapshot.total_bytes)}
            {snapshot.uploaded_bytes > 0 ? ` · ${bytes(snapshot.uploaded_bytes)} new that run` : ''}
          </div>
        </div>
        <Button onClick={() => void download(() => downloadSnapshotArchive(snapshotId, prefix))} disabled={locked}>
          {locked ? 'ZIP locked' : `Download ${prefix ? baseName(prefix) : 'all'} ZIP`}
        </Button>
      </div>

      <nav className="flex flex-wrap items-center gap-1 text-sm" aria-label="Folder path">
        <Crumb onClick={onBackToDevices}>Computers</Crumb>
        <span className="text-gray-500">/</span>
        {prefix ? <Crumb onClick={() => onNavigate('')}>{deviceName}</Crumb> : <span className="rounded-lg px-2 py-1 font-semibold">{deviceName}</span>}
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
                  <Crumb onClick={() => onNavigate(path)}>{segment}</Crumb>
                )}
              </span>
            )
          })}
      </nav>

      <div className="flex flex-wrap items-center gap-2">
        <label className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-gray-400" />
          <input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search this backup"
            className="h-10 w-full rounded-lg border border-gray-200 bg-white pr-3 pl-10 text-sm outline-none focus:border-brand-400 dark:border-gray-800 dark:bg-gray-900"
          />
        </label>
        <div className="flex rounded-lg border border-gray-200 p-0.5 dark:border-gray-800">
          <button
            type="button"
            className={`rounded-md p-2 ${layout === 'grid' ? 'bg-gray-100 dark:bg-white/10' : ''}`}
            aria-label="Grid view"
            onClick={() => setLayout('grid')}
          >
            <LayoutGrid size={16} />
          </button>
          <button
            type="button"
            className={`rounded-md p-2 ${layout === 'list' ? 'bg-gray-100 dark:bg-white/10' : ''}`}
            aria-label="List view"
            onClick={() => setLayout('list')}
          >
            <LayoutList size={16} />
          </button>
        </div>
      </div>

      <div className={`grid gap-4 ${versionsPath ? 'xl:grid-cols-[1fr_20rem]' : ''}`}>
        <div className="admin-card overflow-hidden">
          {searching && remote?.loading && remote.q === needle ? (
            <Empty title="Searching this backup…" />
          ) : remote?.error && searching ? (
            <div className="p-4">
              <ErrorNote>{remote.error}</ErrorNote>
            </div>
          ) : folders.length === 0 && files.length === 0 ? (
            <Empty
              title={needle ? 'Nothing matches that name' : 'This folder is empty in the backup'}
              hint={needle ? 'Try another word. Search looks through the whole backup, not only this folder.' : undefined}
            />
          ) : layout === 'grid' ? (
            <div className="file-grid p-3">
              <button type="button" className="file-tile text-gray-500" onClick={goUp}>
                <span className="grid size-10 place-items-center rounded-xl bg-gray-100 text-lg dark:bg-gray-800">↑</span>
                <span className="text-sm font-semibold">{prefix ? 'Parent folder' : 'All computers'}</span>
              </button>
              {folders.map((folder) => (
                <button key={folder.path} type="button" className="file-tile" onClick={() => onNavigate(folder.path)}>
                  <EntryIcon path={folder.path} type="dir" locked={locked} large />
                  <span className="line-clamp-2 text-sm font-semibold">{baseName(folder.path)}</span>
                </button>
              ))}
              {files.map((file) => (
                <button
                  key={file.path}
                  type="button"
                  className={`file-tile ${versionsPath === file.path ? 'ring-2 ring-brand-400' : ''}`}
                  onClick={() => openEntry(file)}
                >
                  <EntryIcon path={file.path} type={file.type} locked={locked} large />
                  <span className="line-clamp-2 text-sm font-semibold">{baseName(file.path)}</span>
                  {searching && (
                    <span className="line-clamp-2 text-[11px] text-gray-500">{file.path}</span>
                  )}
                  <span className="text-[11px] text-gray-500">
                    {locked ? 'Locked' : file.type === 'symlink' ? 'Link' : bytes(file.size)}
                  </span>
                </button>
              ))}
            </div>
          ) : (
            <ul>
              <li>
                <button type="button" className="file-row w-full text-left" onClick={goUp}>
                  <span className="flex items-center gap-3 text-sm font-semibold text-gray-500">
                    <span className="grid size-7 place-items-center rounded-lg bg-gray-100 dark:bg-gray-800">↑</span>
                    {prefix ? 'Parent folder' : 'All computers'}
                  </span>
                </button>
              </li>
              {folders.map((folder) => (
                <li key={folder.path}>
                  <button type="button" className="file-row w-full text-left" onClick={() => onNavigate(folder.path)}>
                    <span className="flex min-w-0 items-center gap-3">
                      <EntryIcon path={folder.path} type="dir" locked={locked} />
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
                  <div className={`file-row ${versionsPath === file.path ? 'bg-brand-50 dark:bg-brand-500/15' : ''}`}>
                    <button type="button" className="flex min-w-0 items-center gap-3 text-left" onClick={() => openEntry(file)}>
                      <EntryIcon path={file.path} type={file.type} locked={locked} />
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-semibold">{baseName(file.path)}</span>
                        <span className="mt-0.5 block text-xs text-gray-500">
                          {file.type === 'symlink'
                            ? `Link → ${file.link_target || '?'}`
                            : locked
                              ? 'Encrypted · restore on a trusted device'
                              : `${kindLabel(fileKind(file.path, file.type))} · changed ${relative(file.mtime)}`}
                        </span>
                      </span>
                    </button>
                    <span className="file-row-meta text-sm text-gray-500">
                      {locked ? 'Locked' : file.type === 'symlink' ? 'Link' : kindLabel(fileKind(file.path, file.type))}
                    </span>
                    <span className="file-row-meta tabular text-sm text-gray-500">
                      {file.type === 'symlink' ? '—' : bytes(file.size)}
                    </span>
                    <div className="flex items-center justify-end gap-2">
                      {file.type === 'file' && (
                        <>
                          <Button onClick={() => onOpenVersions(file.path)}>Versions</Button>
                          <Button
                            disabled={locked}
                            onClick={() => void download(() => downloadSnapshotFile(snapshotId, file.path))}
                          >
                            {locked ? 'Locked' : 'Download'}
                          </Button>
                        </>
                      )}
                    </div>
                  </div>
                </li>
              ))}
            </ul>
          )}
          {cursor && !searching && (
            <div className="border-t border-gray-200 px-5 py-4 text-center dark:border-gray-800">
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
            locked={locked}
            onClose={onCloseVersions}
          />
        )}
      </div>

      {previewEntry && (
        <FilePreview
          key={previewEntry.path}
          snapshotId={snapshotId}
          path={previewEntry.path}
          size={previewEntry.size}
          locked={locked}
          onClose={() => setPreviewPath(undefined)}
          onDownload={() => void download(() => downloadSnapshotFile(snapshotId, previewEntry.path))}
          onVersions={() => {
            setPreviewPath(undefined)
            onOpenVersions(previewEntry.path)
          }}
        />
      )}
    </div>
  )
}

function Crumb({ children, onClick }: { children: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="rounded-lg px-2 py-1 font-semibold text-brand-500 hover:bg-brand-50 dark:hover:bg-brand-500/15"
      onClick={onClick}
    >
      {children}
    </button>
  )
}

function VersionPanel({
  path,
  deviceId,
  currentSnapshotId,
  locked,
  onClose,
}: {
  path: string
  deviceId: string
  currentSnapshotId: string
  locked: boolean
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
      <div className="border-b border-gray-200 px-4 py-3 dark:border-gray-800">
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
        {locked && (
          <p className="mb-3 text-xs text-gray-500">Versions are listed, but this website cannot download encrypted files.</p>
        )}
        {loading && !data && <p className="text-sm text-gray-500">Loading versions…</p>}
        {data && data.length === 0 && (
          <Empty title="No versions found" hint="This path is not in any completed backup." />
        )}
        {data && data.length === 1 && <p className="mb-2 text-xs text-gray-500">Only one version so far.</p>}
        {data && data.length > 0 && (
          <ul className="space-y-2">
            {data.map((version, index) => (
              <li
                key={version.snapshot.id}
                className="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-gray-900"
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
                    disabled={locked}
                    onClick={() => {
                      setDlError(undefined)
                      void downloadSnapshotFile(version.snapshot.id, path).catch((err) =>
                        setDlError(message(err, 'Download failed')),
                      )
                    }}
                  >
                    {locked ? 'Locked' : 'Download'}
                  </Button>
                  <Button
                    variant="ghost"
                    onClick={() =>
                      navigate(
                        `/files?id=${version.snapshot.id}&path=${encodeURIComponent(parentPath(path))}&file=${encodeURIComponent(path)}${deviceId ? `&device=${deviceId}` : ''}`,
                      )
                    }
                  >
                    Browse that backup
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
