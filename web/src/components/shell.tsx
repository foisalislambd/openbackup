'use client'

/**
 * Shell holds the navigation and the authentication gate.
 *
 * Because the dashboard is a static export, there is no server-side redirect to
 * a login page: the first thing the app does is ask the server whether it needs
 * setting up, whether the visitor is signed in, and only then renders anything.
 */

import { useCallback, useEffect, useState } from 'react'
import { usePathname, useRouter } from 'next/navigation'
import Link from 'next/link'
import { api, ApiError, type Bootstrap } from '@/lib/api'
import { Button, ErrorNote, Field, inputClass, Spinner } from './ui'

const navigation = [
  { href: '/', label: 'Overview' },
  { href: '/devices', label: 'Devices' },
  { href: '/backups', label: 'Backups' },
  { href: '/activity', label: 'Activity' },
  { href: '/settings', label: 'Settings' },
]

export function Shell({ children }: { children: React.ReactNode }) {
  const [state, setState] = useState<Bootstrap | null>(null)
  const [error, setError] = useState<string>()
  const pathname = usePathname()

  const load = useCallback(async () => {
    try {
      setState(await api.bootstrap())
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Cannot reach the server')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  if (error) {
    return (
      <main className="mx-auto max-w-md p-6">
        <ErrorNote>{error}</ErrorNote>
      </main>
    )
  }
  if (!state) {
    return (
      <main className="mx-auto max-w-md p-6">
        <Spinner label="Starting" />
      </main>
    )
  }
  if (state.needs_setup) {
    return <FirstRun onDone={load} />
  }
  if (!state.authenticated) {
    return <SignIn onDone={load} />
  }

  return (
    <div className="mx-auto flex min-h-screen max-w-6xl flex-col gap-6 p-4 sm:p-6">
      <header className="flex flex-wrap items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <Logo />
          <div>
            <div className="text-sm font-semibold">OpenBackup</div>
            <div className="text-xs text-[var(--color-ink-muted)]">{state.version}</div>
          </div>
        </div>
        <nav className="flex flex-wrap items-center gap-1">
          {navigation.map((item) => {
            const active = pathname === item.href
            return (
              <Link
                key={item.href}
                href={item.href}
                className={`rounded-lg px-3 py-1.5 text-sm font-medium transition ${
                  active
                    ? 'bg-[var(--color-brand-soft)] text-[var(--color-brand)]'
                    : 'text-[var(--color-ink-muted)] hover:text-[var(--color-ink)]'
                }`}
              >
                {item.label}
              </Link>
            )
          })}
          <SignOutButton onDone={load} />
        </nav>
      </header>
      <main className="flex-1">{children}</main>
      <footer className="pb-2 text-xs text-[var(--color-ink-muted)]">
        OpenBackup — your data, your server.
      </footer>
    </div>
  )
}

function Logo() {
  return (
    <div
      className="grid size-9 place-items-center rounded-xl text-white"
      style={{ background: 'var(--color-brand)' }}
      aria-hidden
    >
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <path d="M12 3v12" strokeLinecap="round" />
        <path d="m7 10 5 5 5-5" strokeLinecap="round" strokeLinejoin="round" />
        <path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2" strokeLinecap="round" />
      </svg>
    </div>
  )
}

function SignOutButton({ onDone }: { onDone: () => void }) {
  const router = useRouter()
  return (
    <Button
      variant="ghost"
      onClick={async () => {
        await api.logout()
        router.push('/')
        onDone()
      }}
    >
      Sign out
    </Button>
  )
}

/** FirstRun creates the very first account, which is all the setup there is. */
function FirstRun({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  return (
    <main className="mx-auto grid min-h-screen max-w-md place-items-center p-6">
      <form
        className="w-full space-y-4"
        onSubmit={async (event) => {
          event.preventDefault()
          setBusy(true)
          setError(undefined)
          try {
            await api.setup(email, password)
            onDone()
          } catch (err) {
            setError(err instanceof ApiError ? err.message : 'Could not create the account')
          } finally {
            setBusy(false)
          }
        }}
      >
        <div className="flex items-center gap-3">
          <Logo />
          <h1 className="text-lg font-semibold">Set up OpenBackup</h1>
        </div>
        <p className="text-sm text-[var(--color-ink-muted)]">
          This server has no account yet. Create one, and it becomes the owner.
        </p>
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
        <Field label="Password" hint="At least 12 characters. Use a password manager.">
          <input
            className={inputClass}
            type="password"
            required
            minLength={12}
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Button type="submit" variant="primary" disabled={busy} className="w-full">
          {busy ? 'Creating…' : 'Create account'}
        </Button>
      </form>
    </main>
  )
}

function SignIn({ onDone }: { onDone: () => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()

  return (
    <main className="mx-auto grid min-h-screen max-w-md place-items-center p-6">
      <form
        className="w-full space-y-4"
        onSubmit={async (event) => {
          event.preventDefault()
          setBusy(true)
          setError(undefined)
          try {
            await api.login(email, password)
            onDone()
          } catch (err) {
            setError(err instanceof ApiError ? err.message : 'Could not sign in')
          } finally {
            setBusy(false)
          }
        }}
      >
        <div className="flex items-center gap-3">
          <Logo />
          <h1 className="text-lg font-semibold">Sign in</h1>
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
        <Field label="Password">
          <input
            className={inputClass}
            type="password"
            required
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        <Button type="submit" variant="primary" disabled={busy} className="w-full">
          {busy ? 'Signing in…' : 'Sign in'}
        </Button>
      </form>
    </main>
  )
}
