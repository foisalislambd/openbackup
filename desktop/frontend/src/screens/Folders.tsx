import { api } from '../lib/bridge'
import { useAction, useAsync } from '../lib/use-status'
import type { Folder, Overview } from '../lib/types'
import { Badge, Button, Card, Empty, Notice, Rows, Spinner } from '../components/ui'

/** Folders is where a user decides what is protected.
 *
 *  The list is the truth of what is backed up, so it says plainly when a folder has
 *  gone missing or has been switched off. A folder silently dropping out of a
 *  backup is the failure mode this screen exists to prevent. */
export function Folders({ status }: { status: Overview }) {
  const folders = useAsync(() => api.folders(), [status.folder_count, status.missing_folders])
  const suggestions = useAsync(() => api.suggestedFolders(), [status.folder_count])
  const action = useAction()

  const reload = () => {
    folders.reload()
    suggestions.reload()
  }

  const add = () => action.run(api.chooseFolder, reload)
  const remove = (folder: Folder) => action.run(() => api.removeFolder(folder.path), reload)
  const toggle = (folder: Folder) =>
    action.run(() => api.setFolderEnabled(folder.path, !folder.enabled), reload)

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5">
      <Card
        title="Folders being backed up"
        description="Changes take effect immediately. Removing a folder keeps the backups it already has."
        actions={
          <Button tone="primary" busy={action.busy} onClick={add}>
            Add a folder
          </Button>
        }
      >
        {action.error && (
          <div className="mb-4">
            <Notice tone="bad" title="That did not work">
              {action.error}
            </Notice>
          </div>
        )}

        {folders.error && (
          <div className="mb-4">
            <Notice tone="bad" title="Could not load folders">
              {folders.error}
            </Notice>
          </div>
        )}

        {folders.loading && !folders.data ? (
          <div className="flex items-center gap-2 py-6 text-sm text-ink-muted">
            <Spinner /> Loading folders...
          </div>
        ) : !folders.data?.length ? (
          <Empty title="No folders are being backed up">
            Add a folder to start protecting it. Your Documents, Pictures and Desktop are usually a
            good place to begin.
          </Empty>
        ) : (
          <Rows>
            {folders.data.map((folder) => (
              <div key={folder.path} className="flex items-center justify-between gap-4 py-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <p className="truncate text-sm font-medium text-ink">{folder.label}</p>
                    {!folder.exists && <Badge tone="warn">Not found</Badge>}
                    {folder.exists && !folder.enabled && <Badge>Paused</Badge>}
                    {folder.exists && folder.enabled && <Badge tone="good">Backed up</Badge>}
                  </div>
                  <p className="selectable mt-0.5 truncate text-xs text-ink-muted" title={folder.path}>
                    {folder.path}
                  </p>
                </div>
                <div className="flex shrink-0 items-center gap-1">
                  {folder.exists && (
                    <Button tone="quiet" onClick={() => action.run(() => api.revealFolder(folder.path))}>
                      Open
                    </Button>
                  )}
                  <Button tone="quiet" onClick={() => toggle(folder)}>
                    {folder.enabled ? 'Pause' : 'Resume'}
                  </Button>
                  <Button tone="quiet" onClick={() => remove(folder)}>
                    Remove
                  </Button>
                </div>
              </div>
            ))}
          </Rows>
        )}
      </Card>

      {!!suggestions.data?.length && (
        <Card
          title="Not backed up yet"
          description="These folders are on this computer but are not included."
        >
          <Rows>
            {suggestions.data.map((folder) => (
              <div key={folder.path} className="flex items-center justify-between gap-4 py-3">
                <div className="min-w-0">
                  <p className="truncate text-sm text-ink">{folder.label}</p>
                  <p className="truncate text-xs text-ink-muted" title={folder.path}>
                    {folder.path}
                  </p>
                </div>
                <Button
                  busy={action.busy}
                  onClick={() => action.run(() => api.addFolder(folder.path), reload)}
                >
                  Back this up
                </Button>
              </div>
            ))}
          </Rows>
        </Card>
      )}

      <Card title="What is skipped automatically">
        <ul className="list-disc space-y-1.5 pl-5 text-sm text-ink-muted">
          <li>Windows, Program Files, system and recovery folders - restoring those would not fix a computer anyway.</li>
          <li>Caches, temporary files and browser data, which are rebuilt on their own.</li>
          <li>
            Inside a code project, its dependency and build folders (<code className="font-mono text-xs">node_modules</code>,{' '}
            <code className="font-mono text-xs">target</code>, <code className="font-mono text-xs">dist</code>) - the source code
            itself is always backed up.
          </li>
          <li>Virtual machine disks and disk images, which are large and usually replaceable.</li>
        </ul>
      </Card>
    </div>
  )
}
