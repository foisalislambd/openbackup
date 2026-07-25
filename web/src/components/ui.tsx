'use client'

/** Small presentational building blocks shared across the dashboard. */

import type { ReactNode } from 'react'

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
    <section
      className={`rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)] ${className}`}
    >
      {(title || action) && (
        <header className="flex items-center justify-between gap-3 border-b border-[var(--color-border-subtle)] px-5 py-3.5">
          {title && <h2 className="text-sm font-semibold">{title}</h2>}
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
    <div className="rounded-xl border border-[var(--color-border-subtle)] bg-[var(--color-surface)] px-5 py-4">
      <div className="text-xs font-medium uppercase tracking-wide text-[var(--color-ink-muted)]">{label}</div>
      <div className="tabular mt-1.5 text-2xl font-semibold" style={{ color: toneColor }}>
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
    good: 'bg-[color-mix(in_oklch,var(--color-good)_16%,transparent)] text-[var(--color-good)]',
    warn: 'bg-[color-mix(in_oklch,var(--color-warn)_18%,transparent)] text-[var(--color-warn)]',
    bad: 'bg-[color-mix(in_oklch,var(--color-bad)_16%,transparent)] text-[var(--color-bad)]',
    brand: 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]',
  }
  return (
    <span className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium ${styles[tone]}`}>
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
    primary: 'bg-[var(--color-brand)] text-white hover:opacity-90',
    secondary:
      'border border-[var(--color-border-subtle)] bg-[var(--color-surface)] hover:bg-[var(--color-surface-muted)]',
    danger: 'border border-[var(--color-bad)] text-[var(--color-bad)] hover:bg-[color-mix(in_oklch,var(--color-bad)_10%,transparent)]',
    ghost: 'text-[var(--color-ink-muted)] hover:text-[var(--color-ink)]',
  }
  const base = `inline-flex items-center justify-center gap-1.5 rounded-lg px-3 py-1.5 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50 ${styles[variant]} ${className}`

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
      <span className="text-sm font-medium">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-[var(--color-ink-muted)]">{hint}</span>}
    </label>
  )
}

export const inputClass =
  'mt-1.5 w-full rounded-lg border border-[var(--color-border-subtle)] bg-[var(--color-surface)] px-3 py-2 text-sm outline-none focus:border-[var(--color-brand)]'

export function Empty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="py-10 text-center">
      <p className="text-sm font-medium">{title}</p>
      {hint && <p className="mt-1 text-sm text-[var(--color-ink-muted)]">{hint}</p>}
    </div>
  )
}

export function Spinner({ label = 'Loading' }: { label?: string }) {
  return (
    <div className="flex items-center gap-2 py-8 text-sm text-[var(--color-ink-muted)]">
      <span className="size-3.5 animate-spin rounded-full border-2 border-[var(--color-border-subtle)] border-t-[var(--color-brand)]" />
      {label}…
    </div>
  )
}

export function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-lg border border-[var(--color-bad)] bg-[color-mix(in_oklch,var(--color-bad)_8%,transparent)] px-3.5 py-2.5 text-sm text-[var(--color-bad)]">
      {children}
    </div>
  )
}

/** Meter draws a proportional bar, used for quota and disk usage. */
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
