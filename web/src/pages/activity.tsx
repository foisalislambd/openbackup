/**
 * Activity is the answer to "why is that folder not in my backup?".
 *
 * Agents report skips with a plain-language reason, so the feed doubles as an
 * explanation of the ignore rules in practice rather than in theory.
 */

import { useState } from 'react'
import { api, type ActivityEvent } from '@/lib/api'
import { absolute, relative } from '@/lib/format'
import { useLoader } from '@/lib/use-loader'
import { Badge, Card, Empty, ErrorNote } from '@/components/ui'
import { ActivitySkeleton } from '@/components/skeleton'

export default function ActivityPage() {
  const { data: events, error, loading } = useLoader<ActivityEvent[]>(() => api.events(200), { pollMs: 20000 })
  const [filter, setFilter] = useState<'all' | 'problems'>('all')

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !events) return <ActivitySkeleton />

  const shown = filter === 'all' ? events : events.filter((e) => e.level === 'warn' || e.level === 'error')

  return (
    <Card
      title="Activity"
      action={
        <div className="flex items-center gap-1 text-xs">
          {(['all', 'problems'] as const).map((option) => (
            <button
              key={option}
              onClick={() => setFilter(option)}
              className={`rounded-md px-2 py-1 font-medium ${
                filter === option
                  ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
                  : 'text-[var(--color-ink-muted)]'
              }`}
            >
              {option === 'all' ? 'Everything' : 'Problems only'}
            </button>
          ))}
        </div>
      }
    >
      {shown.length === 0 ? (
        <Empty
          title={filter === 'all' ? 'Nothing has happened yet' : 'No problems reported'}
          hint={filter === 'all' ? 'Events appear here as devices back up.' : undefined}
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
                  <div className="mt-0.5 text-xs text-[var(--color-ink-muted)]">
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
  )
}

function LevelDot({ level }: { level: string }) {
  const color =
    level === 'error' ? 'var(--color-bad)' : level === 'warn' ? 'var(--color-warn)' : 'var(--color-ink-muted)'
  return <span className="mt-1.5 size-2 shrink-0 rounded-full" style={{ background: color }} aria-label={level} />
}
