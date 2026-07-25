'use client'

/**
 * Backups lists snapshots and lets a user walk into one and pull files back out.
 *
 * The browser is a plain folder view rather than a tree: people looking for a
 * lost file navigate the way they do in their file manager, and a flat listing
 * paginated by path stays fast with millions of entries.
 */

import { Suspense, useState } from 'react'
import { useRouter, useSearchParams } from 'next/navigation'
import { api, archiveUrl, downloadUrl, type Entry, type Snapshot } from '@/lib/api'
import { absolute, baseName, bytes, count, parentPath, relative } from '@/lib/format'
import { useAction, useLoader } from '@/lib/use-loader'
import { Badge, Button, Card, Empty, ErrorNote, Spinner } from '@/components/ui'

export default function BackupsPage() {
  // useSearchParams needs a Suspense boundary in a statically exported app.
  return (
    <Suspense fallback={<Spinner />}>
      <Backups />
    </Suspense>
  )
}

function Backups() {
  const params = useSearchParams()
  const router = useRouter()
  const selected = params.get('id') ?? ''
  const prefix = params.get('path') ?? ''

  const { data: snapshots, error, loading, reload } = useLoader<Snapshot[]>(() => api.snapshots())
  const { busy, error: actionError, run } = useAction()

  if (selected) {
    return (
      <SnapshotBrowser
        snapshotId={selected}
        prefix={prefix}
        onNavigate={(path) => router.push(`/backups?id=${selected}${path ? `&path=${encodeURIComponent(path)}` : ''}`)}
        onClose={() => router.push('/backups')}
      />
    )
  }

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !snapshots) return <Spinner />
  if (snapshots.length === 0) {
    return <Empty title="No backups yet" hint="They will appear here after the first backup finishes." />
  }

  return (
    <div className="space-y-4">
      {actionError && <ErrorNote>{actionError}</ErrorNote>}
      <Card title={`${snapshots.length} backups`}>
        <ul className="divide-y divide-[var(--color-border-subtle)]">
          {snapshots.map((snapshot) => (
            <li
              key={snapshot.id}
              className="flex flex-wrap items-center justify-between gap-3 py-3 first:pt-0 last:pb-0"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <button
                    className="text-sm font-medium hover:underline"
                    onClick={() => router.push(`/backups?id=${snapshot.id}`)}
                  >
                    {snapshot.device_name ?? 'device'} — {absolute(snapshot.started_at)}
                  </button>
                  {snapshot.status !== 'complete' && (
                    <Badge tone={snapshot.status === 'failed' ? 'bad' : 'warn'}>{snapshot.status}</Badge>
                  )}
                  {snapshot.kind === 'delta' && <Badge>incremental</Badge>}
                </div>
                <div className="mt-0.5 text-xs text-[var(--color-ink-muted)]">
                  {count(snapshot.file_count)} files · {bytes(snapshot.total_bytes)} · uploaded{' '}
                  {bytes(snapshot.uploaded_bytes)} · {relative(snapshot.started_at)}
                </div>
              </div>
              <div className="flex items-center gap-2">
                <Button onClick={() => router.push(`/backups?id=${snapshot.id}`)}>Browse</Button>
                <Button href={archiveUrl(snapshot.id, '')}>Download all</Button>
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
            </li>
          ))}
        </ul>
      </Card>
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

  // Extra pages are appended locally so paging does not reset the view.
  const [extra, setExtra] = useState<{ key: string; entries: Entry[]; cursor: string }>()
  const more = useAction()

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <Spinner label="Opening" />

  const key = `${snapshotId}:${prefix}`
  const appended = extra?.key === key ? extra.entries : []
  const cursor = extra?.key === key ? extra.cursor : data.cursor
  const entries = [...data.entries, ...appended]

  // The listing is flat, so the immediate children are derived from the paths.
  // That keeps the server query trivial and behaves identically for a delta
  // snapshot, whose entries are inherited from several points in the chain.
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

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <div className="text-sm font-semibold">
            {data.snapshot.device_name ?? 'Backup'} — {absolute(data.snapshot.started_at)}
          </div>
          <div className="mt-0.5 text-xs text-[var(--color-ink-muted)]">
            {count(data.snapshot.file_count)} files · {bytes(data.snapshot.total_bytes)}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button href={archiveUrl(snapshotId, prefix)}>
            Download {prefix ? `“${baseName(prefix)}”` : 'everything'} as ZIP
          </Button>
          <Button variant="ghost" onClick={onClose}>
            Back to list
          </Button>
        </div>
      </div>

      <nav className="flex flex-wrap items-center gap-1 text-sm">
        <button className="text-[var(--color-brand)] hover:underline" onClick={() => onNavigate('')}>
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
                  <span className="font-medium">{segment}</span>
                ) : (
                  <button className="text-[var(--color-brand)] hover:underline" onClick={() => onNavigate(path)}>
                    {segment}
                  </button>
                )}
              </span>
            )
          })}
      </nav>

      <Card>
        {folders.size === 0 && files.length === 0 ? (
          <Empty title="This folder is empty in that backup" />
        ) : (
          <ul className="divide-y divide-[var(--color-border-subtle)] text-sm">
            {prefix && (
              <li className="py-2 first:pt-0">
                <button
                  className="flex items-center gap-2 text-[var(--color-ink-muted)] hover:text-[var(--color-ink)]"
                  onClick={() => onNavigate(parentPath(prefix))}
                >
                  ‹ up one level
                </button>
              </li>
            )}
            {[...folders].sort().map((folder) => (
              <li key={folder} className="py-2">
                <button
                  className="flex items-center gap-2 font-medium hover:underline"
                  onClick={() => onNavigate(prefix ? `${prefix}/${folder}` : folder)}
                >
                  <FolderIcon /> {folder}
                </button>
              </li>
            ))}
            {files
              .slice()
              .sort((a, b) => a.path.localeCompare(b.path))
              .map((file) => (
                <li key={file.path} className="flex items-center justify-between gap-3 py-2">
                  <div className="flex min-w-0 items-center gap-2">
                    <FileIcon />
                    <span className="truncate">{baseName(file.path)}</span>
                    {file.type === 'symlink' && <Badge>link</Badge>}
                  </div>
                  <div className="flex items-center gap-3">
                    <span className="tabular text-xs text-[var(--color-ink-muted)]">{bytes(file.size)}</span>
                    <span className="hidden text-xs text-[var(--color-ink-muted)] sm:inline">
                      {relative(file.mtime)}
                    </span>
                    <Button href={downloadUrl(snapshotId, file.path)}>Download</Button>
                  </div>
                </li>
              ))}
          </ul>
        )}
        {cursor && (
          <div className="mt-4 text-center">
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
      </Card>
    </div>
  )
}

function FolderIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden>
      <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" strokeLinejoin="round" />
    </svg>
  )
}

function FileIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      className="text-[var(--color-ink-muted)]"
      aria-hidden
    >
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" strokeLinejoin="round" />
      <path d="M14 3v5h5" strokeLinejoin="round" />
    </svg>
  )
}
