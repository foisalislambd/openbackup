/**
 * Settings covers the handful of choices that genuinely need making: how long to
 * keep backups, how much space to allow, and how fast agents may upload. It also
 * publishes the exclusion rules, because "what exactly are you not backing up?"
 * deserves an answer inside the product rather than in a wiki.
 */

import { useEffect, useRef, useState } from 'react'
import {
  api,
  type DashboardSession,
  type IgnoreRules,
  type IntegrityReport,
  type Me,
  type Settings,
} from '@/lib/api'
import { bytes } from '@/lib/format'
import { message, useLoader } from '@/lib/use-loader'
import { Button, Card, ErrorNote, Field, inputClass } from '@/components/ui'
import { useToast } from '@/lib/toast-context'
import { absolute } from '@/lib/format'
import { SettingsSkeleton } from '@/components/skeleton'

type Data = {
  settings: Settings
  me: Me
  rules: IgnoreRules
  integrity: IntegrityReport
  sessions: DashboardSession[]
}

export default function SettingsPage() {
  const { data, error, loading, reload } = useLoader<Data>(async () => {
    const [settings, me, rules, integrity, sessions] = await Promise.all([
      api.settings(),
      api.me(),
      api.ignoreRules(),
      api.integrity(),
      api.sessions(),
    ])
    return { settings, me, rules, integrity, sessions }
  })

  if (error) return <ErrorNote>{error}</ErrorNote>
  if (loading || !data) return <SettingsSkeleton />

  return (
    <div className="space-y-6">
      <Card title="Account">
        <div className="text-sm">
          <span className="text-gray-500">Signed in as </span>
          {data.me.email}
        </div>
        <div className="mt-4">
          <PasswordChange />
        </div>
      </Card>

      <PolicyCard initial={data.settings} />

      <IntegrityCard initial={data.integrity} onChanged={reload} />

      <SessionsCard sessions={data.sessions} onChanged={reload} />

      <IgnoreRulesCard rules={data.rules} />
    </div>
  )
}

