import { useEffect, useState } from 'react'

import { api, message, on } from '../lib/bridge'
import { ago } from '../lib/format'
import type { Overview, RestoreProgress } from '../lib/types'
import { Button, Card, Notice } from '../components/ui'

export type ActivityEvent = {
  at: string
  level: string
  message: string
  path?: string
  reason?: string
}

/** Logs shows live backup/restore progress and recent file activity. */
export function Logs({ status }: { status: Overview }) {
  const [events, setEvents] = useState<ActivityEvent[]>([])
  const [error, setError] = useState<string>()
  const [restore, setRestore] = useState<RestoreProgress | null>(null)

  const load = async () => {
    try {
      const rows = await api.activity()
      setEvents(rows ?? [])
      setError(undefined)
    } catch (err) {
      setError(message(err))
    }
  }

  useEffect(() => {
    void load()
    const id = window.setInterval(() => void load(), 2500)
    return () => window.clearInterval(id)
  }, [])

  useEffect(() => {
    void api.restoreProgress().then(setRestore).catch(() => setRestore(null))
    return on<RestoreProgress>('restore', setRestore)
  }, [])

  const progress =
    status.files_total > 0 ? Math.min(100, (status.files_done / status.files_total) * 100) : null
  const working = status.state === 'uploading' || status.state === 'scanning'

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <div>
        <h1 className="text-xl font-semibold text-ink">Logs</h1>
        <p className="mt-1 text-sm text-ink-muted">
          See which file is backing up or restoring, and recent activity on this computer.
        </p>
      </div>

      <Card title="Happening now">
        {restore?.running ? (
          <div>
            <p className="text-sm font-medium text-ink">Restoring files</p>
            <p className="mt-1 truncate font-mono text-xs text-ink-muted" title={restore.current}>
              {restore.current || 'Preparing…'}
            </p>
            {restore.of_files > 0 && (
              <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-surface-muted">
                <div
                  className="h-full rounded-full bg-brand transition-all"
                  style={{
                    width: `${Math.min(100, (restore.files / restore.of_files) * 100)}%`,
                  }}
                />
              </div>
            )}
            <p className="mt-2 text-xs text-ink-muted">
              {restore.files} / {restore.of_files} files
            </p>
          </div>
        ) : working ? (
          <div>
            <p className="text-sm font-medium text-ink">
              {status.state === 'scanning' ? 'Scanning' : 'Backing up'}
            </p>
            <p className="mt-1 truncate font-mono text-xs text-ink-muted" title={status.current_path}>
              {status.current_path || status.detail || 'Working…'}
            </p>
            {progress !== null && status.state === 'uploading' && (
              <div className="mt-3 h-1.5 overflow-hidden rounded-full bg-surface-muted">
                <div className="h-full rounded-full bg-brand transition-all" style={{ width: `${progress}%` }} />
              </div>
            )}
          </div>
        ) : status.paused ? (
          <p className="text-sm text-ink-muted">Backups are paused{status.pause_reason ? `: ${status.pause_reason}` : '.'}</p>
        ) : (
          <p className="text-sm text-ink-muted">Idle — nothing is transferring right now.</p>
        )}
      </Card>

      {error && <Notice tone="bad">{error}</Notice>}

      <Card
        title="Recent activity"
        actions={
          <Button tone="quiet" onClick={() => void load()}>
            Refresh
          </Button>
        }
      >
        {events.length === 0 ? (
          <p className="text-sm text-ink-muted">
            Activity appears here while this computer backs up. Start a backup to see file paths.
          </p>
        ) : (
          <ul className="divide-y divide-border-subtle">
            {events.map((event, index) => (
              <li key={`${event.at}-${index}`} className="flex gap-3 py-3 first:pt-0 last:pb-0">
                <span
                  className={`mt-1.5 size-2 shrink-0 rounded-full ${
                    event.level === 'error'
                      ? 'bg-bad'
                      : event.level === 'warn'
                        ? 'bg-warn'
                        : 'bg-ink-muted'
                  }`}
                />
                <div className="min-w-0 flex-1">
                  <p className="text-sm text-ink">{event.message}</p>
                  {(event.path || event.reason) && (
                    <p className="mt-0.5 truncate font-mono text-xs text-ink-muted" title={event.path}>
                      {event.reason ? `${event.reason} — ` : ''}
                      {event.path}
                    </p>
                  )}
                </div>
                <time className="shrink-0 text-xs text-ink-muted">{ago(event.at)}</time>
              </li>
            ))}
          </ul>
        )}
      </Card>

      <div className="flex flex-wrap gap-2">
        <Button tone="quiet" onClick={() => void api.openLogFolder()}>
          Open log folder
        </Button>
      </div>
    </div>
  )
}
