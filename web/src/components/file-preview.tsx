import { useEffect, useState } from 'react'
import { Lock, X } from 'lucide-react'
import { fetchSnapshotFile } from '@/lib/api'
import { baseName, bytes } from '@/lib/format'
import { canPreview, fileKind, kindLabel } from '@/lib/file-kind'
import { message } from '@/lib/use-loader'
import { Button, ErrorNote } from '@/components/ui'
import { EntryIcon } from '@/components/file-icon'

/** FilePreview is a Drive-style overlay: open unencrypted files, explain locked ones. */
export function FilePreview({
  snapshotId,
  path,
  size,
  locked,
  onClose,
  onDownload,
  onVersions,
}: {
  snapshotId: string
  path: string
  size?: number
  locked: boolean
  onClose: () => void
  onDownload: () => void
  onVersions: () => void
}) {
  const kind = fileKind(path, 'file')
  const previewable = !locked && canPreview(kind, size)
  const [blobUrl, setBlobUrl] = useState<string>()
  const [text, setText] = useState<string>()
  const [error, setError] = useState<string>()
  const [loading, setLoading] = useState(previewable)

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  useEffect(() => {
    if (!previewable) return
    let revoked = ''
    let cancelled = false
    fetchSnapshotFile(snapshotId, path)
      .then(async (blob) => {
        if (cancelled) return
        if (kind === 'text' || kind === 'code') {
          setText(await blob.text())
          return
        }
        revoked = URL.createObjectURL(blob)
        setBlobUrl(revoked)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(message(err, 'Could not open this file'))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
      if (revoked) URL.revokeObjectURL(revoked)
    }
  }, [snapshotId, path, previewable, kind])

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-gray-950/70 p-3 backdrop-blur-sm sm:p-8"
      role="dialog"
      aria-modal="true"
      aria-label={baseName(path)}
      onClick={onClose}
    >
      <div
        className="flex max-h-[90dvh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl bg-white shadow-xl dark:bg-gray-900"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="flex items-start gap-3 border-b border-gray-200 px-4 py-3 dark:border-gray-800">
          <EntryIcon path={path} type="file" locked={locked} />
          <div className="min-w-0 flex-1">
            <h2 className="truncate text-sm font-semibold">{baseName(path)}</h2>
            <p className="mt-0.5 truncate text-xs text-gray-500">
              {kindLabel(kind)}
              {size != null ? ` · ${bytes(size)}` : ''}
              {locked ? ' · encrypted on this server' : ''}
            </p>
          </div>
          <button
            type="button"
            className="grid size-9 place-items-center rounded-lg text-gray-500 hover:bg-gray-100 dark:hover:bg-white/5"
            aria-label="Close preview"
            onClick={onClose}
          >
            <X size={18} />
          </button>
        </header>

        <div className="min-h-48 flex-1 overflow-auto bg-gray-50 p-4 dark:bg-gray-950">
          {locked ? (
            <LockedState />
          ) : loading ? (
            <p className="py-16 text-center text-sm text-gray-500">Opening file…</p>
          ) : error ? (
            <ErrorNote>{error}</ErrorNote>
          ) : blobUrl && kind === 'image' ? (
            <img src={blobUrl} alt={baseName(path)} className="mx-auto max-h-[60vh] max-w-full rounded-lg object-contain" />
          ) : blobUrl && kind === 'pdf' ? (
            <iframe title={baseName(path)} src={blobUrl} className="h-[60vh] w-full rounded-lg bg-white" />
          ) : blobUrl && kind === 'video' ? (
            <video src={blobUrl} controls className="mx-auto max-h-[60vh] w-full rounded-lg" />
          ) : blobUrl && kind === 'audio' ? (
            <audio src={blobUrl} controls className="w-full" />
          ) : text != null ? (
            <pre className="max-h-[60vh] overflow-auto rounded-xl bg-white p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap dark:bg-gray-900">
              {text}
            </pre>
          ) : (
            <p className="py-12 text-center text-sm text-gray-500">
              This file type cannot be previewed in the browser. Download it to open on your computer.
            </p>
          )}
        </div>

        <footer className="flex flex-wrap items-center justify-end gap-2 border-t border-gray-200 px-4 py-3 dark:border-gray-800">
          <Button variant="ghost" onClick={onVersions}>
            Versions
          </Button>
          <Button variant="primary" disabled={locked} onClick={onDownload}>
            {locked ? 'Locked' : 'Download'}
          </Button>
        </footer>
      </div>
    </div>
  )
}

function LockedState() {
  return (
    <div className="mx-auto flex max-w-md flex-col items-center py-10 text-center">
      <span className="grid size-14 place-items-center rounded-2xl bg-gray-900 text-white dark:bg-white/10">
        <Lock size={22} />
      </span>
      <p className="mt-4 text-sm font-semibold">This file is locked</p>
      <p className="mt-1 text-sm text-gray-500">
        It was backed up with end-to-end encryption. This website cannot read it. Restore it from the
        desktop app or CLI on a computer that has the recovery code.
      </p>
    </div>
  )
}
