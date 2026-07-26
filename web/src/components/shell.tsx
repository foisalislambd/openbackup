/**
 * Shell is the app chrome: sidebar navigation, top bar, and the auth gate.
 */

import { useState, type ReactNode } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { api, type Bootstrap } from '@/lib/api'
import { message, useLoader } from '@/lib/use-loader'
import { Button, ErrorNote, Field, inputClass, PageTitle } from './ui'
import { ShellSkeleton } from './skeleton'

const navigation = [
  { href: '/', label: 'Home', icon: HomeIcon },
  { href: '/backups', label: 'My files', icon: FilesIcon },
  { href: '/devices', label: 'Devices', icon: DevicesIcon },
  { href: '/activity', label: 'Activity', icon: ActivityIcon },
  { href: '/settings', label: 'Settings', icon: SettingsIcon },
]

const titles: Record<string, { title: string; subtitle: string }> = {
  '/': { title: 'Home', subtitle: 'Backup health and recent files' },
  '/backups': { title: 'My files', subtitle: 'Browse and restore from any backup' },
  '/devices': { title: 'Devices', subtitle: 'Computers connected to this server' },
  '/activity': { title: 'Activity', subtitle: 'What happened during backups' },
  '/settings': { title: 'Settings', subtitle: 'Account, retention, and exclusions' },
}

export function Shell({ children }: { children: ReactNode }) {
  const location = useLocation()
  const { data: state, error, loading, reload } = useLoader<Bootstrap>(() => api.bootstrap())

  if (error) {
    return (
      <main className="mx-auto grid min-h-screen max-w-md place-items-center p-6">
        <ErrorNote>{error}</ErrorNote>
      </main>
    )
  }
  if (loading || !state) {
    return <ShellSkeleton />
  }
  if (state.needs_setup) {
    return <AuthScreen mode="setup" onDone={reload} />
  }
  if (!state.authenticated) {
    return <AuthScreen mode="signin" onDone={reload} />
  }

  const path = location.pathname.startsWith('/backups') ? '/backups' : location.pathname
  const heading = titles[path] ?? titles['/']

  return (
    <div className="app-shell">
      <aside className="app-sidebar">
        <Link to="/" className="flex items-center gap-3 px-2 no-underline">
          <Logo />
          <div className="min-w-0">
            <div className="truncate text-[0.95rem] font-semibold tracking-tight text-[var(--color-ink)]">
              OpenBackup
            </div>
            <div className="truncate text-[0.7rem] text-[var(--color-ink-muted)]">v{state.version}</div>
          </div>
        </Link>

        <nav className="app-sidebar-nav flex flex-1 flex-col gap-1">
          {navigation.map((item) => {
            const active =
              item.href === '/'
                ? location.pathname === '/'
                : location.pathname === item.href || location.pathname.startsWith(`${item.href}/`)
            const Icon = item.icon
            return (
              <Link
                key={item.href}
                to={item.href}
                className={`flex items-center gap-2.5 rounded-xl px-3 py-2.5 text-sm font-medium transition ${
                  active
                    ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand-strong)]'
                    : 'text-[var(--color-ink-muted)] hover:bg-[var(--color-surface-muted)] hover:text-[var(--color-ink)]'
                }`}
              >
                <Icon active={active} />
                {item.label}
              </Link>
            )
          })}
        </nav>

        <div className="app-sidebar-foot space-y-2 px-2">
          <p className="text-[0.7rem] leading-relaxed text-[var(--color-ink-muted)]">
            Your files on your server.
          </p>
          <SignOutButton onDone={reload} />
        </div>
      </aside>

      <div className="app-main">
        <header className="app-topbar">
          <PageTitle title={heading.title} subtitle={heading.subtitle} />
          <div className="hidden items-center gap-2 sm:flex">
            <Link
              to="/backups"
              className="inline-flex items-center rounded-xl bg-[var(--color-brand)] px-3.5 py-2 text-sm font-semibold text-white transition hover:bg-[var(--color-brand-strong)]"
            >
              Browse files
            </Link>
          </div>
        </header>
        <main className="app-content">{children}</main>
      </div>
    </div>
  )
}

