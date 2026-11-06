import {
  Activity,
  FolderOpen,
  HardDrive,
  Home,
  Settings,
  type LucideIcon,
} from 'lucide-react'

export type NavItem = {
  href: string
  label: string
  icon: LucideIcon
}

export const navItems: NavItem[] = [
  { href: '/', label: 'Overview', icon: Home },
  { href: '/files', label: 'My files', icon: FolderOpen },
  { href: '/devices', label: 'Devices', icon: HardDrive },
  { href: '/logs', label: 'Logs', icon: Activity },
  { href: '/settings', label: 'Settings', icon: Settings },
]

export const pageMeta: Record<string, { title: string }> = {
  '/': { title: 'Overview' },
  '/files': { title: 'Files' },
  '/devices': { title: 'Devices' },
  '/logs': { title: 'Activity' },
  '/settings': { title: 'Settings' },
}

/** Normalize legacy paths so old bookmarks keep working. */
export function normalizePath(pathname: string): string {
  if (pathname === '/backups' || pathname.startsWith('/backups/')) return '/files'
  if (pathname === '/activity' || pathname.startsWith('/activity/')) return '/logs'
  return pathname
}

export function isNavActive(pathname: string, href: string): boolean {
  const path = normalizePath(pathname)
  if (href === '/') return path === '/'
  return path === href || path.startsWith(`${href}/`)
}
