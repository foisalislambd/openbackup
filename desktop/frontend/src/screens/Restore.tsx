import { useEffect, useState } from 'react'

import { api, on } from '../lib/bridge'
import { bytes, count, fileName, parentPath, when } from '../lib/format'
import { useAction, useAsync } from '../lib/use-status'
import type { Entry, RestoreProgress } from '../lib/types'
import { Badge, Button, Card, Empty, Notice, Rows, Spinner } from '../components/ui'

/** Restore is the screen that justifies the whole product.
 *
 *  It is built around browsing rather than searching a snapshot list, because the
 *  question people arrive with is "where is my file", not "which backup version do
 *  I want". The version picker is there, but out of the way. */
export function Restore() {
  const [snapshot, setSnapshot] = useState('')
  const [prefix, setPrefix] = useState('')
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [results, setResults] = useState<Entry[] | null>(null)
  const [progress, setProgress] = useState<RestoreProgress | null>(null)
  const action = useAction()

  const snapshots = useAsync(() => api.snapshots(), [])
  const page = useAsync(() => api.browse(snapshot, prefix), [snapshot, prefix])

  useEffect(() => {
    void api.restoreProgress().then(setProgress).catch(() => {})
    return on<RestoreProgress>('restore', setProgress)
  }, [])

  const search = () =>
    action.run(async () => {
      if (!query.trim()) {
        setResults(null)
        return
      }
      setSearching(true)
      try {
        setResults(await api.search(snapshot, query))
      } finally {
        setSearching(false)
      }
    })

  const restore = (path: string) =>
    action.run(async () => {
      const target = await api.chooseRestoreTarget()
      if (!target) return
      await api.startRestore({
        snapshot,
        path,
        target,
        // Skipping is the default because a restore must never destroy the file
        // someone still had; the alternatives are offered while it runs.
        conflict: 'skip',
        dry_run: false,
      })
    })

  const entries = results ?? page.data?.entries ?? []
  const completed = snapshots.data?.filter((snap) => snap.status === 'complete') ?? []

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      {progress && (progress.running || progress.finished) && (
        <RestoreStatus progress={progress} onCancel={() => void api.cancelRestore()} />
      )}

      <Card
        title="Get your files back"
        description="Browse a backup and restore anything from it. Restoring never overwrites a file that is already there."
        actions={
          <select
            value={snapshot}
            onChange={(event) => {
              setSnapshot(event.target.value)
              setPrefix('')
              setResults(null)
            }}
            className="rounded-lg border border-border-subtle bg-surface px-2.5 py-1.5 text-sm text-ink"
          >
            <option value="">Most recent backup</option>
            {completed.map((snap) => (
              <option key={snap.id} value={snap.id}>
                {when(snap.started_at)} - {count(snap.file_count)} files
              </option>
            ))}
          </select>
        }
      >
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
              onClick={() => setPrefix(parentPath(prefix))}
            >
              Up
            </button>
            <span className="selectable truncate text-ink-muted" title={prefix || '/'}>
              {prefix ? `/${prefix}` : 'All backed-up folders'}
            </span>
          </div>
        )}

        {page.error && <Notice tone="warn" title="Cannot read this backup">{page.error}</Notice>}

        {page.loading && !page.data ? (
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
          <Rows>
            {entries.map((entry) => (
              <div key={entry.path} className="flex items-center justify-between gap-4 py-2.5">
                <button
                  className="flex min-w-0 items-center gap-2.5 text-left"
                  disabled={entry.type !== 'dir'}
                  onClick={() => {
                    setResults(null)
                    setPrefix(entry.path.endsWith('/') ? entry.path : `${entry.path}/`)
                  }}
                >
                  <Icon dir={entry.type === 'dir'} />
                  <span className="min-w-0">
                    <span className="block truncate text-sm text-ink">{fileName(entry.path)}</span>
                    <span className="block truncate text-xs text-ink-muted">
                      {entry.type === 'dir' ? 'Folder' : `${bytes(entry.size)} - ${when(entry.mtime)}`}
                      {results ? ` - /${parentPath(entry.path) || ''}` : ''}
                    </span>
                  </span>
                </button>
                <Button
                  tone="quiet"
                  disabled={progress?.running}
                  onClick={() => restore(entry.path)}
                >
                  Restore
                </Button>
              </div>
            ))}
          </Rows>
        )}

        {page.data?.next_cursor && !results && (
          <p className="mt-3 text-xs text-ink-muted">
            Showing the first {entries.length} items in this folder.
          </p>
        )}
      </Card>

      <Card title="Backup history" description="Every backup you can restore from.">
        {snapshots.loading && !snapshots.data ? (
          <div className="flex items-center gap-2 py-4 text-sm text-ink-muted">
            <Spinner /> Loading...
          </div>
        ) : !completed.length ? (
          <Empty title="No completed backups yet" />
        ) : (
          <Rows>
            {completed.slice(0, 12).map((snap) => (
              <div key={snap.id} className="flex items-center justify-between gap-4 py-2.5 text-sm">
                <div className="min-w-0">
                  <p className="text-ink">{when(snap.started_at)}</p>
                  <p className="text-xs text-ink-muted">
                    {count(snap.file_count)} files - {bytes(snap.total_bytes)}
                  </p>
                </div>
                <div className="flex items-center gap-2">
                  <Badge tone={snap.kind === 'full' ? 'default' : 'good'}>
                    {snap.kind === 'full' ? 'Full' : 'Changes only'}
                  </Badge>
                  <Button
                    tone="quiet"
                    onClick={() => {
                      setSnapshot(snap.id)
                      setPrefix('')
                      setResults(null)
                    }}
                  >
                    Browse
                  </Button>
                </div>
              </div>
            ))}
          </Rows>
        )}
      </Card>
    </div>
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
        {progress.of_files > 0 && ` of ${count(progress.of_files)}`} files - {bytes(progress.bytes)}
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
