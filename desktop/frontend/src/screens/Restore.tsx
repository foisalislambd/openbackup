import { useEffect, useState } from 'react'

import { api, on } from '../lib/bridge'
import { bytes, count, fileName, parentPath, when } from '../lib/format'
import { useAction, useAsync } from '../lib/use-status'
import type { Entry, FileVersion, RestoreProgress, Snapshot } from '../lib/types'
import { Badge, Button, Card, Empty, Notice, Rows, Spinner } from '../components/ui'

/** Restore browses the latest backup like a drive. Click a file for older
 *  versions. Snapshot history stays secondary. */
export function Restore() {
  const [snapshot, setSnapshot] = useState('')
  const [prefix, setPrefix] = useState('')
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [results, setResults] = useState<Entry[] | null>(null)
  const [versionsPath, setVersionsPath] = useState('')
  const [progress, setProgress] = useState<RestoreProgress | null>(null)
  const [tab, setTab] = useState<'files' | 'history'>('files')
  const [extra, setExtra] = useState<{ key: string; entries: Entry[]; cursor: string }>()
  const more = useAction()
  const action = useAction()

  const snapshots = useAsync(() => api.snapshots(), [])
  const completed = snapshots.data?.filter((snap) => snap.status === 'complete') ?? []
  const activeSnapshot = snapshot || completed[0]?.id || ''
  const activeMeta = completed.find((s) => s.id === activeSnapshot)
  const viewingOlder = Boolean(snapshot && completed[0] && snapshot !== completed[0].id)

  const page = useAsync(
    () =>
      activeSnapshot
        ? api.browse(activeSnapshot, prefix)
        : Promise.resolve({ entries: [] as Entry[], next_cursor: '' }),
    [activeSnapshot, prefix],
  )

  useEffect(() => {
    void api.restoreProgress().then(setProgress).catch(() => {})
    return on<RestoreProgress>('restore', setProgress)
  }, [])

  const search = () =>
    action.run(async () => {
      if (!query.trim() || !activeSnapshot) {
        setResults(null)
        return
      }
      setSearching(true)
      try {
        setResults(await api.search(activeSnapshot, query))
      } finally {
        setSearching(false)
      }
    })

  const restore = (path: string, fromSnapshot = activeSnapshot) =>
    action.run(async () => {
      if (!fromSnapshot) return
      const target = await api.chooseRestoreTarget()
      if (!target) return
      await api.startRestore({
        snapshot: fromSnapshot,
        path,
        target,
        conflict: 'skip',
        dry_run: false,
      })
    })

  const openFolder = (path: string) => {
    setResults(null)
    setVersionsPath('')
    setExtra(undefined)
    setPrefix(path.replace(/\/+$/, ''))
  }

  const pageKey = `${activeSnapshot}:${prefix}`
  const appended = extra?.key === pageKey ? extra.entries : []
  const cursor = extra?.key === pageKey ? extra.cursor : page.data?.next_cursor || ''
  const entries = results ?? [...(page.data?.entries ?? []), ...appended]

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      {progress && (progress.running || progress.finished) && (
        <RestoreStatus progress={progress} onCancel={() => void api.cancelRestore()} />
      )}

      <div className="flex flex-wrap gap-2">
        <Button tone={tab === 'files' ? 'primary' : 'quiet'} onClick={() => setTab('files')}>
          My files
        </Button>
        <Button tone={tab === 'history' ? 'primary' : 'quiet'} onClick={() => setTab('history')}>
          Backup history
        </Button>
      </div>

      {tab === 'history' ? (
        <HistoryCard
          loading={snapshots.loading && !snapshots.data}
          completed={completed}
          onBrowse={(id) => {
            setSnapshot(id)
            setPrefix('')
            setResults(null)
            setVersionsPath('')
            setExtra(undefined)
            setTab('files')
          }}
        />
      ) : (
        <Card
          title={activeMeta?.device_name ? `${activeMeta.device_name} files` : 'Get your files back'}
          description={
            activeMeta
              ? `As of ${when(activeMeta.started_at)} · ${count(activeMeta.file_count)} files`
              : 'Browse your latest backup. Restoring never overwrites a file that is already there.'
          }
        >
          {viewingOlder && (
            <div className="mb-4 flex flex-wrap items-center justify-between gap-2 rounded-xl border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm">
              <span>Viewing an earlier backup. Current files may differ.</span>
              <Button
                tone="quiet"
                onClick={() => {
                  setSnapshot('')
                  setVersionsPath('')
                  setExtra(undefined)
                }}
              >
                Back to latest
              </Button>
            </div>
          )}

          <form
            className="mb-4 flex gap-2"
            onSubmit={(event) => {
              event.preventDefault()
              void search()
            }}
          >
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search by file name"
              className="selectable flex-1 rounded-lg border border-border-subtle bg-surface px-3 py-2 text-sm text-ink outline-none focus:border-brand"
            />
            <Button type="submit" busy={searching}>
              Search
            </Button>
            {results && (
              <Button
                tone="quiet"
                onClick={() => {
                  setResults(null)
                  setQuery('')
                }}
              >
                Clear
              </Button>
            )}
          </form>

          {action.error && (
            <div className="mb-4">
              <Notice tone="bad" title="That did not work">
                {action.error}
              </Notice>
            </div>
          )}

          {!results && (
            <div className="mb-3 flex items-center gap-2 text-sm">
              <button
                className="text-brand hover:underline disabled:text-ink-muted disabled:no-underline"
                disabled={!prefix}
                onClick={() => openFolder(parentPath(prefix).replace(/\/+$/, ''))}
              >
                Up
              </button>
              <span className="selectable truncate text-ink-muted" title={prefix || '/'}>
                {prefix ? `/${prefix}` : 'All backed-up folders'}
              </span>
            </div>
          )}

          {page.error && <Notice tone="warn" title="Cannot read this backup">{page.error}</Notice>}

          {!activeSnapshot ? (
            snapshots.error ? (
              <Notice tone="bad" title="Could not load backups">
                {snapshots.error}
              </Notice>
            ) : snapshots.loading ? (
              <div className="flex items-center gap-2 py-6 text-sm text-ink-muted">
                <Spinner /> Loading backups...
              </div>
            ) : (
              <Empty title="No completed backups yet" />
            )
          ) : page.loading && !page.data ? (
            <div className="flex items-center gap-2 py-6 text-sm text-ink-muted">
              <Spinner /> Reading the backup...
            </div>
          ) : entries.length === 0 ? (
            <Empty title={results ? 'Nothing matched that name' : 'This folder is empty in the backup'}>
              {results
                ? 'Try part of the file name, without the folder.'
                : 'Files that were excluded, such as caches and build folders, are not in the backup.'}
            </Empty>
          ) : (
            <div className={versionsPath ? 'grid gap-4 lg:grid-cols-[1fr_16rem]' : ''}>
              <div>
                <Rows>
                  {entries.map((entry) => (
                    <div key={entry.path} className="flex items-center justify-between gap-4 py-2.5">
                      <button
                        className="flex min-w-0 items-center gap-2.5 text-left"
                        onClick={() => {
                          if (entry.type === 'dir') {
                            openFolder(entry.path)
                          } else {
                            setVersionsPath(entry.path)
                          }
                        }}
                      >
                        <Icon dir={entry.type === 'dir'} />
                        <span className="min-w-0">
                          <span className="block truncate text-sm text-ink">{fileName(entry.path)}</span>
                          <span className="block truncate text-xs text-ink-muted">
                            {entry.type === 'dir'
                              ? 'Folder'
                              : `${bytes(entry.size)} · ${when(entry.mtime)}`}
                            {results ? ` · /${parentPath(entry.path).replace(/\/+$/, '') || ''}` : ''}
                          </span>
                        </span>
                      </button>
                      <div className="flex shrink-0 items-center gap-2">
                        {entry.type !== 'dir' && (
                          <Button tone="quiet" onClick={() => setVersionsPath(entry.path)}>
                            Versions
                          </Button>
                        )}
                        <Button
                          tone="quiet"
                          disabled={progress?.running}
                          onClick={() => restore(entry.path)}
                        >
                          Restore
                        </Button>
                      </div>
                    </div>
                  ))}
                </Rows>
                {cursor && !results && (
                  <div className="mt-3 text-center">
                    <Button
                      tone="quiet"
                      busy={more.busy}
                      onClick={() =>
                        void more.run(async () => {
                          const next = await api.browse(activeSnapshot, prefix, cursor)
                          setExtra({
                            key: pageKey,
                            entries: [...appended, ...(next.entries ?? [])],
                            cursor: next.next_cursor ?? '',
                          })
                        })
                      }
                    >
                      Show more
                    </Button>
                  </div>
                )}
              </div>

              {versionsPath && (
                <VersionPanel
                  path={versionsPath}
                  currentSnapshotId={activeSnapshot}
                  onClose={() => setVersionsPath('')}
                  onRestore={(snapId) => restore(versionsPath, snapId)}
                  onBrowse={(snapId) => {
                    setSnapshot(snapId)
                    setPrefix(parentPath(versionsPath).replace(/\/+$/, ''))
                    setResults(null)
                    setExtra(undefined)
                  }}
                  restoring={Boolean(progress?.running)}
                />
              )}
            </div>
          )}
        </Card>
      )}
    </div>
  )
}

