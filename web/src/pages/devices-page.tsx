/**
 * Devices is where a machine is added, and where the connection code appears.
 */

import { useState } from 'react'
import { api, type Device } from '@/lib/api'
import { bytes, count, platformLabel, relative, until } from '@/lib/format'
import { useAction, useLoader } from '@/lib/use-loader'
import { HealthBadge } from '@/components/health-badge'
import { Badge, Button, Card, Empty, ErrorNote, inputClass } from '@/components/ui'
import { DevicesSkeleton } from '@/components/skeleton'

export default function DevicesPage() {
  const { data: devices, error, loading, reload } = useLoader<Device[]>(() => api.devices(), { pollMs: 15000 })
  const invite = useAction()
  const [code, setCode] = useState<{ code: string; server_url: string; expires_at: string }>()

  return (
    <div className="space-y-5 sm:space-y-6">
      {error && <ErrorNote>{error}</ErrorNote>}
      {invite.error && <ErrorNote>{invite.error}</ErrorNote>}

      <Card
        title="Add a device"
        action={
          <Button
            variant="primary"
            disabled={invite.busy === 'create'}
            onClick={() => {
              void invite.run('create', async () => setCode(await api.createJoinToken('')))
            }}
          >
            {invite.busy === 'create' ? 'Creating…' : 'Create code'}
          </Button>
        }
      >
        {code ? (
          <div className="space-y-3">
            <p className="text-sm text-gray-500">
              Run on that computer. Code expires {until(code.expires_at)}.
            </p>
            <CopyBlock text={`openbackup connect --server ${code.server_url} --code ${code.code}`} />
            <p className="text-xs leading-relaxed text-gray-500">
              Not installed? Linux/macOS:{' '}
              <code className="break-all rounded bg-gray-100 px-1 py-0.5 dark:bg-gray-800">
                curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh
              </code>
              . Windows: download the installer.
            </p>
          </div>
        ) : (
          <p className="text-sm text-gray-500">
            One-time code per device. The agent picks personal folders and skips system junk.
          </p>
        )}
      </Card>

      <Card title="Connected devices">
        {loading ? (
          <DevicesSkeleton />
        ) : !devices || devices.length === 0 ? (
          <Empty title="No devices yet" hint="Create a code above." />
        ) : (
          <ul className="divide-y divide-gray-100 dark:divide-gray-800">
            {devices.map((device) => (
              <DeviceRow key={device.id} device={device} onChanged={reload} />
            ))}
          </ul>
        )}
      </Card>
    </div>
  )
}

function DeviceRow({ device, onChanged }: { device: Device; onChanged: () => void }) {
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState(device.name)
  const { busy, error, run } = useAction()

  return (
    <li className="py-4 first:pt-0 last:pb-0">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0 flex-1">
          {renaming ? (
            <form
              className="flex flex-wrap items-center gap-2"
              onSubmit={(event) => {
                event.preventDefault()
                void run(
                  'rename',
                  async () => {
                    await api.renameDevice(device.id, name)
                    setRenaming(false)
                  },
                  onChanged,
                )
              }}
            >
              <input
                className={`${inputClass} mt-0 w-full max-w-xs sm:w-48`}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <Button type="submit" variant="primary" disabled={busy === 'rename'}>
                Save
              </Button>
              <Button variant="ghost" onClick={() => setRenaming(false)}>
                Cancel
              </Button>
            </form>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              <span className="truncate text-sm font-medium">{device.name}</span>
              <HealthBadge health={device.health} state={device.state} />
              {device.agent_version && <Badge>v{device.agent_version}</Badge>}
            </div>
          )}
          <div className="mt-1 truncate text-xs text-gray-500">
            {platformLabel(device.platform)}
            {device.hostname ? ` · ${device.hostname}` : ''} · seen {relative(device.last_seen)}
          </div>
          <div className="mt-0.5 text-xs text-gray-500">
            {count(device.snapshot_count)} backups · {bytes(device.logical_bytes)} · last{' '}
            {relative(device.last_backup_at)}
          </div>
          {device.last_error && <div className="mt-1 text-xs text-error-500">{device.last_error}</div>}
          {error && <div className="mt-1 text-xs text-error-500">{error}</div>}
        </div>

        <div className="flex flex-wrap items-center gap-2 sm:justify-end">
          <Button
            disabled={!!busy}
            onClick={() => void run('backup', () => api.sendCommand(device.id, 'backup_now'), onChanged)}
          >
            {busy === 'backup' ? 'Sending…' : 'Back up'}
          </Button>
          {device.state === 'paused' ? (
            <Button
              disabled={!!busy}
              onClick={() => void run('resume', () => api.sendCommand(device.id, 'resume'), onChanged)}
            >
              Resume
            </Button>
          ) : (
            <Button
              disabled={!!busy}
              onClick={() => void run('pause', () => api.sendCommand(device.id, 'pause'), onChanged)}
            >
              Pause
            </Button>
          )}
          <Button onClick={() => setRenaming(true)} variant="ghost">
            Rename
          </Button>
          <Button
            variant="danger"
            disabled={!!busy}
            onClick={() => {
              if (
                !confirm(
                  `Remove ${device.name}?\n\nBackups stay; this device can no longer upload.`,
                )
              ) {
                return
              }
              void run('remove', () => api.removeDevice(device.id), onChanged)
            }}
          >
            Remove
          </Button>
        </div>
      </div>
    </li>
  )
}

function CopyBlock({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-gray-200 bg-gray-100 p-3 sm:flex-row sm:items-center dark:border-gray-800 dark:bg-gray-800">
      <code className="min-w-0 flex-1 overflow-x-auto break-all font-mono text-xs whitespace-pre-wrap sm:whitespace-nowrap sm:break-normal">
        {text}
      </code>
      <Button
        className="shrink-0 self-end sm:self-auto"
        onClick={() => {
          void navigator.clipboard.writeText(text).then(() => {
            setCopied(true)
            setTimeout(() => setCopied(false), 2000)
          })
        }}
      >
        {copied ? 'Copied' : 'Copy'}
      </Button>
    </div>
  )
}
