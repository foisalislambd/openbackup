import { useState } from 'react'

import { Spinner } from './components/ui'
import { useStatus } from './lib/use-status'
import { Folders } from './screens/Folders'
import { Health } from './screens/Health'
import { Home } from './screens/Home'
import { Onboarding } from './screens/Onboarding'
import { Restore } from './screens/Restore'
import { Settings } from './screens/Settings'

type Tab = 'home' | 'folders' | 'restore' | 'settings' | 'health'

const tabs: { id: Tab; label: string; icon: string }[] = [
  { id: 'home', label: 'Overview', icon: 'M3 10.5 12 4l9 6.5V20a1 1 0 0 1-1 1h-5v-6H10v6H4a1 1 0 0 1-1-1z' },
  { id: 'folders', label: 'Folders', icon: 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z' },
  { id: 'restore', label: 'Restore', icon: 'M12 5v8m0 0 3.5-3.5M12 13 8.5 9.5M4 15v3a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-3' },
  { id: 'settings', label: 'Settings', icon: 'M10.3 3h3.4l.5 2.3 2 1.2 2.2-.8 1.7 2.9-1.7 1.6v2.3l1.7 1.6-1.7 2.9-2.2-.8-2 1.2-.5 2.3h-3.4l-.5-2.3-2-1.2-2.2.8-1.7-2.9 1.7-1.6v-2.3L4.1 8.6l1.7-2.9 2.2.8 2-1.2z' },
  { id: 'health', label: 'Diagnostics', icon: 'M4 12h3l2-5 3 10 2.5-5H20' },
]

export default function App() {
  const { status, error, refresh } = useStatus()
  const [tab, setTab] = useState<Tab>('home')

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
  if (!status.connected) {
    return <Onboarding onConnected={refresh} />
  }

  return (
    <div className="flex h-full">
      <nav className="flex w-52 shrink-0 flex-col border-r border-border-subtle bg-surface/60 p-3">
        <div className="mb-4 flex items-center gap-2 px-2 pt-1">
          <Mark health={status.health} />
          <div className="min-w-0">
            <p className="truncate text-sm font-semibold text-ink">OpenBackup</p>
            <p className="truncate text-xs text-ink-muted">{status.device_name}</p>
          </div>
        </div>

        {tabs.map((item) => (
          <button
            key={item.id}
            onClick={() => setTab(item.id)}
            className={`flex items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm transition-colors
              ${
                tab === item.id
                  ? 'bg-brand-soft font-medium text-brand'
                  : 'text-ink-muted hover:bg-surface-muted hover:text-ink'
              }`}
          >
            <svg viewBox="0 0 24 24" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.7">
              <path d={item.icon} strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            {item.label}
          </button>
        ))}

        <div className="mt-auto px-2.5 pb-1 text-xs text-ink-muted">
          <p className="truncate" title={status.server_url}>
            {status.server_url.replace(/^https?:\/\//, '')}
          </p>
          <p className="mt-0.5">Version {status.version}</p>
        </div>
      </nav>

      <main className="min-w-0 flex-1 overflow-y-auto p-6">
        {tab === 'home' && <Home status={status} onGoToFolders={() => setTab('folders')} />}
        {tab === 'folders' && <Folders status={status} />}
        {tab === 'restore' && <Restore />}
        {tab === 'settings' && <Settings status={status} onLoggedOut={refresh} />}
        {tab === 'health' && <Health status={status} />}
      </main>
    </div>
  )
}

/** Mark is the shield from the app icon, tinted by health, so the window itself
 *  carries the same signal as the tray. */
function Mark({ health }: { health: string }) {
  const colour =
    health === 'protected'
      ? 'text-good'
      : health === 'working'
        ? 'text-brand'
        : health === 'paused' || health === 'never_run'
          ? 'text-ink-muted'
          : 'text-warn'
  return (
    <svg viewBox="0 0 24 24" className={`h-7 w-7 ${colour}`} fill="currentColor" aria-hidden>
      <path d="M12 2.5 4.5 5.5v7.2c0 4.4 3.2 7.6 7.5 9 4.3-1.4 7.5-4.6 7.5-9V5.5z" opacity="0.18" />
      <path
        d="M12 3.6 5.6 6.2v6.5c0 3.8 2.7 6.6 6.4 7.9 3.7-1.3 6.4-4.1 6.4-7.9V6.2zm-.6 11.2L8.6 12l1.1-1.1 1.7 1.7 4-4L16.5 9.7z"
        fillRule="evenodd"
      />
    </svg>
  )
}
