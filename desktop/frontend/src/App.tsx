import { useEffect, useState } from 'react'

import { Spinner } from './components/ui'
import { useStatus } from './lib/use-status'
import { Folders } from './screens/Folders'
import { Health } from './screens/Health'
import { Home } from './screens/Home'
import { Logs } from './screens/Logs'
import { Onboarding } from './screens/Onboarding'
import { Restore } from './screens/Restore'
import { Settings } from './screens/Settings'

type Tab = 'home' | 'folders' | 'restore' | 'logs' | 'settings' | 'health'

const tabs: { id: Tab; label: string; icon: string }[] = [
  { id: 'home', label: 'Overview', icon: 'M3 10.5 12 4l9 6.5V20a1 1 0 0 1-1 1h-5v-6H10v6H4a1 1 0 0 1-1-1z' },
  { id: 'folders', label: 'Folders', icon: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z' },
  { id: 'restore', label: 'Restore', icon: 'M12 5v8m0 0 3.5-3.5M12 13 8.5 9.5M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3' },
  { id: 'logs', label: 'Logs', icon: 'M4 6h16M4 12h16M4 18h10' },
  { id: 'settings', label: 'Settings', icon: 'M10.3 3h3.4l.5 2.3 2 1.2 2.2-.8 1.7 2.9-1.7 1.6v2.3l1.7 1.6-1.7 2.9-2.2-.8-2 1.2-.5 2.3h-3.4l-.5-2.3-2-1.2-2.2.8-1.7-2.9 1.7-1.6v-2.3L4.1 8.6l1.7-2.9 2.2.8 2-1.2z' },
  { id: 'health', label: 'Diagnostics', icon: 'M4 12h3l2-5 3 10 2.5-5H20' },
]

const THEME_KEY = 'openbackup-desktop-theme'

function useTheme() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains('dark'))
  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    document.documentElement.classList.toggle('light', !dark)
    localStorage.setItem(THEME_KEY, dark ? 'dark' : 'light')
  }, [dark])
  return { dark, toggle: () => setDark((value) => !value) }
}

export default function App() {
  const { status, error, refresh } = useStatus()
  const theme = useTheme()
  const [tab, setTab] = useState<Tab>('home')
  // Stay on onboarding after Connect until Done, so a status poll cannot hide
  // the recovery code before the user copies it.
  const [holdOnboarding, setHoldOnboarding] = useState(false)

  // Nothing can be shown until the agent's state is known: guessing would mean
  // telling the user their data is safe before checking.
  if (!status) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 text-ink-muted">
        {error ? (
          <>
            <p className="selectable max-w-md text-center text-sm">{error}</p>
            <button className="text-sm text-brand underline" onClick={refresh}>
              Try again
            </button>
          </>
        ) : (
          <>
            <Spinner className="h-5 w-5" />
            <p className="text-sm">Checking your backups...</p>
          </>
        )}
      </div>
    )
  }

  // A device that is not connected has exactly one thing to do, so it gets the
  // whole window rather than a disabled version of the normal interface.
  if (!status.connected || holdOnboarding) {
    return (
      <Onboarding
        onConnectSuccess={() => setHoldOnboarding(true)}
        onConnected={() => {
          setHoldOnboarding(false)
          refresh()
        }}
      />
    )
  }

  return (
    <div className="flex h-full bg-surface-muted">
      <nav className="flex w-56 shrink-0 flex-col border-r border-border-subtle bg-surface px-3 py-4" aria-label="Sections">
        <div className="mb-5 flex items-center gap-3 px-2">
          <span className="grid h-9 w-9 shrink-0 place-items-center rounded-lg bg-brand text-sm font-bold text-white">
            O
          </span>
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-ink">OpenBackup</p>
            <p className="truncate text-xs text-ink-muted">{status.device_name}</p>
          </div>
        </div>

        {tabs.map((item) => (
          <button
            key={item.id}
            onClick={() => setTab(item.id)}
            aria-current={tab === item.id ? 'page' : undefined}
            className={`mb-0.5 flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm transition-colors
              focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand/40
              ${
                tab === item.id
                  ? 'bg-brand-soft font-semibold text-brand'
                  : 'text-ink-muted hover:bg-surface-muted hover:text-ink'
              }`}
          >
            <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.7">
              <path d={item.icon} strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            {item.label}
          </button>
        ))}

        <div className="mt-auto space-y-2 px-2 pb-1">
          <button
            type="button"
            onClick={theme.toggle}
            className="flex w-full items-center justify-between rounded-lg px-1 py-1.5 text-xs font-semibold text-ink-muted hover:text-ink"
            aria-label={theme.dark ? 'Switch to light mode' : 'Switch to dark mode'}
          >
            <span>{theme.dark ? 'Dark' : 'Light'}</span>
            <span className="rounded-md border border-border-subtle px-2 py-0.5">{theme.dark ? 'Light' : 'Dark'}</span>
          </button>
          <div className="text-xs text-ink-muted">
            <p className="truncate" title={status.server_url}>
              {status.server_url.replace(/^https?:\/\//, '')}
            </p>
            <p className="mt-0.5">Version {status.version}</p>
          </div>
        </div>
      </nav>

      <main className="min-w-0 flex-1 overflow-y-auto p-6">
        {tab === 'home' && (
          <Home status={status} onGoToFolders={() => setTab('folders')} onRefresh={refresh} />
        )}
        {tab === 'folders' && <Folders status={status} />}
        {tab === 'restore' && <Restore />}
        {tab === 'logs' && <Logs status={status} />}
        {tab === 'settings' && <Settings status={status} onLoggedOut={refresh} />}
        {tab === 'health' && <Health status={status} />}
      </main>
    </div>
  )
}
