'use client'

/**
 * Overview answers the only question most visitors have: is my data safe right
 * now? The health of the least healthy device therefore comes first, ahead of any
 * storage statistics.
 */

import Link from 'next/link'
import { api, type Device, type Snapshot, type Usage } from '@/lib/api'
import { bytes, count, platformLabel, relative } from '@/lib/format'
import { useLoader } from '@/lib/use-loader'
import { Badge, Card, Empty, ErrorNote, Meter, Spinner, Stat } from '@/components/ui'

type Overview = { devices: Device[]; usage: Usage; snapshots: Snapshot[] }

export default function OverviewPage() {
  const { data, error, loading } = useLoader<Overview>(
    async () => {
      const [devices, usage, snapshots] = await Promise.all([api.devices(), api.usage(), api.snapshots()])
      return { devices, usage, snapshots }
    },
    // Refresh while the page is open so a running backup is visible.
    { pollMs: 15000 },
  )

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <Spinner />

  const { devices, usage, snapshots } = data
  const problems = devices.filter((d) => d.health === 'error' || d.health === 'stale')
  const newest = snapshots.reduce<Snapshot | undefined>(
    (best, s) => (s.status === 'complete' && (!best || s.started_at > best.started_at) ? s : best),
    undefined,
  )

  return (
    <div className="space-y-6">
      <HealthBanner devices={devices} newest={newest} />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Devices" value={count(devices.length)} hint={`${problems.length} needing attention`} />
        <Stat
          label="Files protected"
          value={count(newest?.file_count)}
          hint={newest ? `as of ${relative(newest.started_at)}` : 'no backups yet'}
        />
        <Stat
          label="Stored on server"
          value={bytes(usage.stored_bytes)}
          hint={`${bytes(usage.logical_bytes)} of files, ${usage.dedup_ratio.toFixed(1)}× saved`}
          tone="good"
        />
        <Stat
          label="Backups kept"
          value={count(usage.snapshot_count)}
          hint={`${count(usage.chunk_count)} unique blocks`}
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-3">
        <Card title="Devices" className="lg:col-span-2">
          {devices.length === 0 ? (
            <Empty title="No devices yet" hint="Add one from the Devices page to get a connection code." />
          ) : (
            <ul className="divide-y divide-[var(--color-border-subtle)]">
              {devices.map((device) => (
                <li key={device.id} className="flex items-center justify-between gap-4 py-3 first:pt-0 last:pb-0">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-sm font-medium">{device.name}</span>
                      <HealthBadge health={device.health} state={device.state} />
                    </div>
                    <div className="mt-0.5 truncate text-xs text-[var(--color-ink-muted)]">
                      {platformLabel(device.platform)} · last seen {relative(device.last_seen)}
                      {device.state_reason ? ` · ${device.state_reason}` : ''}
                    </div>
                  </div>
                  <div className="text-right text-xs text-[var(--color-ink-muted)]">
                    <div className="tabular text-sm text-[var(--color-ink)]">{bytes(device.logical_bytes)}</div>
                    <div>backed up {relative(device.last_backup_at)}</div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Card>

        <div className="space-y-6">
          <Card title="Storage">
            <div className="space-y-4">
              <div>
                <div className="flex items-baseline justify-between text-sm">
                  <span className="text-[var(--color-ink-muted)]">Used</span>
                  <span className="tabular font-medium">
                    {bytes(usage.stored_bytes)}
                    {usage.quota_bytes > 0 ? ` of ${bytes(usage.quota_bytes)}` : ''}
                  </span>
                </div>
                {usage.quota_bytes > 0 ? (
                  <div className="mt-2">
                    <Meter value={usage.stored_bytes} max={usage.quota_bytes} />
                  </div>
                ) : (
                  <p className="mt-1 text-xs text-[var(--color-ink-muted)]">No quota set.</p>
                )}
              </div>
              {usage.free_disk_bytes >= 0 && (
                <div className="text-sm">
                  <span className="text-[var(--color-ink-muted)]">Free space on server: </span>
                  <span className="tabular font-medium">{bytes(usage.free_disk_bytes)}</span>
                </div>
              )}
              <div className="rounded-lg bg-[var(--color-surface-muted)] px-3 py-2.5 text-xs text-[var(--color-ink-muted)]">
                Deduplication and compression have saved{' '}
                <strong className="text-[var(--color-ink)]">
                  {bytes(Math.max(usage.logical_bytes - usage.stored_bytes, 0))}
                </strong>{' '}
                so far.
              </div>
            </div>
          </Card>

          <Card
            title="Latest backups"
            action={
              <Link className="text-xs text-[var(--color-brand)]" href="/backups">
                All
              </Link>
            }
          >
            {snapshots.length === 0 ? (
              <Empty title="Nothing backed up yet" />
            ) : (
              <ul className="space-y-2.5">
                {snapshots.slice(0, 5).map((snapshot) => (
                  <li key={snapshot.id} className="text-sm">
                    <Link className="hover:underline" href={`/backups?id=${snapshot.id}`}>
                      <div className="flex items-center justify-between gap-2">
                        <span className="truncate">{snapshot.device_name ?? 'device'}</span>
                        <span className="text-xs text-[var(--color-ink-muted)]">{relative(snapshot.started_at)}</span>
                      </div>
                      <div className="text-xs text-[var(--color-ink-muted)]">
                        {count(snapshot.file_count)} files · {bytes(snapshot.total_bytes)}
                      </div>
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      </div>
    </div>
  )
}

/**
 * HealthBanner states the overall situation in one sentence. Nobody should have
 * to interpret a table to find out whether their backups are working.
 */
function HealthBanner({ devices, newest }: { devices: Device[]; newest?: Snapshot }) {
  if (devices.length === 0) {
    return (
      <Banner tone="brand" title="No devices are connected yet.">
        Go to{' '}
        <Link className="underline" href="/devices">
          Devices
        </Link>{' '}
        to create a connection code, then run{' '}
        <code className="rounded bg-[var(--color-surface-muted)] px-1 py-0.5 text-xs">openbackup connect</code> on the
        computer you want to back up.
      </Banner>
    )
  }

  const failing = devices.filter((d) => d.health === 'error')
  const stale = devices.filter((d) => d.health === 'stale')
  const never = devices.filter((d) => d.health === 'never_run')

  if (failing.length > 0) {
    return (
      <Banner tone="bad" title={`${failing.length} device${failing.length === 1 ? '' : 's'} reported an error.`}>
        {failing[0].last_error || 'Check the Activity page for details.'}
      </Banner>
    )
  }
  if (stale.length > 0) {
    return (
      <Banner tone="warn" title={`${stale.length} device${stale.length === 1 ? '' : 's'} has not backed up recently.`}>
        {stale.map((d) => d.name).join(', ')} — the machine may be switched off, or the agent may have stopped.
      </Banner>
    )
  }
  if (never.length > 0) {
    return (
      <Banner tone="warn" title="A device is connected but has not backed up yet.">
        The first backup can take a while. It will appear here as soon as it finishes.
      </Banner>
    )
  }
  return (
    <Banner tone="good" title="Everything is backed up.">
      The most recent backup finished {relative(newest?.started_at)} and covers {count(newest?.file_count)} files.
    </Banner>
  )
}

function Banner({
  tone,
  title,
  children,
}: {
  tone: 'good' | 'warn' | 'bad' | 'brand'
  title: string
  children: React.ReactNode
}) {
  const color = `var(--color-${tone})`
  return (
    <div
      className="rounded-xl border px-5 py-4"
      style={{
        borderColor: `color-mix(in oklch, ${color} 35%, transparent)`,
        background: `color-mix(in oklch, ${color} 8%, var(--color-surface))`,
      }}
    >
      <div className="flex items-start gap-3">
        <span className="mt-1.5 size-2 shrink-0 rounded-full" style={{ background: color }} />
        <div>
          <p className="text-sm font-semibold">{title}</p>
          <p className="mt-0.5 text-sm text-[var(--color-ink-muted)]">{children}</p>
        </div>
      </div>
    </div>
  )
}

export function HealthBadge({ health, state }: { health: string; state?: string }) {
  switch (health) {
    case 'ok':
      return <Badge tone="good">{state === 'uploading' ? 'backing up' : 'healthy'}</Badge>
    case 'stale':
      return <Badge tone="warn">out of date</Badge>
    case 'error':
      return <Badge tone="bad">error</Badge>
    default:
      return <Badge>no backup yet</Badge>
  }
}
