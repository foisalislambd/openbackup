'use client'

/**
 * Devices is where a machine is added, and where the connection code is shown.
 * The code is displayed exactly once, next to the command to paste it into, so
 * connecting a computer is a copy and a paste rather than a documentation hunt.
 */

import { useCallback, useEffect, useState } from 'react'
import { api, type Device } from '@/lib/api'
import { bytes, count, platformLabel, relative } from '@/lib/format'
import { Badge, Button, Card, Empty, ErrorNote, Field, inputClass, Spinner } from '@/components/ui'
import { HealthBadge } from '../page'

export default function DevicesPage() {
  const [devices, setDevices] = useState<Device[]>()
  const [error, setError] = useState<string>()
  const [invite, setInvite] = useState<{ code: string; server_url: string; expires_at: string }>()
  const [busy, setBusy] = useState(false)

  const load = useCallback(async () => {
    try {
      setDevices(await api.devices())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not load devices')
    }
  }, [])

  useEffect(() => {
    void load()
    const timer = setInterval(load, 15000)
    return () => clearInterval(timer)
  }, [load])

  const addDevice = async () => {
    setBusy(true)
    setError(undefined)
    try {
      setInvite(await api.createJoinToken(''))
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not create a code')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-6">
      {error && <ErrorNote>{error}</ErrorNote>}

      <Card
        title="Add a device"
        action={
          <Button variant="primary" onClick={addDevice} disabled={busy}>
            {busy ? 'Creating…' : 'Create connection code'}
          </Button>
        }
      >
        {invite ? (
          <div className="space-y-3">
            <p className="text-sm">
              Run this on the computer you want to back up. The code works once and expires{' '}
              {relative(invite.expires_at).replace(' ago', '')} from now.
            </p>
            <CopyBlock
              text={`openbackup connect --server ${invite.server_url} --code ${invite.code}`}
            />
            <p className="text-xs text-[var(--color-ink-muted)]">
              Not installed yet? On Linux and macOS:{' '}
              <code className="rounded bg-[var(--color-surface-muted)] px-1 py-0.5">
                curl -fsSL {invite.server_url}/install.sh | sh
              </code>
              . On Windows, download the installer from the releases page.
            </p>
          </div>
        ) : (
          <p className="text-sm text-[var(--color-ink-muted)]">
            Each device gets its own one-time code. Nothing else needs configuring: the agent finds your
            documents, pictures and other personal folders by itself, and skips system files and things like{' '}
            <code className="rounded bg-[var(--color-surface-muted)] px-1 py-0.5 text-xs">node_modules</code>.
          </p>
        )}
      </Card>

      <Card title="Connected devices">
        {!devices ? (
          <Spinner />
        ) : devices.length === 0 ? (
          <Empty title="No devices yet" hint="Create a connection code above." />
        ) : (
          <ul className="divide-y divide-[var(--color-border-subtle)]">
            {devices.map((device) => (
              <DeviceRow key={device.id} device={device} onChanged={load} />
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
  const [busy, setBusy] = useState<string>()
  const [error, setError] = useState<string>()

  const act = async (label: string, fn: () => Promise<unknown>) => {
    setBusy(label)
    setError(undefined)
    try {
      await fn()
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'That did not work')
    } finally {
      setBusy(undefined)
    }
  }

  return (
    <li className="py-4 first:pt-0 last:pb-0">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          {renaming ? (
            <form
              className="flex items-center gap-2"
              onSubmit={(event) => {
                event.preventDefault()
                void act('rename', async () => {
                  await api.renameDevice(device.id, name)
                  setRenaming(false)
                })
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
          <Button onClick={() => act('backup', () => api.sendCommand(device.id, 'backup_now'))} disabled={!!busy}>
            {busy === 'backup' ? 'Sending…' : 'Back up now'}
          </Button>
          {device.state === 'paused' ? (
            <Button onClick={() => act('resume', () => api.sendCommand(device.id, 'resume'))} disabled={!!busy}>
              Resume
            </Button>
          ) : (
            <Button onClick={() => act('pause', () => api.sendCommand(device.id, 'pause'))} disabled={!!busy}>
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
              void act('remove', () => api.removeDevice(device.id))
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
        onClick={async () => {
          await navigator.clipboard.writeText(text)
          setCopied(true)
          setTimeout(() => setCopied(false), 2000)
        }}
      >
        {copied ? 'Copied' : 'Copy'}
      </Button>
    </div>
  )
}