function PolicyCard({ initial }: { initial: Settings }) {
  const [settings, setSettings] = useState(initial)
  const [state, setState] = useState<{ busy?: boolean; saved?: boolean; error?: string }>({})
  // Track the saved-notice timer so a second save cannot be clobbered by the
  // first one's cleanup: an unmanaged setTimeout would re-enable the controls
  // mid-request and let two PUTs race.
  const savedTimer = useRef<number | undefined>(undefined)
  const toast = useToast()
  useEffect(() => () => window.clearTimeout(savedTimer.current), [])

  const save = (changes: Partial<Settings>) => {
    window.clearTimeout(savedTimer.current)
    setState({ busy: true })
    api.updateSettings(changes).then(
      (updated) => {
        setSettings(updated)
        setState({ saved: true })
        toast.push('Settings saved')
        savedTimer.current = window.setTimeout(() => setState((s) => ({ ...s, saved: false })), 2500)
      },
      (err: unknown) => setState({ error: message(err, 'Could not save') }),
    )
  }

  const gigabytes = (n: number) => (n > 0 ? String(Math.round(n / 1024 ** 3)) : '')

  return (
    <Card
      title="Backup policy"
      action={state.saved ? <span className="text-xs text-success-600">Saved</span> : null}
    >
      {state.error && (
        <div className="mb-4">
          <ErrorNote>{state.error}</ErrorNote>
        </div>
      )}
      <div className="grid gap-5 sm:grid-cols-2">
        <Field
          label="Keep backups for"
          hint="Oldest copies are removed. Latest per device is always kept."
        >
          <select
            className={inputClass}
            value={settings.retention_days}
            disabled={state.busy}
            onChange={(e) => save({ retention_days: Number(e.target.value) })}
          >
            {![7, 30, 90, 365, 0].includes(settings.retention_days) && (
              <option value={settings.retention_days}>{settings.retention_days} days</option>
            )}
            <option value={7}>7 days</option>
            <option value={30}>30 days</option>
            <option value={90}>90 days</option>
            <option value={365}>1 year</option>
            <option value={0}>Forever</option>
          </select>
        </Field>

        <Field
          label="Which backups to keep"
          hint="Inside that window. “Every backup” is the original behaviour. Thinning keeps the newest copy of each day, week, and month."
        >
          <select
            className={inputClass}
            disabled={state.busy}
            value={historyPreset(settings)}
            onChange={(e) => {
              const preset = e.target.value
              if (preset === 'all') save({ keep_daily: 0, keep_weekly: 0, keep_monthly: 0 })
              if (preset === 'balanced') save({ keep_daily: 14, keep_weekly: 8, keep_monthly: 12 })
              if (preset === 'custom') {
                save({
                  keep_daily: settings.keep_daily || 7,
                  keep_weekly: settings.keep_weekly || 4,
                  keep_monthly: settings.keep_monthly || 6,
                })
              }
            }}
          >
            <option value="all">Every backup in the window</option>
            <option value="balanced">14 daily, 8 weekly, 12 monthly</option>
            <option value="custom">Custom counts</option>
          </select>
        </Field>

        {historyPreset(settings) === 'custom' && (
          <>
            <CountField
              label="Daily copies"
              value={settings.keep_daily}
              disabled={!!state.busy}
              onCommit={(keep_daily) => save({ keep_daily })}
            />
            <CountField
              label="Weekly copies"
              value={settings.keep_weekly}
              disabled={!!state.busy}
              onCommit={(keep_weekly) => save({ keep_weekly })}
            />
            <CountField
              label="Monthly copies"
              value={settings.keep_monthly}
              disabled={!!state.busy}
              onCommit={(keep_monthly) => save({ keep_monthly })}
            />
          </>
        )}

        <Field
          label="Warn when a device is quiet"
          hint="Overview shows an alert if a computer has not finished a backup in this time. Off keeps the status green until an error."
        >
          <select
            className={inputClass}
            value={settings.alert_after_hours}
            disabled={state.busy}
            onChange={(e) => save({ alert_after_hours: Number(e.target.value) })}
          >
            {![24, 48, 72, 168, 0].includes(settings.alert_after_hours) && (
              <option value={settings.alert_after_hours}>{settings.alert_after_hours} hours</option>
            )}
            <option value={24}>24 hours</option>
            <option value={48}>2 days</option>
            <option value={72}>3 days</option>
            <option value={168}>7 days</option>
            <option value={0}>Off</option>
          </select>
        </Field>

        <Field label="Storage limit" hint="Uploads stop at this limit. Empty = unlimited.">
          <input
            className={inputClass}
            type="text"
            inputMode="numeric"
            defaultValue={gigabytes(settings.quota_bytes)}
            placeholder="unlimited"
            disabled={state.busy}
            onBlur={(e) => {
              const raw = e.target.value.trim()
              const gb = raw === '' ? 0 : Number(raw)
              if (Number.isNaN(gb) || gb < 0) return
              save({ quota_bytes: Math.round(gb * 1024 ** 3) })
            }}
          />
          <span className="mt-1 block text-xs text-gray-500">
            GB. Now {settings.quota_bytes > 0 ? bytes(settings.quota_bytes) : 'unlimited'}.
          </span>
        </Field>

        <Field label="Maximum upload speed" hint="Cap for all devices (MB/s). Empty = unlimited.">
          <input
            className={inputClass}
            type="text"
            inputMode="numeric"
            defaultValue={
              settings.max_upload_bytes_per_sec > 0
                ? String(Math.round(settings.max_upload_bytes_per_sec / 1024 / 1024))
                : ''
            }
            placeholder="unlimited"
            disabled={state.busy}
            onBlur={(e) => {
              const raw = e.target.value.trim()
              const mb = raw === '' ? 0 : Number(raw)
              if (Number.isNaN(mb) || mb < 0) return
              save({ max_upload_bytes_per_sec: Math.round(mb * 1024 * 1024) })
            }}
          />
          <span className="mt-1 block text-xs text-gray-500">
            Now{' '}
            {settings.max_upload_bytes_per_sec > 0 ? `${bytes(settings.max_upload_bytes_per_sec)}/s` : 'unlimited'}.
          </span>
        </Field>

        <Field
          label="Require end-to-end encryption"
          hint="New devices must encrypt files on the computer before upload. The website can still list folders, but locked files cannot be opened in the browser."
        >
          <label className="mt-2 flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={settings.require_encryption}
              disabled={state.busy}
              onChange={(e) => save({ require_encryption: e.target.checked })}
            />
            Only accept encrypted backups
          </label>
        </Field>
      </div>
    </Card>
  )
}

function historyPreset(settings: Settings): 'all' | 'balanced' | 'custom' {
  if (settings.keep_daily === 0 && settings.keep_weekly === 0 && settings.keep_monthly === 0) return 'all'
  if (settings.keep_daily === 14 && settings.keep_weekly === 8 && settings.keep_monthly === 12) return 'balanced'
  return 'custom'
}

function CountField({
  label,
  value,
  disabled,
  onCommit,
}: {
  label: string
  value: number
  disabled: boolean
  onCommit: (value: number) => void
}) {
  return (
    <Field label={label} hint="0 turns this bucket off.">
      <input
        className={inputClass}
        type="text"
        inputMode="numeric"
        defaultValue={String(value)}
        key={value}
        disabled={disabled}
        onBlur={(e) => {
          const n = Number(e.target.value.trim())
          if (Number.isNaN(n) || n < 0) return
          onCommit(Math.round(n))
        }}
      />
    </Field>
  )
}

