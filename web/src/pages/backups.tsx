/**
 * My files: browse backups and walk folders like a cloud drive library.
 */

import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { api, archiveUrl, downloadUrl, type Entry, type Snapshot } from '@/lib/api'
import { absolute, baseName, bytes, count, parentPath, relative } from '@/lib/format'
import { useAction, useLoader } from '@/lib/use-loader'
import { Badge, Button, Empty, ErrorNote, FileGlyph, FolderGlyph } from '@/components/ui'
import { BackupsSkeleton, BrowseSkeleton } from '@/components/skeleton'

export default function BackupsPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const selected = params.get('id') ?? ''
  const prefix = params.get('path') ?? ''

  const { data: snapshots, error, loading, reload } = useLoader<Snapshot[]>(() => api.snapshots())
  const { busy, error: actionError, run } = useAction()

  if (selected) {
    return (
      <SnapshotBrowser
        snapshotId={selected}
        prefix={prefix}
        onNavigate={(path) =>
          navigate(`/backups?id=${selected}${path ? `&path=${encodeURIComponent(path)}` : ''}`)
        }
        onClose={() => navigate('/backups')}
      />
    )
  }

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !snapshots) return <BackupsSkeleton />
  if (snapshots.length === 0) {
    return (
      <div className="panel">
        <Empty title="No backups yet" hint="They will appear here after the first backup finishes." />
      </div>
    )
  }

  return (
    <div className="space-y-4">
      {actionError && <ErrorNote>{actionError}</ErrorNote>}

      <div className="panel overflow-hidden">
        <div className="file-row border-b border-[var(--color-border-subtle)] text-[0.7rem] font-semibold uppercase tracking-[0.06em] text-[var(--color-ink-muted)]">
          <span>Name</span>
          <span className="file-row-meta">Files</span>
          <span className="file-row-meta">Size</span>
          <span className="text-right">Modified</span>
        </div>

        <ul>
          {snapshots.map((snapshot) => (
            <li key={snapshot.id}>
              <div className="file-row group">
                <button
                  type="button"
                  className="flex min-w-0 items-center gap-3 text-left"
                  onClick={() => navigate(`/backups?id=${snapshot.id}`)}
                >
                  <FolderGlyph />
                  <span className="min-w-0">
                    <span className="block truncate text-sm font-semibold group-hover:text-[var(--color-brand)]">
                      {snapshot.device_name ?? 'device'}
                    </span>
                    <span className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-[var(--color-ink-muted)]">
                      {absolute(snapshot.started_at)}
                      {snapshot.status !== 'complete' && (
                        <Badge tone={snapshot.status === 'failed' ? 'bad' : 'warn'}>{snapshot.status}</Badge>
                      )}
                      {snapshot.kind === 'delta' && <Badge>incremental</Badge>}
                    </span>
                  </span>
                </button>
                <span className="file-row-meta tabular text-sm text-[var(--color-ink-muted)]">
                  {count(snapshot.file_count)}
                </span>
                <span className="file-row-meta tabular text-sm text-[var(--color-ink-muted)]">
                  {bytes(snapshot.total_bytes)}
                </span>
                <div className="flex items-center justify-end gap-2">
                  <span className="hidden text-xs text-[var(--color-ink-muted)] lg:inline">
                    {relative(snapshot.started_at)}
                  </span>
                  <Button onClick={() => navigate(`/backups?id=${snapshot.id}`)}>Open</Button>
                  <Button href={archiveUrl(snapshot.id, '')} className="hidden sm:inline-flex">
                    ZIP
                  </Button>
                  <Button
                    variant="danger"
                    disabled={busy === snapshot.id}
                    onClick={() => {
                      if (!confirm('Delete this backup? Files it uniquely holds will be gone for good.')) return
                      void run(snapshot.id, () => api.deleteSnapshot(snapshot.id), reload)
                    }}
                  >
                    Delete
                  </Button>
                </div>
              </div>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

type Page = { snapshot: Snapshot; entries: Entry[]; cursor: string }

function SnapshotBrowser({
  snapshotId,
  prefix,
  onNavigate,
  onClose,
}: {
  snapshotId: string
  prefix: string
  onNavigate: (path: string) => void
  onClose: () => void
}) {
  const { data, error, loading } = useLoader<Page>(
    async () => {
      const [snapshot, page] = await Promise.all([api.snapshot(snapshotId), api.browse(snapshotId, prefix)])
      return { snapshot, entries: page.entries ?? [], cursor: page.next_cursor ?? '' }
    },
    { deps: [snapshotId, prefix] },
  )

  const [extra, setExtra] = useState<{ key: string; entries: Entry[]; cursor: string }>()
  const more = useAction()

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <BrowseSkeleton />

  const key = `${snapshotId}:${prefix}`
  const appended = extra?.key === key ? extra.entries : []
  const cursor = extra?.key === key ? extra.cursor : data.cursor
  const entries = [...data.entries, ...appended]

  const folders = new Set<string>()
  const files: Entry[] = []
  for (const entry of entries) {
    const tail = prefix ? entry.path.slice(prefix.length + 1) : entry.path
    if (!tail) continue
    const slash = tail.indexOf('/')
    if (slash >= 0) {
      folders.add(tail.slice(0, slash))
    } else if (entry.type === 'dir') {
      folders.add(tail)
    } else {
      files.push(entry)
    }
  }

  const folderList = [...folders].sort()
  const fileList = files.slice().sort((a, b) => a.path.localeCompare(b.path))

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-lg font-semibold tracking-tight">
            {data.snapshot.device_name ?? 'Backup'}
          </div>
          <div className="mt-0.5 text-sm text-[var(--color-ink-muted)]">
            {absolute(data.snapshot.started_at)} · {count(data.snapshot.file_count)} files ·{' '}
            {bytes(data.snapshot.total_bytes)}
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button href={archiveUrl(snapshotId, prefix)}>
            Download {prefix ? `“${baseName(prefix)}”` : 'folder'} ZIP
          </Button>
          <Button variant="ghost" onClick={onClose}>
            All backups
          </Button>
        </div>
      </div>

      <nav className="flex flex-wrap items-center gap-1 text-sm">
        <button
          type="button"
          className="rounded-lg px-2 py-1 font-semibold text-[var(--color-brand)] hover:bg-[var(--color-brand-soft)]"
          onClick={() => onNavigate('')}
        >
          Root
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

      <div className="panel overflow-hidden">
        {folderList.length === 0 && fileList.length === 0 ? (
          <Empty title="This folder is empty in that backup" />
        ) : (
          <>
            <div className="file-row border-b border-[var(--color-border-subtle)] text-[0.7rem] font-semibold uppercase tracking-[0.06em] text-[var(--color-ink-muted)]">
              <span>Name</span>
              <span className="file-row-meta">Type</span>
              <span className="file-row-meta">Size</span>
              <span className="text-right">Modified</span>
            </div>
            <ul>
              {prefix && (
                <li>
                  <button type="button" className="file-row w-full text-left" onClick={() => onNavigate(parentPath(prefix))}>
                    <span className="flex items-center gap-3 text-sm font-semibold text-[var(--color-ink-muted)]">
                      <span className="grid size-7 place-items-center rounded-lg bg-[var(--color-surface-muted)]">↑</span>
                      Parent folder
                    </span>
                  </button>
                </li>
              )}
              {folderList.map((folder) => (
                <li key={folder}>
                  <button
                    type="button"
                    className="file-row w-full text-left"
                    onClick={() => onNavigate(prefix ? `${prefix}/${folder}` : folder)}
                  >
                    <span className="flex min-w-0 items-center gap-3">
                      <FolderGlyph />
                      <span className="truncate text-sm font-semibold">{folder}</span>
                    </span>
                    <span className="file-row-meta text-sm text-[var(--color-ink-muted)]">Folder</span>
                    <span className="file-row-meta text-sm text-[var(--color-ink-muted)]">—</span>
                    <span className="text-right text-sm text-[var(--color-ink-muted)]">—</span>
                  </button>
                </li>
              ))}
              {fileList.map((file) => (
                <li key={file.path}>
                  <div className="file-row">
                    <span className="flex min-w-0 items-center gap-3">
                      <FileGlyph />
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-semibold">{baseName(file.path)}</span>
                        {file.type === 'symlink' && (
                          <span className="mt-0.5 inline-block">
                            <Badge>link</Badge>
                          </span>
                        )}
                      </span>
                    </span>
                    <span className="file-row-meta text-sm text-[var(--color-ink-muted)]">File</span>
                    <span className="file-row-meta tabular text-sm text-[var(--color-ink-muted)]">
                      {bytes(file.size)}
                    </span>
                    <div className="flex items-center justify-end gap-2">
                      <span className="hidden text-xs text-[var(--color-ink-muted)] md:inline">
                        {relative(file.mtime)}
                      </span>
                      <Button href={downloadUrl(snapshotId, file.path)}>Download</Button>
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
    </div>
  )
}
