import { api } from '../lib/bridge'
import { ago, bytes, count } from '../lib/format'
import { useAction } from '../lib/use-status'
import type { Overview } from '../lib/types'
import { Button, Card, Notice, Progress } from '../components/ui'

/** Home answers one question: is my data safe right now?
 *
 *  Everything else on this screen is subordinate to that answer, and the answer is
 *  never inferred from optimism - it comes from what the server confirms it has. */
export function Home({
  status,
  onGoToFolders,
  onRefresh,
}: {
  status: Overview
  onGoToFolders: () => void
  onRefresh?: () => void
}) {
  const action = useAction()

  const tone =
    status.health === 'protected'
      ? 'good'
      : status.health === 'working'
        ? 'info'
        : status.health === 'paused' || status.health === 'never_run'
          ? 'info'
          : 'warn'
  const hero =
    tone === 'good'
      ? 'from-[#12b76a] via-[#039855] to-[#027a48]'
      : tone === 'warn'
        ? 'from-[#f79009] via-[#dc6803] to-[#b54708]'
        : 'from-[#465fff] via-[#3641f5] to-[#2a31d8]'

  const progress =
    status.files_total > 0 ? Math.min(100, (status.files_done / status.files_total) * 100) : null

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <section className={`relative overflow-hidden rounded-2xl bg-gradient-to-br px-6 py-5 text-white ${hero}`}>
        <div className="flex items-start justify-between gap-6">
          <div className="min-w-0">
            <p className="text-xs font-medium text-white/80">
              {status.encrypted ? 'End-to-end encrypted' : 'This device'}
            </p>
            <h1 className="mt-1 text-2xl font-semibold tracking-tight">{status.headline}</h1>
            <p className="selectable mt-1.5 text-sm text-white/85">{status.detail}</p>
          </div>
          <div className="flex shrink-0 flex-col gap-2">
            {status.paused ? (
              <Button
                tone="primary"
                className="!bg-white !text-[#101828] hover:!bg-white/90"
                busy={action.busy}
                onClick={() => action.run(api.resume)}
              >
                Resume backups
              </Button>
            ) : (
              <Button
                tone="primary"
                className="!bg-white !text-[#101828] hover:!bg-white/90"
                busy={action.busy}
                disabled={!status.agent_running}
                onClick={() => action.run(api.backupNow)}
              >
                Back up now
              </Button>
            )}
            {!status.paused && status.agent_running && (
              <Button
                tone="quiet"
                className="!text-white hover:!bg-white/15"
                onClick={() => action.run(() => api.pause(60))}
              >
                Pause for an hour
              </Button>
            )}
          </div>
        </div>

        {progress !== null && (status.state === 'uploading' || status.state === 'scanning') && (
          <div className="mt-5">
            <Progress value={progress} onDark />
            <p className="mt-2 truncate text-xs text-white/80" title={status.current_path}>
              {status.files_done} of {status.files_total}
              {status.current_path ? ` · ${status.current_path}` : ''}
            </p>
          </div>
        )}
      </section>

      {action.error && <Notice tone="bad" title="That did not work">{action.error}</Notice>}

      {!status.agent_running && (
        <Notice
          tone="warn"
          title="Backups are not running"
          action={
            <Button
              busy={action.busy}
              onClick={() => action.run(api.startService, onRefresh)}
            >
              Start backups
            </Button>
          }
        >
          Closing the window keeps backups going in the tray. Use Quit from the tray
          menu only when you want them to stop.
        </Notice>
      )}

      {status.missing_folders > 0 && (
        <Notice
          tone="warn"
          title={`${status.missing_folders} folder${status.missing_folders === 1 ? '' : 's'} cannot be found`}
          action={<Button onClick={onGoToFolders}>Review folders</Button>}
        >
          They may have been moved, renamed, or live on a drive that is not connected. Nothing in
          them is being backed up.
        </Notice>
      )}

      {status.server_error && (
        <Notice tone="warn" title="Cannot reach the server">
          {status.server_error}. Backups will continue and catch up once it is reachable again.
        </Notice>
      )}

      {status.last_error && status.health !== 'error' && (
        <Notice tone="info" title="Last reported problem">
          {status.last_error}
        </Notice>
      )}

      <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
        <Stat label="Last backup" value={ago(status.last_backup_at)} tone={tone} />
        <Stat label="Files protected" value={count(status.last_backup_files || status.tracked_files)} />
        <Stat label="Size" value={bytes(status.last_backup_size || status.tracked_bytes)} />
        <Stat label="Versions kept" value={count(status.snapshot_count)} />
      </div>

      <Card
        title="This device"
        actions={
          <Button tone="quiet" onClick={() => action.run(api.openDashboard)}>
            Open dashboard
          </Button>
        }
      >
        <dl className="grid grid-cols-1 gap-x-6 gap-y-3 text-sm sm:grid-cols-2">
          <Row label="Folders backed up" value={`${status.folder_count}`} />
          <Row label="Encryption" value={status.encrypted ? 'On (end-to-end)' : 'Off'} />
          <Row label="Server" value={status.server_url.replace(/^https?:\/\//, '')} />
          <Row label="Agent" value={status.agent_running ? 'Running' : 'Stopped'} />
        </dl>
      </Card>
    </div>
  )
}

function Stat({
  label,
  value,
  tone = 'default',
}: {
  label: string
  value: string
  tone?: 'default' | 'good' | 'info' | 'warn'
}) {
  const colour = tone === 'good' ? 'text-good' : tone === 'warn' ? 'text-warn' : 'text-ink'
  return (
    <div className="rounded-xl border border-border-subtle bg-surface p-4">
      <p className="text-xs text-ink-muted">{label}</p>
      <p className={`tabular mt-1 text-lg font-semibold ${colour}`}>{value}</p>
    </div>
  )
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3 border-b border-border-subtle pb-2">
      <dt className="text-ink-muted">{label}</dt>
      <dd className="selectable truncate text-ink" title={value}>
        {value}
      </dd>
    </div>
  )
}
