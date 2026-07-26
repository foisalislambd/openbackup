/**
 * Home: status first, then the user's backed-up content as openable tiles —
 * closer to a file library than a metrics dashboard.
 */

import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, type Device, type Snapshot, type Usage } from '@/lib/api'
import { bytes, count, platformLabel, relative } from '@/lib/format'
import { useLoader } from '@/lib/use-loader'
import { HealthBadge } from '@/components/health-badge'
import { Empty, ErrorNote, FolderGlyph, Meter, Stat } from '@/components/ui'
import { OverviewSkeleton } from '@/components/skeleton'

type Overview = { devices: Device[]; usage: Usage; snapshots: Snapshot[] }

export default function OverviewPage() {
  const navigate = useNavigate()
  const { data, error, loading } = useLoader<Overview>(
    async () => {
      const [devices, usage, snapshots] = await Promise.all([api.devices(), api.usage(), api.snapshots()])
      return { devices, usage, snapshots }
    },
    { pollMs: 15000 },
  )

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <OverviewSkeleton />

  const { devices, usage, snapshots } = data
  const problems = devices.filter((d) => d.health === 'error' || d.health === 'stale')
  const complete = snapshots.filter((s) => s.status === 'complete')
  const newest = complete[0]
  // One tile per device — each backup is a version of the same files, not a new folder.
  const latestByDevice: Snapshot[] = []
  const seen = new Set<string>()
  for (const snap of complete) {
    if (seen.has(snap.device_id)) continue
    seen.add(snap.device_id)
    latestByDevice.push(snap)
  }

  return (
    <div className="space-y-8">
      <HealthBanner devices={devices} newest={newest} />

      <section>
        <div className="mb-3 flex items-end justify-between gap-3">
          <div>
            <h2 className="text-lg font-semibold tracking-tight">Your computers</h2>
            <p className="text-sm text-[var(--color-ink-muted)]">
              Open current files — older versions appear when you click a file
            </p>
          </div>
          <Link to="/backups" className="text-sm font-semibold text-[var(--color-brand)] hover:underline">
            Browse files
          </Link>
        </div>

        {latestByDevice.length === 0 ? (
          <div className="panel">
            <Empty
              title="No files backed up yet"
              hint="Connect a device, run a backup, and your folders will appear here."
            />
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {latestByDevice.map((snapshot) => (
              <button
                key={snapshot.device_id}
                type="button"
                className="content-tile"
                onClick={() =>
                  navigate(`/backups?device=${encodeURIComponent(snapshot.device_id)}`)
                }
              >
                <FolderGlyph large />
                <div className="min-w-0">
                  <div className="truncate text-sm font-semibold">
                    {snapshot.device_name ?? 'Computer'}
                  </div>
                  <div className="mt-0.5 truncate text-xs text-[var(--color-ink-muted)]">
                    {count(snapshot.file_count)} files · {bytes(snapshot.total_bytes)}
                  </div>
                  <div className="mt-2 text-[0.7rem] font-medium text-[var(--color-ink-muted)]">
                    Updated {relative(snapshot.started_at)}
                  </div>
                </div>
              </button>
            ))}
          </div>
        )}
      </section>

      <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <Stat label="Devices" value={count(devices.length)} hint={`${problems.length} needing attention`} />
        <Stat
          label="Files protected"
          value={count(newest?.file_count)}
          hint={newest ? `as of ${relative(newest.started_at)}` : 'no backups yet'}
        />
        <Stat
          label="Stored on server"
          value={bytes(usage.stored_bytes)}
          hint={`${bytes(usage.logical_bytes)} logical · ${usage.dedup_ratio.toFixed(1)}× saved`}
          tone="good"
        />
        <Stat label="Backups kept" value={count(usage.snapshot_count)} hint={`${count(usage.chunk_count)} unique blocks`} />
      </section>

      <section className="grid gap-4 lg:grid-cols-[minmax(0,1.4fr)_minmax(18rem,0.8fr)]">
        <div className="panel overflow-hidden">
          <header className="flex items-center justify-between border-b border-[var(--color-border-subtle)] px-5 py-3.5">
            <h2 className="text-sm font-semibold">Devices</h2>
            <Link to="/devices" className="text-xs font-semibold text-[var(--color-brand)] hover:underline">
              Manage
            </Link>
          </header>
          {devices.length === 0 ? (
            <Empty title="No devices yet" hint="Create a connection code on the Devices page." />
          ) : (
            <ul>
              {devices.map((device) => (
                <li
                  key={device.id}
                  className="flex items-center justify-between gap-4 border-b border-[var(--color-border-subtle)] px-5 py-3.5 last:border-0"
                >
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate text-sm font-semibold">{device.name}</span>
                      <HealthBadge health={device.health} state={device.state} />
                    </div>
                    <div className="mt-0.5 truncate text-xs text-[var(--color-ink-muted)]">
                      {platformLabel(device.platform)} · last seen {relative(device.last_seen)}
                    </div>
                  </div>
                  <div className="text-right text-xs text-[var(--color-ink-muted)]">
                    <div className="tabular text-sm font-semibold text-[var(--color-ink)]">
                      {bytes(device.logical_bytes)}
                    </div>
                    <div>{relative(device.last_backup_at)}</div>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="panel p-5">
          <h2 className="text-sm font-semibold">Storage</h2>
          <div className="mt-4 space-y-4">
            <div className="flex items-baseline justify-between text-sm">
              <span className="text-[var(--color-ink-muted)]">Used</span>
              <span className="tabular font-semibold">
                {bytes(usage.stored_bytes)}
                {usage.quota_bytes > 0 ? ` of ${bytes(usage.quota_bytes)}` : ''}
              </span>
            </div>
            {usage.quota_bytes > 0 ? (
              <Meter value={usage.stored_bytes} max={usage.quota_bytes} />
            ) : (
              <p className="text-xs text-[var(--color-ink-muted)]">No quota set — using available disk.</p>
            )}
            {usage.free_disk_bytes >= 0 && (
              <p className="text-sm">
                <span className="text-[var(--color-ink-muted)]">Free on server · </span>
                <span className="tabular font-semibold">{bytes(usage.free_disk_bytes)}</span>
              </p>
            )}
            <p className="rounded-xl bg-[var(--color-surface-muted)] px-3 py-2.5 text-xs leading-relaxed text-[var(--color-ink-muted)]">
              Dedup + compression saved{' '}
              <strong className="text-[var(--color-ink)]">
                {bytes(Math.max(usage.logical_bytes - usage.stored_bytes, 0))}
              </strong>{' '}
              so far.
            </p>
          </div>
        </div>
      </section>
    </div>
  )
}

function HealthBanner({ devices, newest }: { devices: Device[]; newest?: Snapshot }) {
  if (devices.length === 0) {
    return (
      <Banner tone="brand" title="Ready when you are">
        Go to{' '}
        <Link className="font-semibold underline" to="/devices">
          Devices
        </Link>{' '}
        for a connection code, then run{' '}
        <code className="rounded-md bg-[var(--color-surface)] px-1.5 py-0.5 font-mono text-xs">openbackup connect</code>.
      </Banner>
    )
  }

  const failing = devices.filter((d) => d.health === 'error')
  const stale = devices.filter((d) => d.health === 'stale')
  const never = devices.filter((d) => d.health === 'never_run')

  if (failing.length > 0) {
    return (
      <Banner tone="bad" title={`${failing.length} device${failing.length === 1 ? '' : 's'} reported an error`}>
        {failing[0].last_error || 'Check Activity for details.'}
      </Banner>
    )
  }
  if (stale.length > 0) {
    return (
      <Banner tone="warn" title={`${stale.length} device${stale.length === 1 ? '' : 's'} out of date`}>
        {stale.map((d) => d.name).join(', ')} may be offline or the agent stopped.
      </Banner>
    )
  }
  if (never.length > 0) {
    return (
      <Banner tone="warn" title="First backup still running">
        It will show under Your computers when finished.
      </Banner>
    )
  }
  return (
    <Banner tone="good" title="Everything is backed up">
      Latest finished {relative(newest?.started_at)} · {count(newest?.file_count)} files protected.
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
  children: ReactNode
}) {
  const color = `var(--color-${tone})`
  return (
    <div
      className="rounded-2xl border px-5 py-4"
      style={{
        borderColor: `color-mix(in oklch, ${color} 28%, transparent)`,
        background: `linear-gradient(135deg, color-mix(in oklch, ${color} 10%, var(--color-surface)), var(--color-surface))`,
      }}
    >
      <div className="flex items-start gap-3">
        <span className="mt-1.5 size-2.5 shrink-0 rounded-full" style={{ background: color }} />
        <div>
          <p className="text-sm font-semibold tracking-tight">{title}</p>
          <p className="mt-0.5 text-sm text-[var(--color-ink-muted)]">{children}</p>
        </div>
      </div>
    </div>
  )
}
