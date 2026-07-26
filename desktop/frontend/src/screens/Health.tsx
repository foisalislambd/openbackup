import { api } from '../lib/bridge'
import { bytes, count, when } from '../lib/format'
import { useAction, useAsync } from '../lib/use-status'
import type { Overview } from '../lib/types'
import { Badge, Button, Card, Notice, Rows, Spinner } from '../components/ui'

/** Health is the "prove it" screen.
 *
 *  It runs the same checks as 'openbackup doctor' so a user can see, item by item,
 *  which part of the chain is working: the configuration, the service, the server,
 *  the folders, and whether anything actually arrived. */
export function Health({ status }: { status: Overview }) {
  const checks = useAsync(() => api.diagnostics(), [])
  const action = useAction()

  const failing = checks.data?.filter((check) => !check.ok) ?? []

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <Card
        title="Is everything working?"
        description="Each step between your files and the server, checked now."
        actions={
          <Button busy={checks.loading} onClick={checks.reload}>
            Run the checks again
          </Button>
        }
      >
        {checks.error && <Notice tone="bad" title="Could not run the checks">{checks.error}</Notice>}

        {checks.loading && !checks.data ? (
          <div className="flex items-center gap-2 py-6 text-sm text-ink-muted">
            <Spinner /> Checking...
          </div>
        ) : (
          <>
            {failing.length === 0 && checks.data?.length ? (
              <div className="mb-4">
                <Notice tone="good" title="Everything looks healthy">
                  Your files are reaching the server, and the agent is running.
                </Notice>
              </div>
            ) : null}
            <Rows>
              {checks.data?.map((check) => (
                <div key={check.name} className="flex items-start justify-between gap-4 py-3">
                  <div className="min-w-0">
                    <p className="text-sm text-ink">{check.name}</p>
                    <p className="selectable mt-0.5 break-words text-xs text-ink-muted">{check.detail}</p>
                  </div>
                  <Badge tone={check.ok ? 'good' : 'bad'}>{check.ok ? 'OK' : 'Needs attention'}</Badge>
                </div>
              ))}
            </Rows>
          </>
        )}
      </Card>

      <Card title="What this device has backed up">
        <dl className="grid grid-cols-2 gap-4 text-sm">
          <Item label="Files tracked here" value={count(status.tracked_files)} />
          <Item label="Size on this device" value={bytes(status.tracked_bytes)} />
          <Item label="Backups on the server" value={count(status.snapshot_count)} />
          <Item label="Most recent" value={when(status.last_backup_at)} />
        </dl>
        {status.last_error && (
          <div className="mt-4">
            <Notice tone="warn" title="Last reported problem">
              {status.last_error}
            </Notice>
          </div>
        )}
      </Card>

      <Card
        title="Something still wrong?"
        description="The agent's log has the detail, and it is the most useful thing to attach to a bug report."
      >
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => action.run(api.openLogFolder)}>Open log folder</Button>
          <Button onClick={() => action.run(api.backupNow)} disabled={!status.agent_running}>
            Force a backup now
          </Button>
          {!status.agent_running && (
            <Button tone="primary" busy={action.busy} onClick={() => action.run(api.startService)}>
              Start the background service
            </Button>
          )}
        </div>
        {action.error && (
          <div className="mt-3">
            <Notice tone="bad">{action.error}</Notice>
          </div>
        )}
      </Card>
    </div>
  )
}

function Item({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-ink-muted">{label}</dt>
      <dd className="tabular mt-0.5 text-sm font-medium text-ink">{value}</dd>
    </div>
  )
}
