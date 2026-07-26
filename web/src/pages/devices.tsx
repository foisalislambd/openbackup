/**
 * Devices is where a machine is added, and where the connection code appears.
 * The code is shown next to the exact command it goes into, so connecting a
 * computer is a copy and a paste rather than a documentation hunt.
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
    <div className="space-y-6">
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
            {invite.busy === 'create' ? 'Creating…' : 'Create connection code'}
          </Button>
        }
      >
        {code ? (
          <div className="space-y-3">
            <p className="text-sm">
              Run this on the computer you want to back up. The code works once, and expires{' '}
              {until(code.expires_at)}.
            </p>
            <CopyBlock text={`openbackup connect --server ${code.server_url} --code ${code.code}`} />
            <p className="text-xs text-[var(--color-ink-muted)]">
              Not installed yet? On Linux and macOS:{' '}
              <code className="rounded bg-[var(--color-surface-muted)] px-1 py-0.5">
                curl -fsSL https://raw.githubusercontent.com/foisalislambd/openbackup/main/scripts/install-agent.sh | sh
              </code>
              . On Windows, download the installer from the releases page.
            </p>
          </div>
        ) : (
          <p className="text-sm text-[var(--color-ink-muted)]">
            Each device gets its own one-time code. Nothing else needs configuring: the agent finds your documents,
            pictures and other personal folders by itself, and skips system files and things like{' '}
            <code className="rounded bg-[var(--color-surface-muted)] px-1 py-0.5 text-xs">node_modules</code>.
          </p>
        )}
      </Card>

      <Card title="Connected devices">
        {loading ? (
          <DevicesSkeleton />
        ) : !devices || devices.length === 0 ? (
          <Empty title="No devices yet" hint="Create a connection code above." />
        ) : (
          <ul className="divide-y divide-[var(--color-border-subtle)]">
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
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          {renaming ? (
            <form
              className="flex items-center gap-2"
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
              <input className={`${inputClass} mt-0 w-48`} value={name} onChange={(e) => setName(e.target.value)} />
              <Button type="submit" variant="primary" disabled={busy === 'rename'}>
                Save
              </Button>
              <Button variant="ghost" onClick={() => setRenaming(false)}>
                Cancel
              </Button>
            </form>
          ) : (
            <div className="flex items-center gap-2">
              <span className="text-sm font-medium">{device.name}</span>
              <HealthBadge health={device.health} state={device.state} />
              {device.agent_version && <Badge>v{device.agent_version}</Badge>}
            </div>
          )}
          <div className="mt-1 text-xs text-[var(--color-ink-muted)]">
            {platformLabel(device.platform)}
            {device.os_version ? ` · ${device.os_version}` : ''} · {device.hostname} · last seen{' '}
            {relative(device.last_seen)}
          </div>
          <div className="mt-1 text-xs text-[var(--color-ink-muted)]">
            {count(device.snapshot_count)} backups · {bytes(device.logical_bytes)} of files · last backup{' '}
            {relative(device.last_backup_at)}
          </div>
          {device.last_error && <div className="mt-1 text-xs text-[var(--color-bad)]">{device.last_error}</div>}
          {error && <div className="mt-1 text-xs text-[var(--color-bad)]">{error}</div>}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button
            disabled={!!busy}
            onClick={() => void run('backup', () => api.sendCommand(device.id, 'backup_now'), onChanged)}
          >
            {busy === 'backup' ? 'Sending…' : 'Back up now'}
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
                  `Remove ${device.name}?\n\nIts backups are kept and can still be restored, but the device can no longer upload.`,
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
    <div className="flex items-center gap-2 rounded-lg border border-[var(--color-border-subtle)] bg-[var(--color-surface-muted)] p-3">
      <code className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-xs">{text}</code>
      <Button
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