function HistoryCard({
  loading,
  completed,
  onBrowse,
}: {
  loading: boolean
  completed: Snapshot[]
  onBrowse: (id: string) => void
}) {
  return (
    <Card
      title="Backup history"
      description="Each row is a point in time — not a separate folder of files."
    >
      {loading ? (
        <div className="flex items-center gap-2 py-4 text-sm text-ink-muted">
          <Spinner /> Loading...
        </div>
      ) : !completed.length ? (
        <Empty title="No completed backups yet" />
      ) : (
        <Rows>
          {completed.slice(0, 20).map((snap, index) => (
            <div key={snap.id} className="flex items-center justify-between gap-4 py-2.5 text-sm">
              <div className="min-w-0">
                <p className="text-ink">
                  {index === 0 ? 'Latest backup' : 'Earlier backup'} · {when(snap.started_at)}
                </p>
                <p className="text-xs text-ink-muted">
                  {count(snap.file_count)} files · {bytes(snap.total_bytes)}
                </p>
              </div>
              <div className="flex items-center gap-2">
                <Badge tone={snap.kind === 'full' ? 'default' : 'good'}>
                  {snap.kind === 'full' ? 'Full' : 'Changes only'}
                </Badge>
                <Button tone="quiet" onClick={() => onBrowse(snap.id)}>
                  Browse
                </Button>
              </div>
            </div>
          ))}
        </Rows>
      )}
    </Card>
  )
}

