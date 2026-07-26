import { useState } from 'react'

import { api } from '../lib/bridge'
import { useAction } from '../lib/use-status'
import type { ConnectResult } from '../lib/types'
import { Button, Field, Input, Notice, Toggle } from '../components/ui'

/** Onboarding is the only screen a new user sees, so it asks for the least
 *  possible: where the server is, and the code from it. Folders are detected
 *  automatically and can be changed afterwards. */
export function Onboarding({
  onConnected,
  onConnectSuccess,
}: {
  onConnected: () => void
  onConnectSuccess?: () => void
}) {
  const [server, setServer] = useState('')
  const [code, setCode] = useState('')
  const [name, setName] = useState('')
  const [encrypt, setEncrypt] = useState(false)
  const [recoveryCode, setRecoveryCode] = useState('')
  const [joining, setJoining] = useState(false)
  const [result, setResult] = useState<ConnectResult | null>(null)
  const action = useAction()

  if (result) {
    return <Done result={result} onFinish={onConnected} />
  }

  const submit = () =>
    action.run(async () => {
      const connected = await api.connect({
        server_url: server,
        code,
        device_name: name,
        encrypt: encrypt || joining,
        recovery_code: joining ? recoveryCode : '',
      })
      onConnectSuccess?.()
      setResult(connected)
    })

  return (
    <div className="mx-auto flex h-full max-w-md flex-col justify-center gap-6 p-8">
      <header>
        <h1 className="text-xl font-semibold text-ink">Connect this device</h1>
        <p className="mt-1.5 text-sm text-ink-muted">
          Your files are backed up to your own server. Get an address and a code from its dashboard,
          then paste them here.
        </p>
      </header>

      <form
        className="flex flex-col gap-4"
        onSubmit={(event) => {
          event.preventDefault()
          void submit()
        }}
      >
        <Field label="Server address" hint="For example https://backup.example.com">
          <Input
            value={server}
            onChange={(event) => setServer(event.target.value)}
            placeholder="https://backup.example.com"
            autoFocus
            spellCheck={false}
          />
        </Field>

        <Field label="Connection code" hint="Created in the dashboard under Devices">
          <Input
            value={code}
            onChange={(event) => setCode(event.target.value.toUpperCase())}
            placeholder="ABCD-EFGH-JKLM"
            spellCheck={false}
            className="font-mono tracking-wide"
          />
        </Field>

        <Field label="Name for this device" hint="Leave blank to use this computer's name">
          <Input value={name} onChange={(event) => setName(event.target.value)} placeholder="" />
        </Field>

        <div className="rounded-lg border border-border-subtle px-3.5">
          <Toggle
            checked={encrypt || joining}
            disabled={joining}
            onChange={setEncrypt}
            label="Encrypt my backups end to end"
            description="Only this device can read them. You get a recovery code, and losing it means losing the backups. This cannot be turned on later."
          />
          {(encrypt || joining) && (
            <div className="border-t border-border-subtle py-3">
              <Toggle
                checked={joining}
                onChange={(value) => {
                  setJoining(value)
                  if (value) setEncrypt(true)
                }}
                label="Another device already uses encryption"
                description="Enter that account's recovery code so both devices share one key."
              />
              {joining && (
                <Input
                  value={recoveryCode}
                  onChange={(event) => setRecoveryCode(event.target.value)}
                  placeholder="Recovery code from your other device"
                  spellCheck={false}
                  className="font-mono"
                />
              )}
            </div>
          )}
        </div>

        {action.error && <Notice tone="bad" title="Could not connect">{action.error}</Notice>}

        <Button
          type="submit"
          tone="primary"
          busy={action.busy}
          disabled={!server.trim() || !code.trim() || (joining && !recoveryCode.trim())}
          className="mt-1 w-full py-2.5"
        >
          Connect and start backing up
        </Button>
      </form>
    </div>
  )
}

/** Done shows what was set up, and the recovery code, which is the one thing on
 *  this screen the user must not click past without reading. */
function Done({ result, onFinish }: { result: ConnectResult; onFinish: () => void }) {
  const [copied, setCopied] = useState(false)
  const [acknowledged, setAcknowledged] = useState(!result.recovery_code)
  const enabled = result.folders.filter((folder) => folder.enabled)

  return (
    <div className="mx-auto flex h-full max-w-md flex-col justify-center gap-5 p-8">
      <header>
        <h1 className="text-xl font-semibold text-ink">This device is connected</h1>
        <p className="mt-1.5 text-sm text-ink-muted">
          Backups run in the background from now on. The first one may take a while.
        </p>
      </header>

      {result.recovery_code && (
        <div className="rounded-lg border border-warn/40 bg-warn/10 p-4">
          <p className="text-sm font-medium text-ink">Write this recovery code down</p>
          <p className="mt-1 text-sm text-ink-muted">
            It is the only way to read your backups if this device is lost. The server does not have
            a copy, and nobody can recover it for you.
          </p>
          <code className="selectable mt-3 block rounded-md bg-surface px-3 py-2.5 font-mono text-sm text-ink">
            {result.recovery_code}
          </code>
          <div className="mt-3 flex items-center gap-2">
            <Button
              onClick={() => {
                void navigator.clipboard.writeText(result.recovery_code ?? '')
                setCopied(true)
              }}
            >
              {copied ? 'Copied' : 'Copy'}
            </Button>
            <label className="flex items-center gap-2 text-sm text-ink-muted">
              <input
                type="checkbox"
                checked={acknowledged}
                onChange={(event) => setAcknowledged(event.target.checked)}
              />
              I have saved it somewhere safe
            </label>
          </div>
        </div>
      )}

      <div className="rounded-lg border border-border-subtle p-4">
        <p className="text-sm font-medium text-ink">
          Backing up {enabled.length} folder{enabled.length === 1 ? '' : 's'}
        </p>
        <ul className="mt-2 space-y-1 text-sm text-ink-muted">
          {enabled.slice(0, 6).map((folder) => (
            <li key={folder.path} className="truncate" title={folder.path}>
              {folder.label}
            </li>
          ))}
        </ul>
        <p className="mt-2 text-xs text-ink-muted">
          System files, caches and build folders are skipped automatically. You can add or remove
          folders at any time.
        </p>
      </div>

      <Button tone="primary" disabled={!acknowledged} onClick={onFinish} className="w-full py-2.5">
        Done
      </Button>
    </div>
  )
}
