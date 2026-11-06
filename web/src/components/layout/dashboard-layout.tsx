/**
 * Dashboard chrome: sidebar, top bar, auth gate (ZenPanel admin shell).
 */

import { useEffect, useState, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { api, type Bootstrap } from '@/lib/api'
import { useLoader } from '@/lib/use-loader'
import { ErrorNote } from '@/components/ui'
import { ShellSkeleton } from '@/components/skeleton'
import { SidebarProvider, useSidebar } from '@/context/sidebar-context'
import { SIDEBAR_WIDTH_COLLAPSED, SIDEBAR_WIDTH_EXPANDED } from '@/lib/sidebar'
import { normalizePath, pageMeta } from '@/lib/nav'
import { AppSidebar } from '@/components/layout/app-sidebar'
import { AppHeader } from '@/components/layout/app-header'
import { MobileNav } from '@/components/layout/mobile-nav'
import { AuthScreen } from '@/components/layout/auth-screen'

const THEME_KEY = 'openbackup-theme'

export function DashboardLayout({ children }: { children: ReactNode }) {
  const { data: state, error, loading, reload } = useLoader<Bootstrap>(() => api.bootstrap())
  const [dark, setDark] = useState(() => {
    if (typeof window === 'undefined') return false
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'dark') return true
    if (saved === 'light') return false
    return window.matchMedia('(prefers-color-scheme: dark)').matches
  })

  useEffect(() => {
    document.documentElement.classList.toggle('dark', dark)
    localStorage.setItem(THEME_KEY, dark ? 'dark' : 'light')
  }, [dark])

  if (error) {
    return (
      <main className="mx-auto grid min-h-dvh max-w-md place-items-center p-6">
        <ErrorNote>{error}</ErrorNote>
      </main>
    )
  }
  if (loading || !state) return <ShellSkeleton />
  if (state.needs_setup) return <AuthScreen mode="setup" onDone={reload} />
  if (!state.authenticated) return <AuthScreen mode="signin" onDone={reload} />

  return (
    <SidebarProvider>
      <DashboardChrome
        version={state.version}
        dark={dark}
        onToggleTheme={() => setDark((v) => !v)}
        onSignedOut={reload}
      >
        {children}
      </DashboardChrome>
    </SidebarProvider>
  )
}

function DashboardChrome({
  children,
  version,
  dark,
  onToggleTheme,
  onSignedOut,
}: {
  children: ReactNode
  version: string
  dark: boolean
  onToggleTheme: () => void
  onSignedOut: () => void
}) {
  const location = useLocation()
  const navigate = useNavigate()
  const { isExpanded, isDesktop, isMobileOpen, closeMobileSidebar } = useSidebar()
  const [signOutError, setSignOutError] = useState('')
  const path = normalizePath(location.pathname)
  const heading = pageMeta[path] ?? pageMeta['/']
  const sidebarWidth = isDesktop ? (isExpanded ? SIDEBAR_WIDTH_EXPANDED : SIDEBAR_WIDTH_COLLAPSED) : 0

  return (
    <div className="admin-shell admin-main flex h-dvh w-full overflow-hidden">
      <div
        className="hidden shrink-0 transition-[width] duration-300 ease-in-out lg:block"
        style={{ width: sidebarWidth }}
        aria-hidden
      />

      <AppSidebar
        version={version}
        onSignOut={() => {
          // Only leave the dashboard once the session is really gone; a failed
          // request would otherwise bounce back into an authenticated shell.
          api.logout().then(
            () => {
              setSignOutError('')
              navigate('/')
              onSignedOut()
            },
            (err: unknown) => {
              setSignOutError(err instanceof Error ? err.message : String(err))
            },
          )
        }}
      />
      {signOutError && (
        <div className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2">
          <ErrorNote>Sign-out failed: {signOutError}</ErrorNote>
        </div>
      )}

      {isMobileOpen && (
        <button
          type="button"
          className="fixed inset-0 z-40 bg-gray-900/40 backdrop-blur-[1px] lg:hidden"
          aria-label="Close menu backdrop"
          onClick={closeMobileSidebar}
        />
      )}

      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <AppHeader title={heading.title} dark={dark} onToggleTheme={onToggleTheme} />
        <main className="admin-scrollbar min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
          <div className="w-full px-3 py-4 pb-24 sm:px-6 sm:py-6 lg:px-8 lg:pb-8">{children}</div>
        </main>
        <MobileNav />
      </div>
    </div>
  )
}