function IntegrityCard({ initial, onChanged }: { initial: IntegrityReport; onChanged: () => void }) {
  const [report, setReport] = useState(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const toast = useToast()

  return (
    <Card
      title="Stored data check"
      action={
        <Button
          disabled={busy}
          onClick={() => {
            setBusy(true)
            setError('')
            api.runIntegrity().then(
              (next) => {
                setReport(next)
                setBusy(false)
                toast.push(next.failed ? 'Integrity check found a problem' : 'Sampled blocks look intact', next.failed ? 'bad' : 'ok')
                onChanged()
              },
              (err: unknown) => {
                setBusy(false)
                setError(message(err, 'Could not check stored data'))
              },
            )
          }}
        >
          {busy ? 'Checking…' : 'Check a sample'}
        </Button>
      }
    >
      {error && (
        <div className="mb-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
      {report.finished_at ? (
        <p className="text-sm text-gray-600 dark:text-gray-300">
          Last sample {absolute(report.finished_at)} checked {report.checked} block{report.checked === 1 ? '' : 's'}
          {report.failed ? ` and ${report.failed} failed.` : ' and they matched.'}
        </p>
      ) : (
        <p className="text-sm text-gray-500">
          No check has run yet. The server also samples stored blocks about once an hour. Encrypted blocks are
          checked for presence; the computer that holds the recovery code still verifies their contents on restore.
        </p>
      )}
      {report.detail && <p className="mt-2 text-xs text-error-500">{report.detail}</p>}
    </Card>
  )
}

function SessionsCard({ sessions, onChanged }: { sessions: DashboardSession[]; onChanged: () => void }) {
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  return (
    <Card title="Signed-in browsers">
      {error && (
        <div className="mb-3">
          <ErrorNote>{error}</ErrorNote>
        </div>
      )}
      <ul className="divide-y divide-gray-100 dark:divide-gray-800">
        {sessions.map((session) => (
          <li key={session.id} className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:justify-between">
            <div className="min-w-0">
              <div className="truncate text-sm font-medium">{session.user_agent || 'Unknown browser'}</div>
              <div className="text-xs text-gray-500">
                Signed in {absolute(session.created_at)}
                {session.current ? ' · this browser' : ''}
              </div>
            </div>
            {session.current ? (
              <span className="text-xs font-semibold text-gray-500">Current</span>
            ) : (
              <Button
                variant="danger"
                disabled={busy === session.id}
                onClick={() => {
                  setBusy(session.id)
                  setError('')
                  api.revokeSession(session.id).then(
                    () => {
                      setBusy('')
                      onChanged()
                    },
                    (err: unknown) => {
                      setBusy('')
                      setError(message(err, 'Could not sign that browser out'))
                    },
                  )
                }}
              >
                Sign out
              </Button>
            )}
          </li>
        ))}
      </ul>
    </Card>
  )
}

function PasswordChange() {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [state, setState] = useState<{ busy?: boolean; ok?: boolean; message?: string }>({})

  return (
    <form
      className="grid gap-3 sm:grid-cols-[1fr_1fr_auto] sm:items-end"
      onSubmit={(event) => {
        event.preventDefault()
        setState({ busy: true })
        api.changePassword(current, next).then(
          () => {
            setState({ ok: true, message: 'Password changed' })
            setCurrent('')
            setNext('')
          },
          (err: unknown) => setState({ ok: false, message: message(err, 'Could not change the password') }),
        )
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
          minLength={10}
          value={next}
          onChange={(e) => setNext(e.target.value)}
          required
        />
      </Field>
      <Button type="submit" disabled={state.busy}>
        {state.busy ? 'Saving…' : 'Change'}
      </Button>
      {state.message && (
        <p
          className={`text-xs sm:col-span-3 ${state.ok ? 'text-success-600' : 'text-error-500'}`}
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
      <p className="text-sm text-gray-500">
        Only your files are kept. OS, apps, and regenerable folders are skipped.
      </p>
      {open && (
        <div className="mt-4 space-y-5">
          {Object.entries(rules.categories).map(([category, list]) => (
            <div key={category}>
              <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">
                {categoryTitles[category] ?? category}
              </h3>
              <ul className="mt-2 space-y-1">
                {list.map((rule) => (
                  <li key={rule.pattern} className="flex flex-wrap items-baseline gap-x-2 text-sm">
                    <code className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs dark:bg-gray-800">
                      {rule.pattern}
                    </code>
                    <span className="text-xs text-gray-500">{rule.reason}</span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
          <div>
            <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">
              Project detection
            </h3>
            <p className="mt-2 text-sm text-gray-500">
              Build folders are skipped only inside real projects (markers below). Source code is kept.
            </p>
            <div className="mt-2 flex flex-wrap gap-1.5">
              {rules.project_markers.map((marker) => (
                <code key={marker} className="rounded bg-gray-100 px-1.5 py-0.5 font-mono text-xs dark:bg-gray-800">
                  {marker}
                </code>
              ))}
            </div>
          </div>
          {rules.max_file_size != null && rules.max_file_size > 0 && (
            <div>
              <h3 className="text-xs font-semibold uppercase tracking-wide text-gray-500">
                Large files
              </h3>
              <p className="mt-2 text-sm text-gray-500">
                Files over {bytes(rules.max_file_size)} are skipped.
              </p>
            </div>
          )}
        </div>
      )}
    </Card>
  )
}