function Logo() {
  return (
    <div
      className="grid size-10 shrink-0 place-items-center rounded-2xl text-white shadow-[var(--shadow-lift)]"
      style={{ background: 'linear-gradient(145deg, var(--color-brand), var(--color-brand-strong))' }}
      aria-hidden
    >
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.1">
        <path d="M12 3v12" strokeLinecap="round" />
        <path d="m7 10 5 5 5-5" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" strokeLinecap="round" />
      </svg>
    </div>
  )
}

function SignOutButton({ onDone }: { onDone: () => void }) {
  const navigate = useNavigate()
  return (
    <Button
      variant="ghost"
      className="w-full justify-start px-2"
      onClick={() => {
        void api.logout().then(() => {
          navigate('/')
          onDone()
        })
      }}
    >
      Sign out
    </Button>
  )
}

function AuthScreen({ mode, onDone }: { mode: 'setup' | 'signin'; onDone: () => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const setup = mode === 'setup'

  return (
    <main className="relative grid min-h-screen place-items-center overflow-hidden p-6">
      <div
        className="pointer-events-none absolute inset-0 opacity-80"
        style={{
          background:
            'radial-gradient(700px 400px at 20% 20%, color-mix(in oklch, var(--color-brand) 18%, transparent), transparent), radial-gradient(600px 360px at 80% 80%, color-mix(in oklch, var(--color-brand) 12%, transparent), transparent)',
        }}
      />
      <form
        className="panel relative w-full max-w-md space-y-5 p-8"
        onSubmit={(event) => {
          event.preventDefault()
          setBusy(true)
          setError(undefined)
          const action = setup ? api.setup(email, password) : api.login(email, password)
          action.then(
            () => onDone(),
            (err: unknown) => {
              setError(message(err, setup ? 'Could not create the account' : 'Could not sign in'))
              setBusy(false)
            },
          )
        }}
      >
        <div className="flex items-center gap-3">
          <Logo />
          <div>
            <h1 className="text-xl font-semibold tracking-tight">{setup ? 'Set up OpenBackup' : 'Sign in'}</h1>
            <p className="text-sm text-[var(--color-ink-muted)]">
              {setup ? 'Create the owner account for this server.' : 'Open your private backup library.'}
            </p>
          </div>
        </div>
        {error && <ErrorNote>{error}</ErrorNote>}
        <Field label="Email">
          <input
            className={inputClass}
            type="email"
            required
            autoComplete="username"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field
          label="Password"
          hint={setup ? 'At least 12 characters. Use a password manager.' : undefined}
        >
          <input
            className={inputClass}
            type="password"
            required
            minLength={setup ? 12 : undefined}
            autoComplete={setup ? 'new-password' : 'current-password'}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Button type="submit" variant="primary" disabled={busy} className="w-full">
          {busy ? (setup ? 'Creating…' : 'Signing in…') : setup ? 'Create account' : 'Sign in'}
        </Button>
      </form>
    </main>
  )
}

function HomeIcon({ active }: { active: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={active ? 2.2 : 1.8}>
      <path d="M4 10.5 12 4l8 6.5V20a1 1 0 0 1-1 1h-5v-6H10v6H5a1 1 0 0 1-1-1z" strokeLinejoin="round" />
    </svg>
  )
}

function FilesIcon({ active }: { active: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={active ? 2.2 : 1.8}>
      <path d="M4 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2z" strokeLinejoin="round" />
    </svg>
  )
}

function DevicesIcon({ active }: { active: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={active ? 2.2 : 1.8}>
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M8 20h8M12 16v4" strokeLinecap="round" />
    </svg>
  )
}

function ActivityIcon({ active }: { active: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={active ? 2.2 : 1.8}>
      <path d="M4 12h4l2-6 4 12 2-6h4" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function SettingsIcon({ active }: { active: boolean }) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={active ? 2.2 : 1.8}>
      <circle cx="12" cy="12" r="3" />
      <path d="M12 3v2M12 19v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M3 12h2M19 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" strokeLinecap="round" />
    </svg>
  )
}
