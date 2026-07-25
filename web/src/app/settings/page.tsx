'use client'

/**
 * Settings covers the handful of choices that genuinely need making: how long to
 * keep backups, how much space to allow, and how fast agents may upload. It also
 * publishes the exclusion rules, because "what exactly are you not backing up?"
 * deserves an answer in the product, not in a wiki.
 */

import { useEffect, useState } from 'react'
import { api, type IgnoreRules, type Me, type Settings } from '@/lib/api'
import { bytes } from '@/lib/format'
import { Button, Card, ErrorNote, Field, inputClass, Spinner } from '@/components/ui'

export default function SettingsPage() {
  const [settings, setSettings] = useState<Settings>()
  const [me, setMe] = useState<Me>()
  const [rules, setRules] = useState<IgnoreRules>()
  const [error, setError] = useState<string>()
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const load = async () => {
      try {
        const [s, m, r] = await Promise.all([api.settings(), api.me(), api.ignoreRules()])
        setSettings(s)
        setMe(m)
        setRules(r)
      } catch (err) {
        setError(err instanceof Error ? err.message : 'Could not load settings')
      }
    }
    void load()
  }, [])

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (!settings || !me) return <Spinner />

  const save = async (changes: Partial<Settings>) => {
    setBusy(true)
    setError(undefined)
    try {
      setSettings(await api.updateSettings(changes))
      setSaved(true)
      setTimeout(() => setSaved(false), 2500)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not save')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-6">
      <Card title="Account">
        <div className="space-y-1 text-sm">
          <div>
            <span className="text-[var(--color-ink-muted)]">Signed in as </span>
            {me.email}
          </div>
        </div>
        <div className="mt-4">
          <PasswordChange />
        </div>
      </Card>

      <Card title="Backup policy" action={saved ? <span className="text-xs text-[var(--color-good)]">Saved</span> : null}>
        <div className="grid gap-5 sm:grid-cols-2">
          <Field
            label="Keep backups for"
            hint="Older backups are deleted automatically. The newest backup of every device is always kept, whatever this says."
          >
            <select
              className={inputClass}
              value={settings.retention_days}
              disabled={busy}
              onChange={(e) => save({ retention_days: Number(e.target.value) })}
            >
              <option value={7}>7 days</option>
              <option value={30}>30 days</option>
              <option value={90}>90 days</option>
              <option value={365}>1 year</option>
              <option value={0}>Forever</option>
            </select>
          </Field>

          <Field label="Storage limit" hint="Uploads stop when this is reached. Leave empty for no limit.">
            <input
              className={inputClass}
              type="text"
              defaultValue={settings.quota_bytes > 0 ? String(Math.round(settings.quota_bytes / 1024 ** 3)) : ''}
              placeholder="unlimited"
              disabled={busy}
              onBlur={(e) => {
                const value = e.target.value.trim()
                const gb = value === '' ? 0 : Number(value)
                if (Number.isNaN(gb)) return
                save({ quota_bytes: Math.round(gb * 1024 ** 3) })
              }}
            />
            <span className="mt-1 block text-xs text-[var(--color-ink-muted)]">
              In gigabytes. Currently {settings.quota_bytes > 0 ? bytes(settings.quota_bytes) : 'unlimited'}.
            </span>
          </Field>

          <Field
            label="Maximum upload speed"
            hint="Applies to every device. Useful on a slow connection where a backup would otherwise saturate the line."
          >
            <input
              className={inputClass}
              type="text"
              defaultValue={
                settings.max_upload_bytes_per_sec > 0
                  ? String(Math.round(settings.max_upload_bytes_per_sec / 1024 / 1024))
                  : ''
              }
              placeholder="unlimited"
              disabled={busy}
              onBlur={(e) => {
                const value = e.target.value.trim()
                const mb = value === '' ? 0 : Number(value)
                if (Number.isNaN(mb)) return
                save({ max_upload_bytes_per_sec: Math.round(mb * 1024 * 1024) })
              }}
            />
            <span className="mt-1 block text-xs text-[var(--color-ink-muted)]">
              In megabytes per second. Currently{' '}
              {settings.max_upload_bytes_per_sec > 0 ? `${bytes(settings.max_upload_bytes_per_sec)}/s` : 'unlimited'}.
            </span>
          </Field>

          <Field
            label="Require end-to-end encryption"
            hint="Refuse data from devices that have not turned encryption on. Turn this on before connecting your first device."
          >
            <label className="mt-2 flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={settings.require_encryption}
                disabled={busy}
                onChange={(e) => save({ require_encryption: e.target.checked })}
              />
              Only accept encrypted backups
            </label>
          </Field>
        </div>
      </Card>

      {rules && <IgnoreRulesCard rules={rules} />}
    </div>
  )
}