function VersionPanel({
  path,
  currentSnapshotId,
  onClose,
  onRestore,
  onBrowse,
  restoring,
}: {
  path: string
  currentSnapshotId: string
  onClose: () => void
  onRestore: (snapshotId: string) => void
  onBrowse: (snapshotId: string) => void
  restoring?: boolean
}) {
  const versions = useAsync(() => api.fileVersions(path), [path])
  const list = versions.data ?? []

  return (
    <aside className="rounded-xl border border-border-subtle bg-surface-muted/40 p-3">
      <div className="mb-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold text-ink">{fileName(path)}</p>
          <p className="text-xs text-ink-muted">Version history</p>
        </div>
        <Button tone="quiet" onClick={onClose}>
          Close
        </Button>
      </div>
      {versions.error && (
        <Notice tone="warn" title="Could not load versions">
          {versions.error}
        </Notice>
      )}
      {versions.loading && !versions.data && (
        <div className="flex items-center gap-2 py-4 text-sm text-ink-muted">
          <Spinner /> Loading...
        </div>
      )}
      {!versions.loading && !versions.error && list.length === 0 && (
        <Empty title="No versions found">This path is not in any completed backup.</Empty>
      )}
      {list.length === 1 && (
        <p className="mb-2 text-xs text-ink-muted">
          Only one version — the file has not changed across backups yet.
        </p>
      )}
      {list.length > 0 && (
        <ul className="space-y-2">
          {list.map((version: FileVersion, index: number) => (
            <li key={version.snapshot.id} className="rounded-lg border border-border-subtle bg-surface p-2.5">
              <div className="flex items-center gap-2">
                <span className="text-sm font-semibold text-ink">
                  {index === 0 ? 'Current' : `Version ${list.length - index}`}
                </span>
                {version.snapshot.id === currentSnapshotId && <Badge>viewing</Badge>}
              </div>
              <p className="mt-1 text-xs text-ink-muted">
                {when(version.snapshot.started_at)} · {bytes(version.entry.size)}
              </p>
              <div className="mt-2 flex flex-wrap gap-2">
                <Button
                  tone="quiet"
                  disabled={restoring}
                  onClick={() => onRestore(version.snapshot.id)}
                >
                  Restore
                </Button>
                <Button tone="quiet" onClick={() => onBrowse(version.snapshot.id)}>
                  Browse then
                </Button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </aside>
  )
}

function RestoreStatus({
  progress,
  onCancel,
}: {
  progress: RestoreProgress
  onCancel: () => void
}) {
  if (progress.error) {
    return (
      <Notice tone="bad" title="The restore stopped">
        {progress.error}
      </Notice>
    )
  }
  if (progress.finished) {
    return (
      <Notice
        tone={progress.failed > 0 ? 'warn' : 'good'}
        title={`Restored ${count(progress.restored)} file${progress.restored === 1 ? '' : 's'} to ${progress.target}`}
      >
        {bytes(progress.bytes)} written
        {progress.skipped > 0 && `, ${count(progress.skipped)} skipped because a file already existed`}
        {progress.failed > 0 && `, ${count(progress.failed)} could not be restored`}.
      </Notice>
    )
  }
  const percent = progress.of_files > 0 ? (progress.files / progress.of_files) * 100 : null
  return (
    <Card
      title="Restoring"
      description={progress.current || 'Preparing...'}
      actions={
        <Button tone="quiet" onClick={onCancel}>
          Cancel
        </Button>
      }
    >
      <div className="h-1.5 overflow-hidden rounded-full bg-surface-muted">
        <div
          className="h-full rounded-full bg-brand transition-all"
          style={{ width: `${percent ?? 15}%` }}
        />
      </div>
      <p className="tabular mt-2 text-xs text-ink-muted">
        {count(progress.files)}
        {progress.of_files > 0 && ` of ${count(progress.of_files)}`} files · {bytes(progress.bytes)}
      </p>
    </Card>
  )
}

function Icon({ dir }: { dir: boolean }) {
  return (
    <svg
      viewBox="0 0 24 24"
      className={`h-4 w-4 shrink-0 ${dir ? 'text-brand' : 'text-ink-muted'}`}
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      aria-hidden
    >
      {dir ? (
        <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />
      ) : (
        <path d="M7 3h7l4 4v14a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zm7 0v5h5" />
      )}
    </svg>
  )
}
