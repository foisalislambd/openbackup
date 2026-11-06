/**
 * Home: status first, then each computer as an openable tile.
 */

import { Link, useNavigate } from 'react-router-dom'
import { api, type ActivityEvent, type BackupAlert, type Device, type Snapshot, type Usage } from '@/lib/api'
import { bytes, count, platformLabel, relative } from '@/lib/format'
import { useLoader } from '@/lib/use-loader'
import { HealthBadge } from '@/components/health-badge'
import { Button, Empty, ErrorNote, Meter, Stat } from '@/components/ui'
import { OverviewSkeleton } from '@/components/skeleton'
import { EntryIcon, LockBadge } from '@/components/file-icon'
import { isEncryptedDevice } from '@/lib/file-kind'

type Overview = {
  devices: Device[]
  usage: Usage
  snapshots: Snapshot[]
  alerts: BackupAlert[]
  events: ActivityEvent[]
}

export default function HomePage() {
  const navigate = useNavigate()
  const { data, error, loading, reload } = useLoader<Overview>(
    async () => {
      const [devices, usage, snapshots, alerts, events] = await Promise.all([
        api.devices(),
        api.usage(),
        api.snapshots(),
        api.alerts(),
        api.events(8),
      ])
      return { devices, usage, snapshots, alerts, events }
    },
    { pollMs: 15000 },
  )

  // A single failed poll must not wipe the whole dashboard: keep rendering the
  // last good data and show the failure as a banner instead.
  if (error && !data) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <OverviewSkeleton />

  const { devices, usage, snapshots, alerts, events } = data
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
      {error && <ErrorNote>{error}</ErrorNote>}
      <HealthBanner devices={devices} newest={newest} />
      <AlertsPanel alerts={alerts} onDismissed={reload} />

      <section>
        <div className="mb-3 flex flex-wrap items-end justify-between gap-2">
          <h2 className="min-w-0 text-base font-semibold tracking-tight sm:text-lg">Your computers</h2>
          <Link to="/files" className="shrink-0 text-sm font-semibold text-brand-500 hover:underline">
            Browse files
          </Link>
        </div>

        {latestByDevice.length === 0 ? (
          <div className="admin-card">
            <Empty
              title="No files backed up yet"
              hint="Connect a device and run a backup."
            />
          </div>
        ) : (
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {latestByDevice.map((snapshot) => {
              const device = devices.find((d) => d.id === snapshot.device_id)
              const locked = Boolean(snapshot.encrypted || isEncryptedDevice(device))
              return (
              <button
                key={snapshot.device_id}
                type="button"
                className="content-tile"
                onClick={() =>
                  navigate(`/files?device=${encodeURIComponent(snapshot.device_id)}`)
                }
              >
                <EntryIcon path={snapshot.device_name ?? 'Computer'} type="dir" locked={locked} large />
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <div className="truncate text-sm font-semibold">
                      {snapshot.device_name ?? 'Computer'}
                    </div>
                    {locked && <LockBadge />}
                  </div>
                  <div className="mt-0.5 truncate text-xs text-gray-500">
                    {count(snapshot.file_count)} files · {bytes(snapshot.total_bytes)}
                  </div>
                  <div className="mt-2 text-[0.7rem] font-medium text-gray-500">
                    Updated {relative(snapshot.started_at)}
                  </div>
                </div>
              </button>
              )
            })}
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
        <div className="admin-card overflow-hidden">
          <header className="flex items-center justify-between border-b border-gray-200 dark:border-gray-800 px-5 py-3.5">
            <h2 className="text-sm font-semibold">Devices</h2>
            <Link to="/devices" className="text-xs font-semibold text-brand-500 hover:underline">
              Manage
            </Link>
          </header>
          {devices.length === 0 ? (
            <Empty title="No devices yet" hint="Create a code on Devices." />
          ) : (
            <ul>
              {devices.map((device) => (
                <li
                  key={device.id}
                  className="flex flex-col gap-2 border-b border-gray-200 px-4 py-3.5 last:border-0 sm:flex-row sm:items-center sm:justify-between sm:gap-4 sm:px-5 dark:border-gray-800"
                >
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate text-sm font-semibold">{device.name}</span>
                      <HealthBadge health={device.health} state={device.state} />
                    </div>
                    <div className="mt-0.5 truncate text-xs text-gray-500">
                      {platformLabel(device.platform)} · {relative(device.last_seen)}
                    </div>
                  </div>
                  <div className="text-left text-xs text-gray-500 sm:text-right">
                    <div className="tabular text-sm font-semibold text-gray-900 dark:text-white">
                      {bytes(device.logical_bytes)}
                    </div>
                    <div>Last backup {relative(device.last_backup_at)}</div>
                    <DeviceProgress device={device} />
                  </div>
                </li>
              ))}
            </ul>
          )}
        </div>

        <div className="admin-card p-5">
          <h2 className="text-sm font-semibold">Storage</h2>
          <div className="mt-4 space-y-4">
            <div className="flex items-baseline justify-between text-sm">
              <span className="text-gray-500">Used</span>
              <span className="tabular font-semibold">
                {bytes(usage.stored_bytes)}
                {usage.quota_bytes > 0 ? ` of ${bytes(usage.quota_bytes)}` : ''}
              </span>
            </div>
            {usage.quota_bytes > 0 ? (
              <Meter value={usage.stored_bytes} max={usage.quota_bytes} />
            ) : (
              <p className="text-xs text-gray-500">No quota — using free disk.</p>
            )}
            {usage.free_disk_bytes >= 0 && (
              <p className="text-sm">
                <span className="text-gray-500">Free on server · </span>
                <span className="tabular font-semibold">{bytes(usage.free_disk_bytes)}</span>
              </p>
            )}
            <p className="rounded-xl bg-gray-100 dark:bg-gray-800 px-3 py-2.5 text-xs leading-relaxed text-gray-500">
              Dedup + compression saved{' '}
              <strong className="text-gray-900 dark:text-white">
                {bytes(Math.max(usage.logical_bytes - usage.stored_bytes, 0))}
              </strong>{' '}
              so far.
            </p>
          </div>
        </div>
      </section>

      <section className="admin-card overflow-hidden">
        <header className="flex items-center justify-between border-b border-gray-200 px-5 py-3.5 dark:border-gray-800">
          <h2 className="text-sm font-semibold">Recent activity</h2>
          <Link to="/logs" className="text-xs font-semibold text-brand-500 hover:underline">
            All activity
          </Link>
        </header>
        {events.length === 0 ? (
          <Empty title="No activity yet" hint="A backup will show up here as it runs." />
        ) : (
          <ul>
            {events.map((event) => (
              <li
                key={`${event.at}-${event.message}`}
                className="flex flex-col gap-0.5 border-b border-gray-100 px-5 py-3 last:border-0 sm:flex-row sm:items-baseline sm:justify-between dark:border-gray-800"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm">{event.message}</div>
                  {event.device_name && <div className="text-xs text-gray-500">{event.device_name}</div>}
                </div>
                <div className="shrink-0 text-xs text-gray-500">{relative(event.at)}</div>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  )
}

function AlertsPanel({ alerts, onDismissed }: { alerts: BackupAlert[]; onDismissed: () => void }) {
  if (alerts.length === 0) return null
  return (
    <section id="alerts" className="space-y-2" aria-label="Alerts">
      {alerts.map((alert) => (
        <div
          key={alert.id}
          className={`flex flex-col gap-2 rounded-xl border px-4 py-3 sm:flex-row sm:items-center sm:justify-between ${
            alert.level === 'bad'
              ? 'border-error-200 bg-error-50 dark:border-error-500/30 dark:bg-error-500/10'
              : 'border-warning-200 bg-warning-50 dark:border-warning-500/30 dark:bg-warning-500/10'
          }`}
        >
          <div className="min-w-0">
            <div className="text-sm font-semibold">{alert.message}</div>
            <div className="text-xs text-gray-500">{relative(alert.opened_at)}</div>
          </div>
          <Button
            variant="ghost"
            onClick={() => {
              void api.ackAlert(alert.id).then(onDismissed)
            }}
          >
            Dismiss
          </Button>
        </div>
      ))}
    </section>
  )
}

function DeviceProgress({ device }: { device: Device }) {
  if (!device.files_total || (device.state !== 'uploading' && device.state !== 'scanning')) return null
  const ratio = Math.max(0, Math.min(1, (device.files_done ?? 0) / device.files_total))
  return (
    <div className="mt-2 max-w-xs sm:ml-auto">
      <div className="progress-track" aria-hidden>
        <div className="progress-fill" style={{ width: `${ratio * 100}%` }} />
      </div>
      <div className="mt-1 truncate text-[11px] text-gray-500">
        {device.files_done} / {device.files_total}
        {device.current_path ? ` · ${device.current_path}` : ''}
      </div>
    </div>
  )
}

function HealthBanner({ devices, newest }: { devices: Device[]; newest?: Snapshot }) {
  if (devices.length === 0) {
    return (
      <div className="relative overflow-hidden rounded-2xl border border-brand-200/60 bg-gradient-to-br from-brand-500 via-brand-600 to-brand-800 px-5 py-4 sm:px-6 sm:py-5 dark:border-brand-500/20">
        <div
          className="pointer-events-none absolute -top-12 -right-12 h-32 w-32 rounded-full bg-white/10 blur-2xl"
          aria-hidden
        />
        <div
          className="pointer-events-none absolute -bottom-14 -left-8 h-28 w-28 rounded-full bg-brand-300/20 blur-2xl"
          aria-hidden
        />

        <div className="relative flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
          <div className="min-w-0">
            <p className="text-xs font-medium text-brand-100">Get started</p>
            <h2 className="mt-0.5 text-lg font-bold tracking-tight text-white sm:text-xl">
              Ready when you are
            </h2>
            <p className="mt-1 max-w-xl text-sm text-brand-100/90">
              Create a connection code, then paste the code and server address into the desktop
              app on the computer you want to protect.
            </p>
          </div>

          <div className="flex shrink-0 flex-wrap gap-2">
            <Link
              to="/devices"
              className="inline-flex h-9 items-center gap-2 rounded-lg bg-white px-3.5 text-sm font-semibold text-brand-600 shadow-sm transition hover:bg-brand-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/50"
            >
              Add a device
            </Link>
            <Link
              to="/settings"
              className="inline-flex h-9 items-center gap-2 rounded-lg border border-white/25 bg-white/10 px-3.5 text-sm font-semibold text-white backdrop-blur-sm transition hover:bg-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/30"
            >
              Settings
            </Link>
          </div>
        </div>
      </div>
    )
  }

  const failing = devices.filter((d) => d.health === 'error')
  const stale = devices.filter((d) => d.health === 'stale')
  const never = devices.filter((d) => d.health === 'never_run')

  if (failing.length > 0) {
    return (
      <StatusHero
        tone="bad"
        eyebrow="Needs attention"
        title={`${failing.length} device${failing.length === 1 ? '' : 's'} reported an error`}
        description={failing[0].last_error || 'Check Logs for details.'}
        primary={{ to: '/logs', label: 'Open logs' }}
        secondary={{ to: '/devices', label: 'Devices' }}
      />
    )
  }
  if (stale.length > 0) {
    return (
      <StatusHero
        tone="warn"
        eyebrow="Out of date"
        title={`${stale.length} device${stale.length === 1 ? '' : 's'} need a sync`}
        description={`${stale.map((d) => d.name).join(', ')} may be offline.`}
        primary={{ to: '/devices', label: 'Check devices' }}
        secondary={{ to: '/logs', label: 'Logs' }}
      />
    )
  }
  if (never.length > 0) {
    const name = never.length === 1 ? never[0].name : `${never.length} devices`
    return (
      <StatusHero
        tone="warn"
        eyebrow="Almost there"
        title={`${name} never backed up`}
        description={
          never.length === 1
            ? 'Run the first backup from Devices to start protecting files.'
            : `${never.map((d) => d.name).join(', ')} still need a first backup.`
        }
        primary={{ to: '/devices', label: 'Start backup' }}
        secondary={{ to: '/settings', label: 'Settings' }}
      />
    )
  }
  return (
    <StatusHero
      tone="good"
      eyebrow="All good"
      title="Everything is backed up"
      description={`Last run ${relative(newest?.started_at)} · ${count(newest?.file_count)} files.`}
      primary={{ to: '/files', label: 'Browse files' }}
      secondary={{ to: '/devices', label: 'Devices' }}
    />
  )
}

function StatusHero({
  tone,
  eyebrow,
  title,
  description,
  primary,
  secondary,
}: {
  tone: 'good' | 'warn' | 'bad'
  eyebrow: string
  title: string
  description: string
  primary: { to: string; label: string }
  secondary: { to: string; label: string }
}) {
  const styles = {
    good: {
      box: 'border-success-400/40 bg-gradient-to-br from-success-500 via-success-600 to-success-700 dark:border-success-500/25',
      eyebrow: 'text-success-100',
      body: 'text-success-100/90',
      glow: 'bg-success-300/25',
    },
    warn: {
      box: 'border-warning-400/40 bg-gradient-to-br from-warning-500 via-warning-600 to-warning-700 dark:border-warning-500/25',
      eyebrow: 'text-warning-100',
      body: 'text-warning-100/90',
      glow: 'bg-warning-300/25',
    },
    bad: {
      box: 'border-error-400/40 bg-gradient-to-br from-error-500 via-error-600 to-error-700 dark:border-error-500/25',
      eyebrow: 'text-error-100',
      body: 'text-error-100/90',
      glow: 'bg-error-300/25',
    },
  }[tone]

  return (
    <div className={`relative overflow-hidden rounded-2xl border px-5 py-4 sm:px-6 sm:py-5 ${styles.box}`}>
      <div
        className="pointer-events-none absolute -top-12 -right-12 h-32 w-32 rounded-full bg-white/10 blur-2xl"
        aria-hidden
      />
      <div
        className={`pointer-events-none absolute -bottom-14 -left-8 h-28 w-28 rounded-full blur-2xl ${styles.glow}`}
        aria-hidden
      />

      <div className="relative flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between sm:gap-4">
        <div className="min-w-0">
          <p className={`text-xs font-medium ${styles.eyebrow}`}>{eyebrow}</p>
          <h2 className="mt-0.5 text-lg font-bold tracking-tight text-white sm:text-xl">{title}</h2>
          <p className={`mt-1 max-w-xl text-sm ${styles.body}`}>{description}</p>
        </div>

        <div className="flex shrink-0 flex-wrap gap-2">
          <Link
            to={primary.to}
            className="inline-flex h-9 items-center gap-2 rounded-lg bg-white px-3.5 text-sm font-semibold text-gray-800 shadow-sm transition hover:bg-white/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/50"
          >
            {primary.label}
          </Link>
          <Link
            to={secondary.to}
            className="inline-flex h-9 items-center gap-2 rounded-lg border border-white/25 bg-white/10 px-3.5 text-sm font-semibold text-white backdrop-blur-sm transition hover:bg-white/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/30"
          >
            {secondary.label}
          </Link>
        </div>
      </div>
    </div>
  )
}
