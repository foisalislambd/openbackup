import { Link, useLocation } from 'react-router-dom'
import { isNavActive, navItems } from '@/lib/nav'
import { cn } from '@/lib/cn'

/** Thumb-reachable navigation for narrow screens. The sidebar stays the
 * desktop navigation. */
export function MobileNav() {
  const { pathname } = useLocation()
  return (
    <nav className="mobile-tabbar flex lg:hidden" aria-label="Sections">
      {navItems.map((item) => {
        const active = isNavActive(pathname, item.href)
        const Icon = item.icon
        return (
          <Link
            key={item.href}
            to={item.href}
            aria-current={active ? 'page' : undefined}
            className={cn('mobile-tab', active && 'mobile-tab-active')}
          >
            <Icon className="h-5 w-5" aria-hidden />
            <span>{item.label === 'My files' ? 'Files' : item.label}</span>
          </Link>
        )
      })}
    </nav>
  )
}
