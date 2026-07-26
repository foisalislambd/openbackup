import { api } from '../lib/bridge'
import { ago, bytes, count } from '../lib/format'
import { useAction } from '../lib/use-status'
import type { Overview } from '../lib/types'
import { Button, Card, Notice } from '../components/ui'

/** Home answers one question: is my data safe right now?
 *
 *  Everything else on this screen is subordinate to that answer, and the answer is
 *  never inferred from optimism - it comes from what the server confirms it has. */
export function Home({
  status,
  onGoToFolders,
}: {
  status: Overview
  onGoToFolders: () => void
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

  const progress =
    status.files_total > 0 ? Math.min(100, (status.files_done / status.files_total) * 100) : null

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <section className="rounded-xl border border-border-subtle bg-surface p-6">
        <div className="flex items-start justify-between gap-6">
          <div className="min-w-0">
            <p className="text-xs font-medium uppercase tracking-wide text-ink-muted">
              {status.encrypted ? 'End-to-end encrypted' : 'This device'}
            </p>
            <h1 className="mt-1 text-2xl font-semibold text-ink">{status.headline}</h1>
            <p className="selectable mt-1.5 text-sm text-ink-muted">{status.detail}</p>
          </div>
          <div className="flex shrink-0 flex-col gap-2">
            {status.paused ? (
              <Button tone="primary" busy={action.busy} onClick={() => action.run(api.resume)}>
                Resume backups
              </Button>
            ) : (
              <Button
                tone="primary"
                busy={action.busy}
                disabled={!status.agent_running}
                onClick={() => action.run(api.backupNow)}
              >
                Back up now
              </Button>
            )}
            {!status.paused && status.agent_running && (
              <Button tone="quiet" onClick={() => action.run(() => api.pause(60))}>
                Pause for an hour
              </Button>
            )}
          </div>
        </div>

        {progress !== null && status.state === 'uploading' && (
          <div className="mt-5">
            <div className="h-1.5 overflow-hidden rounded-full bg-surface-muted">
              <div className="h-full rounded-full bg-brand transition-all" style={{ width: `${progress}%` }} />
            </div>
            <p className="mt-2 truncate text-xs text-ink-muted" title={status.current_path}>
              {status.current_path || 'Working...'}
            </p>
          </div>
        )}
      </section>

      {action.error && <Notice tone="bad" title="That did not work">{action.error}</Notice>}

      {!status.agent_running && (
        <Notice
          tone="warn"
          title="The background service is not running"
          action={
            <Button busy={action.busy} onClick={() => action.run(api.startService)}>
              Start it
            </Button>
          }
        >
          Backups only happen while the service runs. Starting it may ask for administrator
          permission.
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
