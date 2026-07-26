/** Presentational building blocks for the admin dashboard. */

import type { ReactNode } from 'react'
import { cn } from '@/lib/cn'

export function PageTitle({ title }: { title: string; subtitle?: string }) {
  return (
    <div className="min-w-0 leading-tight">
      <h1 className="truncate text-base font-semibold tracking-tight text-gray-900 dark:text-white sm:text-lg">
        {title}
      </h1>
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
    <section className={cn('admin-card overflow-hidden', className)}>
      {(title || action) && (
        <header className="flex flex-col gap-3 border-b border-gray-200 px-4 py-3.5 sm:flex-row sm:items-center sm:justify-between sm:px-5 dark:border-gray-800">
          {title && (
            <h2 className="shrink-0 text-sm font-semibold tracking-tight text-gray-900 dark:text-white">{title}</h2>
          )}
          {action && <div className="flex flex-wrap items-center gap-2 sm:justify-end">{action}</div>}
        </header>
      )}
      <div className="px-4 py-4 sm:px-5">{children}</div>
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
  const toneClass = {
    neutral: 'text-gray-900 dark:text-white',
    good: 'text-success-600 dark:text-success-500',
    warn: 'text-warning-500',
    bad: 'text-error-500',
  }[tone]

  return (
    <div className="admin-card px-4 py-4 sm:px-5">
      <div className="text-[0.7rem] font-semibold uppercase tracking-[0.08em] text-gray-500">{label}</div>
      <div className={cn('tabular mt-2 text-xl font-semibold tracking-tight sm:text-[1.65rem]', toneClass)}>{value}</div>
      {hint && <div className="mt-1 text-xs text-gray-500">{hint}</div>}
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
    neutral: 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300',
    good: 'bg-success-50 text-success-600 dark:bg-success-500/15 dark:text-success-500',
    warn: 'bg-warning-50 text-warning-500 dark:bg-warning-500/15',
    bad: 'bg-error-50 text-error-500 dark:bg-error-500/15',
    brand: 'bg-brand-50 text-brand-600 dark:bg-brand-500/15 dark:text-brand-300',
  }
  return (
    <span className={cn('inline-flex items-center rounded-md px-2 py-0.5 text-[0.7rem] font-semibold', styles[tone])}>
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
    primary: 'bg-brand-500 text-white shadow-theme-xs hover:bg-brand-600 disabled:bg-brand-300',
    secondary:
      'bg-white text-gray-700 ring-1 ring-inset ring-gray-300 hover:bg-gray-50 dark:bg-gray-800 dark:text-gray-400 dark:ring-gray-700 dark:hover:bg-white/[0.03]',
    danger: 'border border-error-500 text-error-500 hover:bg-error-50 dark:hover:bg-error-500/10',
    ghost: 'text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-white/5 dark:hover:text-gray-300',
  }
  const base = cn(
    'inline-flex items-center justify-center gap-1.5 rounded-lg px-3 py-2 text-sm font-semibold transition disabled:cursor-not-allowed disabled:opacity-50',
    styles[variant],
    className,
  )

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

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="text-sm font-semibold text-gray-800 dark:text-gray-200">{label}</span>
      {children}
      {hint && <span className="mt-1 block text-xs text-gray-500">{hint}</span>}
    </label>
  )
}

export const inputClass =
  'mt-1.5 h-11 w-full rounded-lg border border-gray-300 bg-transparent px-3.5 text-sm text-gray-800 shadow-theme-xs outline-none transition placeholder:text-gray-400 focus:border-brand-300 focus:ring-[3px] focus:ring-brand-500/10 dark:border-gray-700 dark:bg-gray-900 dark:text-white/90 dark:placeholder:text-white/30'

export function Empty({ title, hint }: { title: string; hint?: string }) {
  return (
    <div className="px-4 py-14 text-center">
      <div className="mx-auto mb-3 grid size-14 place-items-center rounded-2xl bg-brand-50 text-brand-500 dark:bg-brand-500/15">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
          <path d="M4 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" />
        </svg>
      </div>
      <p className="text-sm font-semibold text-gray-900 dark:text-white">{title}</p>
      {hint && <p className="mx-auto mt-1 max-w-sm text-sm text-gray-500">{hint}</p>}
    </div>
  )
}

export function ErrorNote({ children }: { children: ReactNode }) {
  return (
    <div className="rounded-xl border border-error-500/30 bg-error-50 px-3.5 py-2.5 text-sm text-error-500 dark:bg-error-500/10">
      {children}
    </div>
  )
}

export function Meter({ value, max, tone }: { value: number; max: number; tone?: 'good' | 'warn' | 'bad' }) {
  const ratio = max > 0 ? Math.min(value / max, 1) : 0
  const auto = ratio > 0.9 ? 'bad' : ratio > 0.75 ? 'warn' : 'good'
  const color = {
    good: 'bg-success-500',
    warn: 'bg-warning-500',
    bad: 'bg-error-500',
  }[tone ?? auto]
  return (
    <div className="h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-gray-800">
      <div className={cn('h-full rounded-full transition-all', color)} style={{ width: `${ratio * 100}%` }} />
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
      <path d="M14 3.5V8h4.5" fill="#dde9ff" />
    </svg>
  )
}
