/**
 * Logs: live progress (which file is uploading) plus the activity feed.
 */

import { useMemo, useState } from 'react'
import { api, type ActivityEvent, type Device } from '@/lib/api'
import { absolute, relative } from '@/lib/format'
import { useLoader } from '@/lib/use-loader'
import { Badge, Card, Empty, ErrorNote } from '@/components/ui'
import { ActivitySkeleton } from '@/components/skeleton'

type LogsData = { devices: Device[]; events: ActivityEvent[]; fetchedAt: number }

const LIVE_MAX_AGE_MS = 3 * 60 * 1000

function isLiveDevice(device: Device, now: number): boolean {
  if (!device.last_seen) return false
  const ageMs = now - new Date(device.last_seen).getTime()
  // Heartbeats are ~15–60s while working; ignore stale leftover state.
  if (Number.isNaN(ageMs) || ageMs > LIVE_MAX_AGE_MS) return false
  return Boolean(device.current_path) || device.state === 'uploading' || device.state === 'scanning'
}

export default function LogsPage() {
  const { data, error, loading } = useLoader<LogsData>(
    async () => {
      const [devices, events] = await Promise.all([api.devices(), api.events(250)])
      return { devices, events, fetchedAt: Date.now() }
    },
    { pollMs: 8000 },
  )
  const [filter, setFilter] = useState<'all' | 'problems' | 'files'>('all')

  const live = useMemo(
    () => (data ? data.devices.filter((d) => isLiveDevice(d, data.fetchedAt)) : []),
    [data],
  )

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <ActivitySkeleton />

  const shown = data.events.filter((e) => {
    if (filter === 'problems') return e.level === 'warn' || e.level === 'error'
    if (filter === 'files') return Boolean(e.path) || /back(ing|ed) up|restor|skipped|could not read/i.test(e.message)
    return true
  })

  return (
    <div className="space-y-4">
      {live.length > 0 && (
        <Card title="Happening now">
          <ul className="divide-y divide-gray-100 dark:divide-gray-800">
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
                    <p className="mt-1 truncate font-mono text-xs text-gray-500" title={device.current_path}>
                      {device.current_path}
                    </p>
                  ) : (
                    <p className="mt-1 text-xs text-gray-500">
                      {device.state_reason || 'Working…'}
                    </p>
                  )}
                  {progress !== null && (
                    <div className="mt-2 h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
                      <div
                        className="h-full rounded-full bg-brand-500 transition-all"
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
                    ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/15 dark:text-brand-300'
                    : 'text-gray-500'
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
            hint="Activity shows up while devices back up."
          />
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-800">
            {shown.map((event, index) => (
              <li key={event.id ?? index} className="flex gap-3 py-3 first:pt-0 last:pb-0">
                <LevelDot level={event.level} />
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-2">
                    <span className="text-sm">{event.message}</span>
                    {event.device_name && <Badge>{event.device_name}</Badge>}
                  </div>
                  {(event.reason || event.path) && (
                    <div className="mt-0.5 truncate font-mono text-xs text-gray-500" title={event.path}>
                      {event.reason ? `Reason: ${event.reason}` : null}
                      {event.reason && event.path ? ' — ' : null}
                      {event.path ?? null}
                    </div>
                  )}
                </div>
                <time className="shrink-0 text-xs text-gray-500" title={absolute(event.at)}>
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
    level === 'error' ? 'bg-error-500' : level === 'warn' ? 'bg-warning-500' : 'bg-gray-400'
  return <span className={`mt-1.5 size-2 shrink-0 rounded-full ${color}`} aria-label={level} />
}
