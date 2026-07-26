/** Presentational building blocks for the dashboard. */

import type { ReactNode } from 'react'

export function PageTitle({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <div className="min-w-0">
      <h1 className="truncate text-2xl font-semibold tracking-tight text-[var(--color-ink)]">{title}</h1>
      {subtitle && <p className="mt-0.5 truncate text-sm text-[var(--color-ink-muted)]">{subtitle}</p>}
    </div>
  )
}

export function Card({
  title,
  action,
  children,
  className = '',
}: {
  title?: string
  action?: ReactNode
  children: ReactNode
  className?: string
}) {
  return (
    <section className={`panel ${className}`}>
      {(title || action) && (
        <header className="flex items-center justify-between gap-3 border-b border-[var(--color-border-subtle)] px-5 py-3.5">
          {title && <h2 className="text-sm font-semibold tracking-tight">{title}</h2>}
          {action}
        </header>
      )}
      <div className="px-5 py-4">{children}</div>
    </section>
  )
}

export function Stat({
  label,
  value,
  hint,
  tone = 'neutral',
}: {
  label: string
  value: string
  hint?: string
  tone?: 'neutral' | 'good' | 'warn' | 'bad'
}) {
  const toneColor = {
    neutral: 'var(--color-ink)',
    good: 'var(--color-good)',
    warn: 'var(--color-warn)',
    bad: 'var(--color-bad)',
  }[tone]

  return (
    <div className="panel px-5 py-4">
      <div className="text-[0.7rem] font-semibold uppercase tracking-[0.08em] text-[var(--color-ink-muted)]">
        {label}
      </div>
      <div className="tabular mt-2 text-[1.65rem] font-semibold tracking-tight" style={{ color: toneColor }}>
        {value}
      </div>
      {hint && <div className="mt-1 text-xs text-[var(--color-ink-muted)]">{hint}</div>}
    </div>
  )
}

export function Badge({
  children,
  tone = 'neutral',
}: {
  children: ReactNode
  tone?: 'neutral' | 'good' | 'warn' | 'bad' | 'brand'
}) {
  const styles: Record<string, string> = {
    neutral: 'bg-[var(--color-surface-muted)] text-[var(--color-ink-muted)]',
    good: 'bg-[color-mix(in_oklch,var(--color-good)_14%,transparent)] text-[var(--color-good)]',
    warn: 'bg-[color-mix(in_oklch,var(--color-warn)_16%,transparent)] text-[var(--color-warn)]',
    bad: 'bg-[color-mix(in_oklch,var(--color-bad)_14%,transparent)] text-[var(--color-bad)]',
    brand: 'bg-[var(--color-brand-soft)] text-[var(--color-brand-strong)]',
  }
  return (
    <span className={`inline-flex items-center rounded-md px-2 py-0.5 text-[0.7rem] font-semibold ${styles[tone]}`}>
      {children}
    </span>
  )
}

export function Button({
  children,
  onClick,
  variant = 'secondary',
  disabled,
  type = 'button',
  href,
  className = '',
}: {
  children: ReactNode
  onClick?: () => void
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost'
  disabled?: boolean
  type?: 'button' | 'submit'
  href?: string
  className?: string
}) {
  const styles: Record<string, string> = {
    primary: 'bg-[var(--color-brand)] text-white hover:bg-[var(--color-brand-strong)]',
    secondary:
      'border border-[var(--color-border-subtle)] bg-[var(--color-surface)] hover:bg-[var(--color-surface-muted)]',
    danger:
      'border border-[var(--color-bad)] text-[var(--color-bad)] hover:bg-[color-mix(in_oklch,var(--color-bad)_10%,transparent)]',
    ghost: 'text-[var(--color-ink-muted)] hover:bg-[var(--color-surface-muted)] hover:text-[var(--color-ink)]',
  }
  const base = `inline-flex items-center justify-center gap-1.5 rounded-xl px-3 py-1.5 text-sm font-semibold transition disabled:cursor-not-allowed disabled:opacity-50 ${styles[variant]} ${className}`

  if (href) {
    return (
      <a className={base} href={href}>
        {children}
      </a>
    )
  }
  return (
    <button className={base} onClick={onClick} disabled={disabled} type={type}>
      {children}
    </button>
  )
}

export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className="block">
      <span className="text-sm font-semibold">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-[var(--color-ink-muted)]">{hint}</span>}
    </label>
  )
}

export const inputClass =
  'mt-1.5 w-full rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)] px-3.5 py-2.5 text-sm outline-none transition focus:border-[var(--color-brand)] focus:ring-2 focus:ring-[var(--color-brand-soft)]'

export function Empty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="px-4 py-14 text-center">
      <div className="mx-auto mb-3 grid size-14 place-items-center rounded-2xl bg-[var(--color-brand-soft)] text-[var(--color-brand)]">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
          <path d="M4 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" />
        </svg>
      </div>
      <p className="text-sm font-semibold">{title}</p>
      {hint && <p className="mx-auto mt-1 max-w-sm text-sm text-[var(--color-ink-muted)]">{hint}</p>}
    </div>
  )
}

export function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-xl border border-[var(--color-bad)] bg-[color-mix(in_oklch,var(--color-bad)_8%,transparent)] px-3.5 py-2.5 text-sm text-[var(--color-bad)]">
      {children}
    </div>
  )
}

export function Meter({ value, max, tone }: { value: number; max: number; tone?: 'good' | 'warn' | 'bad' }) {
  const ratio = max > 0 ? Math.min(value / max, 1) : 0
  const auto = ratio > 0.9 ? 'bad' : ratio > 0.75 ? 'warn' : 'good'
  const color = `var(--color-${tone ?? auto})`
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-[var(--color-surface-muted)]">
      <div className="h-full rounded-full transition-all" style={{ width: `${ratio * 100}%`, background: color }} />
    </div>
  )
}

export function FolderGlyph({ large = false }: { large?: boolean }) {
  const size = large ? 40 : 28
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="var(--color-folder)" aria-hidden>
      <path d="M3 7.5A2.5 2.5 0 0 1 5.5 5H9l1.8 1.8H18.5A2.5 2.5 0 0 1 21 9.3v7.2A2.5 2.5 0 0 1 18.5 19h-13A2.5 2.5 0 0 1 3 16.5z" />
    </svg>
  )
}

export function FileGlyph() {
  return (
    <svg width="28" height="28" viewBox="0 0 24 24" fill="none" aria-hidden>
      <path
        d="M7 3.5h7l4 4V20a1.5 1.5 0 0 1-1.5 1.5h-9.5A1.5 1.5 0 0 1 5.5 20V5A1.5 1.5 0 0 1 7 3.5z"
        fill="var(--color-file)"
        opacity="0.9"
      />
      <path d="M14 3.5V8h4.5" fill="color-mix(in oklch, var(--color-surface) 40%, var(--color-file))" />
    </svg>
  )
}
