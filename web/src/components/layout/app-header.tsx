import { Link } from 'react-router-dom'
import { FolderOpen, Menu, Moon, Sun, X } from 'lucide-react'
import { useSidebar } from '@/context/sidebar-context'
import { PageTitle } from '@/components/ui'
import { cn } from '@/lib/cn'

export function AppHeader({
  title,
  dark,
  onToggleTheme,
}: {
  title: string
  dark: boolean
  onToggleTheme: () => void
}) {
  const { isMobileOpen, isDesktop, toggleMobileSidebar } = useSidebar()

  return (
    <header className="admin-topbar sticky top-0 z-30 w-full overflow-hidden bg-white/95 backdrop-blur supports-[backdrop-filter]:bg-white/80 dark:bg-gray-900/95 dark:supports-[backdrop-filter]:bg-gray-900/80">
      <div className="flex h-full items-center gap-2 px-3 sm:gap-3 sm:px-5 lg:px-6">
        <button
          type="button"
          onClick={toggleMobileSidebar}
          className={cn(
            'flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-brand-500/30 dark:border-gray-800 dark:text-gray-400 dark:hover:bg-white/5',
            isDesktop && 'lg:hidden',
          )}
          aria-label={!isDesktop && isMobileOpen ? 'Close menu' : 'Open menu'}
          aria-expanded={!isDesktop ? isMobileOpen : undefined}
        >
          {!isDesktop && isMobileOpen ? <X className="h-5 w-5" /> : <Menu className="h-5 w-5" />}
        </button>

        <div className="min-w-0 flex-1">
          <PageTitle title={title} />
        </div>

        <div className="ml-auto flex shrink-0 items-center gap-1.5 sm:gap-2">
          <button
            type="button"
            onClick={onToggleTheme}
            className="flex h-9 w-9 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:bg-gray-50 dark:border-gray-800 dark:text-gray-400 dark:hover:bg-white/5"
            aria-label={dark ? 'Switch to light mode' : 'Switch to dark mode'}
          >
            {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </button>
          <Link
            to="/files"
            className="inline-flex h-9 items-center gap-1.5 rounded-lg bg-brand-500 px-2.5 text-sm font-medium text-white shadow-theme-xs transition hover:bg-brand-600 sm:gap-2 sm:px-3.5"
          >
            <FolderOpen className="h-4 w-4" />
            <span className="hidden sm:inline">Browse</span>
          </Link>
        </div>
      </div>
    </header>
  )
}
