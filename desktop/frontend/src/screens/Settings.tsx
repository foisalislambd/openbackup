import { useEffect, useState } from 'react'

import { api } from '../lib/bridge'
import { parseRate, rate } from '../lib/format'
import { useAction, useAsync } from '../lib/use-status'
import type { Overview, Settings as SettingsData } from '../lib/types'
import { Button, Card, Field, Input, Notice, Toggle } from '../components/ui'

/** Settings holds the few things worth changing.
 *
 *  The defaults are meant to be right, so this screen is short: limits that keep
 *  the agent out of the user's way, and encryption, which is the one decision that
 *  cannot be undone. */
export function Settings({ status }: { status: Overview }) {
  const loaded = useAsync(() => api.settings(), [])
  const action = useAction()
  const [draft, setDraft] = useState<SettingsData | null>(null)
  const [uploadText, setUploadText] = useState('')
  const [uploadError, setUploadError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    if (loaded.data && !draft) {
      setDraft(loaded.data)
      setUploadText(loaded.data.upload_bytes_per_sec ? rate(loaded.data.upload_bytes_per_sec) : '')
    }
  }, [loaded.data, draft])

  if (!draft) {
    return <p className="text-sm text-ink-muted">Loading settings...</p>
  }

  const save = (next: SettingsData) => {
    setDraft(next)
    setSaved(false)
    void action.run(
      async () => {
        const applied = await api.updateSettings(next)
        setDraft(applied)
      },
      () => setSaved(true),
    )
  }

  const commitUpload = () => {
    const parsed = parseRate(uploadText)
    if (parsed === null) {
      setUploadError('Try something like 5MB, 500KB, or leave it empty for no limit.')
      return
    }
    setUploadError('')
    save({ ...draft, upload_bytes_per_sec: parsed })
  }

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-5">
      {action.error && <Notice tone="bad" title="Could not save">{action.error}</Notice>}
      {saved && !action.error && (
        <Notice tone="good" title="Saved">
          The change is already in effect; there is no need to restart anything.
        </Notice>
      )}

      <Card
        title="Staying out of your way"
        description="These limits decide when the agent works. They apply to the background service immediately."
      >
        <Field
          label="Upload speed limit"
          hint="Leave empty for no limit. Useful on a slow connection or when someone else is using it."
          error={uploadError}
        >
          <Input
            value={uploadText}
            onChange={(event) => setUploadText(event.target.value)}
            onBlur={commitUpload}
            onKeyDown={(event) => {
              if (event.key === 'Enter') commitUpload()
            }}
            placeholder="No limit"
            spellCheck={false}
          />
        </Field>

        <div className="mt-4">
          <Field
            label={`Pause while the computer is busier than ${Math.round(draft.max_cpu_percent)}%`}
            hint="Backups wait rather than compete with what you are doing."
          >
            <input
              type="range"
              min={10}
              max={100}
              step={5}
              value={draft.max_cpu_percent}
              onChange={(event) => setDraft({ ...draft, max_cpu_percent: Number(event.target.value) })}
              onMouseUp={() => save(draft)}
              onKeyUp={() => save(draft)}
              className="mt-2 w-full accent-brand"
            />
          </Field>
        </div>

        <div className="mt-2 divide-y divide-border-subtle">
          <Toggle
            checked={draft.pause_on_metered}
            onChange={(value) => save({ ...draft, pause_on_metered: value })}
            label="Pause on metered connections"
            description="Avoids using up a mobile hotspot or a capped connection."
          />
          <Toggle
            checked={draft.pause_on_battery}
            onChange={(value) => save({ ...draft, pause_on_battery: value })}
            label="Pause while on battery"
            description="Off by default: a laptop that is never plugged in would otherwise never be backed up."
          />
          <Toggle
            checked={draft.pause_while_fullscreen}
            onChange={(value) => save({ ...draft, pause_while_fullscreen: value })}
            label="Pause during games and presentations"
            description="Stops backups while an app is running full screen."
          />
        </div>
      </Card>

      <Encryption encrypted={draft.encrypted} hasBackups={status.snapshot_count > 0} />

      <Account serverURL={status.server_url} deviceName={status.device_name} />

      <About />
    </div>
  )
}

