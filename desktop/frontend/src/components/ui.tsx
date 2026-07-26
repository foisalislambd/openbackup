// The small set of building blocks every screen uses.
//
// There is no component library here on purpose: a backup app has one job, and a
// dozen hand-written pieces are easier to keep consistent (and far smaller to
// ship) than a dependency that brings its own opinions.

import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'

type Tone = 'default' | 'primary' | 'quiet' | 'danger'

const toneClasses: Record<Tone, string> = {
  primary: 'bg-brand text-white hover:opacity-90 disabled:opacity-50',
  default:
    'bg-surface text-ink border border-border-subtle hover:bg-surface-muted disabled:opacity-50',
  quiet: 'text-ink-muted hover:text-ink hover:bg-surface-muted disabled:opacity-50',
  danger: 'text-bad border border-bad/40 hover:bg-bad/10 disabled:opacity-50',
}

export function Button({
  tone = 'default',
  busy = false,
  children,
  className = '',
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { tone?: Tone; busy?: boolean }) {
  return (
    <button
      {...rest}
      disabled={rest.disabled || busy}
      className={`inline-flex items-center justify-center gap-2 rounded-lg px-3.5 py-2 text-sm font-medium
        transition-colors disabled:cursor-not-allowed ${toneClasses[tone]} ${className}`}
    >
      {busy && <Spinner />}
      {children}
    </button>
  )
}

export function Spinner({ className = '' }: { className?: string }) {
  return (
    <span
      aria-hidden
      className={`inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-current
        border-t-transparent ${className}`}
    />
  )
}

export function Card({
  title,
  description,
  actions,
  children,
  className = '',
}: {
  title?: ReactNode
  description?: ReactNode
  actions?: ReactNode
  children?: ReactNode
  className?: string
}) {
  return (
    <section
      className={`rounded-xl border border-border-subtle bg-surface p-5 ${className}`}
    >
      {(title || actions) && (
        <header className="mb-4 flex items-start justify-between gap-4">
          <div>
            {title && <h2 className="text-sm font-semibold text-ink">{title}</h2>}
            {description && <p className="mt-1 text-sm text-ink-muted">{description}</p>}
          </div>
          {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  )
}

export function Badge({
  tone = 'default',
  children,
}: {
  tone?: 'default' | 'good' | 'warn' | 'bad'
  children: ReactNode
}) {
  const tones = {
    default: 'bg-surface-muted text-ink-muted',
    good: 'bg-good/12 text-good',
    warn: 'bg-warn/15 text-warn',
    bad: 'bg-bad/12 text-bad',
  }
  return (
    <span className={`rounded-full px-2 py-0.5 text-xs font-medium ${tones[tone]}`}>
      {children}
    </span>
  )
}

export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string
  hint?: ReactNode
  error?: string
  children: ReactNode
}) {
  return (
    <label className="block">
      <span className="text-sm font-medium text-ink">{label}</span>
      {children}
      {error ? (
        <span className="mt-1 block text-xs text-bad">{error}</span>
      ) : (
        hint && <span className="mt-1 block text-xs text-ink-muted">{hint}</span>
      )}
    </label>
  )
}

export function Input({ className = '', ...rest }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      {...rest}
      className={`selectable mt-1.5 w-full rounded-lg border border-border-subtle bg-surface px-3 py-2
        text-sm text-ink outline-none placeholder:text-ink-muted/70 focus:border-brand ${className}`}
    />
  )
}

export function Toggle({
  checked,
  onChange,
  label,
  description,
  disabled = false,
}: {
  checked: boolean
  onChange: (value: boolean) => void
  label: string
  description?: string
  disabled?: boolean
}) {
  return (
    <div className="flex items-start justify-between gap-4 py-3">
      <div>
        <p className="text-sm text-ink">{label}</p>
        {description && <p className="mt-0.5 text-xs text-ink-muted">{description}</p>}
      </div>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        aria-label={label}
        disabled={disabled}
        onClick={() => onChange(!checked)}
        className={`mt-0.5 h-5 w-9 shrink-0 rounded-full transition-colors disabled:opacity-50
          ${checked ? 'bg-brand' : 'bg-border-subtle'}`}
      >
        <span
          className={`block h-4 w-4 translate-y-0.5 rounded-full bg-white transition-transform
            ${checked ? 'translate-x-4.5' : 'translate-x-0.5'}`}
        />
      </button>
    </div>
  )
}

/** Notice carries a message that is worth reading but is not a screen of its own:
 *  an error from an action, or an explanation of a state. */
export function Notice({
  tone = 'info',
  title,
  children,
  action,
}: {
  tone?: 'info' | 'warn' | 'bad' | 'good'
  title?: ReactNode
  children?: ReactNode
  action?: ReactNode
}) {
  const tones = {
    info: 'border-brand/30 bg-brand-soft/60 text-ink',
    good: 'border-good/30 bg-good/10 text-ink',
    warn: 'border-warn/40 bg-warn/10 text-ink',
    bad: 'border-bad/40 bg-bad/10 text-ink',
  }
  return (
    <div className={`flex items-start justify-between gap-4 rounded-lg border p-3.5 ${tones[tone]}`}>
      <div className="min-w-0">
        {title && <p className="text-sm font-medium">{title}</p>}
        {children && <p className="selectable mt-0.5 text-sm text-ink-muted">{children}</p>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-border-subtle p-8 text-center">
      <p className="text-sm font-medium text-ink">{title}</p>
      {children && <p className="mx-auto mt-1 max-w-sm text-sm text-ink-muted">{children}</p>}
    </div>
  )
}

/** Rows lays out a list with dividers, which is most of this app's content. */
export function Rows({ children }: { children: ReactNode }) {
  return <div className="divide-y divide-border-subtle">{children}</div>
}
