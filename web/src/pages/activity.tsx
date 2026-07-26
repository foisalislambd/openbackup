/**
 * Logs: live progress (which file is uploading) plus the activity feed.
 */

import { useState } from 'react'
import { api, type ActivityEvent, type Device } from '@/lib/api'
import { absolute, relative } from '@/lib/format'
import { useLoader } from '@/lib/use-loader'
import { Badge, Card, Empty, ErrorNote } from '@/components/ui'
import { ActivitySkeleton } from '@/components/skeleton'

type LogsData = { devices: Device[]; events: ActivityEvent[] }

export default function ActivityPage() {
  const { data, error, loading } = useLoader<LogsData>(
    async () => {
      const [devices, events] = await Promise.all([api.devices(), api.events(250)])
      return { devices, events }
    },
    { pollMs: 8000 },
  )
  const [filter, setFilter] = useState<'all' | 'problems' | 'files'>('all')

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <ActivitySkeleton />

  const live = data.devices.filter((d) => {
    if (!d.last_seen) return false
    const ageMs = Date.now() - new Date(d.last_seen).getTime()
    // Heartbeats are ~15–60s while working; ignore stale leftover state.
    if (Number.isNaN(ageMs) || ageMs > 3 * 60 * 1000) return false
    return Boolean(d.current_path) || d.state === 'uploading' || d.state === 'scanning'
  })

  const shown = data.events.filter((e) => {
    if (filter === 'problems') return e.level === 'warn' || e.level === 'error'
    if (filter === 'files') return Boolean(e.path) || /back(ing|ed) up|restor|skipped|could not read/i.test(e.message)
    return true
  })

  return (
    <div className="space-y-4">
      {live.length > 0 && (
        <Card title="Happening now">
          <ul className="divide-y divide-[var(--color-border-subtle)]">
            {live.map((device) => {
              const progress =
                device.files_total && device.files_total > 0
                  ? Math.min(100, ((device.files_done || 0) / device.files_total) * 100)
                  : null
              return (
                <li key={device.id} className="py-3 first:pt-0 last:pb-0">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="text-sm font-semibold">{device.name}</span>
                    <Badge>{device.state}</Badge>
                  </div>
                  {device.current_path ? (
                    <p className="mt-1 truncate font-mono text-xs text-[var(--color-ink-muted)]" title={device.current_path}>
                      {device.current_path}
                    </p>
                  ) : (
                    <p className="mt-1 text-xs text-[var(--color-ink-muted)]">
                      {device.state_reason || 'Working…'}
                    </p>
                  )}
                  {progress !== null && (
                    <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-[var(--color-surface-muted)]">
                      <div
                        className="h-full rounded-full bg-[var(--color-brand)] transition-all"
                        style={{ width: `${progress}%` }}
                      />
                    </div>
                  )}
                </li>
              )
            })}
          </ul>
        </Card>
      )}

      <Card
        title="Log"
        action={
          <div className="flex items-center gap-1 text-xs">
            {(['all', 'files', 'problems'] as const).map((option) => (
              <button
                key={option}
                onClick={() => setFilter(option)}
                className={`rounded-md px-2 py-1 font-medium ${
                  filter === option
                    ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
                    : 'text-[var(--color-ink-muted)]'
                }`}
              >
                {option === 'all' ? 'Everything' : option === 'files' ? 'Files' : 'Problems'}
              </button>
            ))}
          </div>
        }
      >
        {shown.length === 0 ? (
          <Empty
            title={filter === 'problems' ? 'No problems reported' : 'Nothing logged yet'}
            hint="File activity appears here while devices back up."
          />
        ) : (
          <ul className="divide-y divide-[var(--color-border-subtle)]">
            {shown.map((event, index) => (
              <li key={event.id ?? index} className="flex gap-3 py-3 first:pt-0 last:pb-0">
                <LevelDot level={event.level} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="text-sm">{event.message}</span>
                    {event.device_name && <Badge>{event.device_name}</Badge>}
                  </div>
                  {(event.reason || event.path) && (
                    <div className="mt-0.5 truncate font-mono text-xs text-[var(--color-ink-muted)]" title={event.path}>
                      {event.reason ? `Reason: ${event.reason}` : null}
                      {event.reason && event.path ? ' — ' : null}
                      {event.path ?? null}
                    </div>
                  )}
                </div>
                <time className="shrink-0 text-xs text-[var(--color-ink-muted)]" title={absolute(event.at)}>
                  {relative(event.at)}
                </time>
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}

function LevelDot({ level }: { level: string }) {
  const color =
    level === 'error' ? 'var(--color-bad)' : level === 'warn' ? 'var(--color-warn)' : 'var(--color-ink-muted)'
  return <span className="mt-1.5 size-2 shrink-0 rounded-full" style={{ background: color }} aria-label={level} />
}