function PasswordChange() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [state, setState] = useState<{ ok?: boolean; message?: string }>({})
  const [busy, setBusy] = useState(false)

  return (
    <form
      className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
      onSubmit={async (event) => {
        event.preventDefault()
        setBusy(true)
        setState({})
        try {
          await api.changePassword(current, next)
          setState({ ok: true, message: 'Password changed' })
          setCurrent('')
          setNext('')
        } catch (err) {
          setState({ ok: false, message: err instanceof Error ? err.message : 'Could not change the password' })
        } finally {
          setBusy(false)
        }
      }}
    >
      <Field label="Current password">
        <input
          className={inputClass}
          type="password"
          autoComplete="current-password"
          value={current}
          onChange={(e) => setCurrent(e.target.value)}
          required
        />
      </Field>
      <Field label="New password">
        <input
          className={inputClass}
          type="password"
          autoComplete="new-password"
          minLength={12}
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
        />
      </Field>
      <Button type="submit" disabled={busy}>
        {busy ? 'Saving…' : 'Change'}
      </Button>
      {state.message && (
        <p
          className="text-xs sm:col-span-3"
          style={{ color: state.ok ? 'var(--color-good)' : 'var(--color-bad)' }}
        >
          {state.message}
        </p>
      )}
    </form>
  )
}

function IgnoreRulesCard({ rules }: { rules: IgnoreRules }) {
  const [open, setOpen] = useState(false)
  const categoryTitles: Record<string, string> = {
    system: 'Operating system files',
    junk: 'Junk files',
    cache: 'Caches',
    developer: 'Developer dependencies and build output',
    virtualisation: 'Virtual machine disks',
    ephemeral: 'Temporary files',
  }

  return (
    <Card
      title="What is not backed up"
      action={
        <Button variant="ghost" onClick={() => setOpen((v) => !v)}>
          {open ? 'Hide details' : 'Show details'}
        </Button>
      }
    >
      <p className="text-sm text-[var(--color-ink-muted)]">
        OpenBackup only backs up your own files. Operating system files, installed programs and anything that can
        be regenerated are skipped, which is why a backup is usually far smaller than the disk.
      </p>
      {open && (
        <div className="mt-4 space-y-5">
          {Object.entries(rules.categories).map(([category, list]) => (
            <div key={category}>
              <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--color-ink-muted)]">
                {categoryTitles[category] ?? category}
              </h3>
              <ul className="mt-2 space-y-1">
                {list.map((rule) => (
                  <li key={rule.pattern} className="flex flex-wrap items-baseline gap-x-2 text-sm">
                    <code className="rounded bg-[var(--color-surface-muted)] px-1.5 py-0.5 font-mono text-xs">
                      {rule.pattern}
                    </code>
                    <span className="text-xs text-[var(--color-ink-muted)]">{rule.reason}</span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wide text-[var(--color-ink-muted)]">
              Project detection
            </h3>
            <p className="mt-2 text-sm text-[var(--color-ink-muted)]">
              Dependency and build folders are only skipped inside a folder that contains one of these files, so a
              folder called <code className="font-mono text-xs">build</code> in your photo library is still backed
              up. Source code is always backed up.
            </p>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {rules.project_markers.map((marker) => (
                <code
                  key={marker}
                  className="rounded bg-[var(--color-surface-muted)] px-1.5 py-0.5 font-mono text-xs"
                >
                  {marker}
                </code>
              ))}
            </div>
          </div>
        </div>
      )}
    </Card>
  )
}