function Account({ serverURL, deviceName }: { serverURL: string; deviceName: string }) {
  const action = useAction()
  const [confirming, setConfirming] = useState(false)

  return (
    <Card
      title="This device"
      description="Log out clears the connection on this computer. Your backups stay on the server until you delete the device in the dashboard."
    >
      <dl className="mb-4 space-y-2 text-sm">
        <div className="flex justify-between gap-4">
          <dt className="text-ink-muted">Device</dt>
          <dd className="selectable truncate text-ink">{deviceName || '—'}</dd>
        </div>
        <div className="flex justify-between gap-4">
          <dt className="text-ink-muted">Server</dt>
          <dd className="selectable truncate text-ink" title={serverURL}>
            {serverURL.replace(/^https?:\/\//, '') || '—'}
          </dd>
        </div>
      </dl>

      {action.error && <Notice tone="bad">{action.error}</Notice>}

      {!confirming ? (
        <Button tone="quiet" className="self-start" onClick={() => setConfirming(true)}>
          Log out
        </Button>
      ) : (
        <div className="flex flex-col gap-3">
          <Notice tone="warn" title="Log out of this device?">
            You will need a new connection code to back up from here again. If encryption is on,
            keep your recovery code somewhere safe — it is removed from this computer.
          </Notice>
          <div className="flex flex-wrap gap-2">
            <Button
              tone="danger"
              busy={action.busy}
              onClick={() =>
                action.run(async () => {
                  await api.disconnect()
                  // Status polling will flip the window back to onboarding.
                })
              }
            >
              Log out
            </Button>
            <Button tone="quiet" disabled={action.busy} onClick={() => setConfirming(false)}>
              Cancel
            </Button>
          </div>
        </div>
      )}
    </Card>
  )
}

function Encryption({ encrypted, hasBackups }: { encrypted: boolean; hasBackups: boolean }) {
  const action = useAction()
  const [code, setCode] = useState('')
  const [revealed, setRevealed] = useState(false)
  const [warning, setWarning] = useState('')

  if (encrypted) {
    return (
      <Card
        title="End-to-end encryption is on"
        description="Your files are encrypted on this device before they are uploaded. The server cannot read them."
        actions={
          !revealed && (
            <Button
              busy={action.busy}
              onClick={() =>
                action.run(async () => {
                  setCode(await api.recoveryCode())
                  setRevealed(true)
                })
              }
            >
              Show recovery code
            </Button>
          )
        }
      >
        {action.error && <Notice tone="bad">{action.error}</Notice>}
        {revealed && (
          <div>
            <code className="selectable block rounded-md bg-surface-muted px-3 py-2.5 font-mono text-sm text-ink">
              {code}
            </code>
            <p className="mt-2 text-xs text-ink-muted">
              Keep this somewhere other than this computer. Without it, a lost device means lost
              backups - by design, since the server has no copy.
            </p>
          </div>
        )}
      </Card>
    )
  }

  return (
    <Card
      title="End-to-end encryption is off"
      description="Your data is compressed and sent over TLS, but the server can read it."
    >
      {hasBackups ? (
        <Notice tone="info" title="This cannot be turned on now">
          This device has already uploaded unencrypted data. Mixing the two would mean some backups
          are readable and some are not. To switch, remove this device in the dashboard and connect
          it again with encryption on.
        </Notice>
      ) : (
        <div className="flex flex-col gap-3">
          <p className="text-sm text-ink-muted">
            Turning it on generates a recovery code that only you hold. Losing that code means losing
            access to your backups, so it has to be written down.
          </p>
          {action.error && <Notice tone="bad">{action.error}</Notice>}
          {warning && (
            <Notice tone="warn" title="Encryption is on for future backups">
              {warning}
            </Notice>
          )}
          {code ? (
            <div>
              <code className="selectable block rounded-md bg-surface-muted px-3 py-2.5 font-mono text-sm text-ink">
                {code}
              </code>
              <p className="mt-2 text-xs text-ink-muted">Write this down before you close the app.</p>
            </div>
          ) : (
            <Button
              tone="primary"
              busy={action.busy}
              className="self-start"
              onClick={() =>
                action.run(async () => {
                  try {
                    setCode(await api.enableEncryption(''))
                  } catch (err) {
                    // A restart requirement arrives as an error alongside the code,
                    // so it is shown as a warning rather than a failure.
                    const text = String(err)
                    if (text.includes('restart')) {
                      setWarning(text)
                      setCode(await api.recoveryCode())
                      return
                    }
                    throw err
                  }
                })
              }
            >
              Turn on encryption
            </Button>
          )}
        </div>
      )}
    </Card>
  )
}

function About() {
  const info = useAsync(() => api.info(), [])
  const action = useAction()

  return (
    <Card title="About this app">
      <dl className="space-y-2 text-sm">
        <div className="flex justify-between gap-4">
          <dt className="text-ink-muted">Version</dt>
          <dd className="selectable text-ink">{info.data?.version ?? '-'}</dd>
        </div>
        <div className="flex justify-between gap-4">
          <dt className="text-ink-muted">Settings file</dt>
          <dd className="selectable truncate text-ink" title={info.data?.config_path}>
            {info.data?.config_path ?? '-'}
          </dd>
        </div>
      </dl>
      <div className="mt-4 flex flex-wrap gap-2">
        <Button onClick={() => action.run(api.openLogFolder)}>Open log folder</Button>
        <Button onClick={() => action.run(api.openDashboard)}>Open web dashboard</Button>
        <Button tone="quiet" onClick={() => action.run(api.minimiseToTray)}>
          Hide to the tray
        </Button>
      </div>
      <p className="mt-3 text-xs text-ink-muted">
        Closing this window does not stop backups: they run as a background service.
      </p>
    </Card>
  )
}
