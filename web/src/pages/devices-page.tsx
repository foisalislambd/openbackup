/**
 * Devices is where a machine is added, and where the connection code appears.
 */

import { useState } from 'react'
import { api, type Device } from '@/lib/api'
import { bytes, count, formatVersion, platformLabel, relative, until } from '@/lib/format'
import { useAction, useLoader } from '@/lib/use-loader'
import { isEncryptedDevice } from '@/lib/file-kind'
import { HealthBadge } from '@/components/health-badge'
import { cn } from '@/lib/cn'
import { Badge, Button, Card, Empty, ErrorNote, inputClass } from '@/components/ui'
import { DevicesSkeleton } from '@/components/skeleton'
import { LockBadge } from '@/components/file-icon'
import { useToast } from '@/lib/toast-context'

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
          <div className="space-y-4">
            <p className="text-sm text-gray-500">
              Paste into the desktop app. Expires {until(code.expires_at)}.
            </p>
            <CopyRow label="Connection code" action="Copy code" text={code.code} emphasize />
            <CopyRow label="Server address" action="Copy address" text={code.server_url} />
            <div className="border-t border-gray-100 pt-4 dark:border-gray-800">
              <CopyRow
                label="Command line"
                action="Copy command"
                text={`openbackup connect --server ${code.server_url} --code ${code.code}`}
                compact
              />
            </div>
          </div>
        ) : (
          <p className="text-sm text-gray-500">Paste the code into the desktop app.</p>
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
  const toast = useToast()

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
              {isEncryptedDevice(device) && <LockBadge />}
              {device.agent_version && (
                <Badge>{formatVersion(device.agent_version)}</Badge>
              )}
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
          <DeviceProgress device={device} />
          {device.last_error && <div className="mt-1 text-xs text-error-500">{device.last_error}</div>}
          {error && <div className="mt-1 text-xs text-error-500">{error}</div>}
        </div>

        <div className="flex flex-wrap items-center gap-2 sm:justify-end">
          <Button href={`/files?device=${encodeURIComponent(device.id)}`}>Browse files</Button>
          <Button
            disabled={!!busy}
            onClick={() =>
              void run(
                'backup',
                () => api.sendCommand(device.id, 'backup_now'),
                () => {
                  toast.push('Backup requested. It starts on the next check-in.')
                  onChanged()
                },
              )
            }
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

function DeviceProgress({ device }: { device: Device }) {
  if (!device.files_total || (device.state !== 'uploading' && device.state !== 'scanning')) return null
  const done = device.files_done ?? 0
  const ratio = Math.max(0, Math.min(1, done / device.files_total))
  return (
    <div className="mt-2 max-w-md">
      <div className="progress-track" role="progressbar" aria-valuenow={Math.round(ratio * 100)} aria-valuemin={0} aria-valuemax={100}>
        <div className="progress-fill" style={{ width: `${ratio * 100}%` }} />
      </div>
      <p className="mt-1 truncate text-xs text-gray-500">
        {done} of {device.files_total} files
        {device.current_path ? ` · ${device.current_path}` : ''}
      </p>
    </div>
  )
}

function CopyRow({
  label,
  action,
  text,
  emphasize,
  compact,
}: {
  label: string
  action: string
  text: string
  emphasize?: boolean
  compact?: boolean
}) {
  const [copied, setCopied] = useState(false)
  const [failed, setFailed] = useState(false)
  // Plain-HTTP deployments (http://192.168.x.x on a LAN) have no clipboard
  // API, so failure is normal there and needs an honest fallback.
  const copy = () => {
    const mark = () => {
      setCopied(true)
      setFailed(false)
      setTimeout(() => setCopied(false), 2000)
    }
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(text).then(mark, () => (fallbackCopy(text) ? mark() : setFailed(true)))
      return
    }
    if (fallbackCopy(text)) {
      mark()
    } else {
      setFailed(true)
    }
  }
  return (
    <div>
      <div className="mb-1.5 text-xs font-medium text-gray-500">{label}</div>
      <div
        className={cn(
          'flex flex-col gap-2 rounded-lg border border-gray-200 bg-gray-50 p-3 sm:flex-row sm:items-center dark:border-gray-800 dark:bg-gray-800/80',
          emphasize && 'bg-white dark:bg-gray-900',
        )}
      >
        <code
          className={cn(
            'min-w-0 flex-1 select-all overflow-x-auto break-all font-mono text-gray-900 dark:text-white',
            emphasize
              ? 'text-lg font-semibold tracking-[0.18em] sm:text-xl'
              : compact
                ? 'text-xs whitespace-pre-wrap sm:whitespace-nowrap sm:break-normal'
                : 'text-sm',
          )}
        >
          {text}
        </code>
        <Button className="shrink-0 self-end sm:self-auto" onClick={copy}>
          {copied ? 'Copied' : action}
        </Button>
      </div>
      {failed && (
        <p className="mt-1 text-xs text-error-600">Copy failed — select the text manually</p>
      )}
    </div>
  )
}

// fallbackCopy uses the legacy execCommand path, which still works where the
// async clipboard API is unavailable (non-secure origins).
function fallbackCopy(text: string): boolean {
  const area = document.createElement('textarea')
  area.value = text
  area.style.position = 'fixed'
  area.style.opacity = '0'
  document.body.appendChild(area)
  area.select()
  let ok
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  document.body.removeChild(area)
  return ok
}
