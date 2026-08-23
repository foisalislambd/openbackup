/**
 * Auth screens for first-time setup and sign-in (ZenPanel-style).
 */

import { useState } from 'react'
import { Lock, Mail } from 'lucide-react'
import { api } from '@/lib/api'
import { message } from '@/lib/use-loader'
import { Button, ErrorNote, Field, inputClass } from '@/components/ui'

export function AuthScreen({ mode, onDone }: { mode: 'setup' | 'signin'; onDone: () => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [showPassword, setShowPassword] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string>()
  const setup = mode === 'setup'

  return (
    <main className="flex min-h-dvh flex-col justify-center bg-gray-50 px-5 py-10 dark:bg-gray-900 sm:px-10">
      <div className="mx-auto w-full max-w-[400px]">
        <div className="mb-8 flex h-11 w-11 items-center justify-center rounded-xl bg-brand-500 text-base font-bold text-white shadow-theme-xs">
          O
        </div>
        <h1 className="text-2xl font-semibold tracking-tight text-gray-900 dark:text-white">
          {setup ? 'Set up OpenBackup' : 'Sign in'}
        </h1>
        <p className="mt-2 text-sm text-gray-500 dark:text-gray-400">
          {setup
            ? 'This account owns the backup server. Use an email you check.'
            : 'Sign in to browse backups, download files, and manage devices.'}
        </p>

        <form
          className="mt-8 space-y-5"
          onSubmit={(event) => {
            event.preventDefault()
            setBusy(true)
            setError(undefined)
            const action = setup ? api.setup(email, password) : api.login(email, password)
            action.then(
              () => {
                // Reset before handing over: if the post-login bootstrap stalls,
                // this screen must stay usable instead of freezing on "…".
                setBusy(false)
                onDone()
              },
              (err: unknown) => {
                setError(message(err, setup ? 'Could not create the account' : 'Could not sign in'))
                setBusy(false)
              },
            )
          }}
        >
          {error && <ErrorNote>{error}</ErrorNote>}
          <Field label="Email">
            <div className="relative mt-1.5">
              <Mail className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-gray-400" />
              <input
                className={`${inputClass} !mt-0 pl-10`}
                type="email"
                required
                autoComplete="username"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
              />
            </div>
          </Field>
          <Field label="Password" hint={setup ? 'At least 10 characters.' : undefined}>
            <div className="relative mt-1.5">
              <Lock className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-gray-400" />
              <input
                className={`${inputClass} !mt-0 pr-16 pl-10`}
                type={showPassword ? 'text' : 'password'}
                required
                minLength={setup ? 10 : undefined}
                autoComplete={setup ? 'new-password' : 'current-password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <button
                type="button"
                className="absolute top-1/2 right-3 -translate-y-1/2 text-xs font-semibold text-gray-500 hover:text-gray-800"
                onClick={() => setShowPassword((v) => !v)}
              >
                {showPassword ? 'Hide' : 'Show'}
              </button>
            </div>
          </Field>
          <Button type="submit" variant="primary" disabled={busy} className="h-11 w-full">
            {busy ? (setup ? 'Creating…' : 'Signing in…') : setup ? 'Create account' : 'Sign in'}
          </Button>
        </form>
      </div>
    </main>
  )
}
