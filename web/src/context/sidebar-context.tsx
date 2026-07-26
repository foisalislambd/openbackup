import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { useIsDesktop } from '@/hooks/use-is-desktop'

type SidebarContextValue = {
  isExpanded: boolean
  isMobileOpen: boolean
  isDesktop: boolean
  toggleSidebar: () => void
  toggleMobileSidebar: () => void
  closeMobileSidebar: () => void
}

const SidebarContext = createContext<SidebarContextValue | null>(null)

export function SidebarProvider({ children }: { children: ReactNode }) {
  const isDesktop = useIsDesktop()
  const [isExpanded, setIsExpanded] = useState(true)
  const [isMobileOpen, setIsMobileOpen] = useState(false)

  const effectiveExpanded = isDesktop ? isExpanded : true
  const effectiveMobileOpen = isDesktop ? false : isMobileOpen

  useEffect(() => {
    document.body.style.overflow = effectiveMobileOpen ? 'hidden' : ''
    return () => {
      document.body.style.overflow = ''
    }
  }, [effectiveMobileOpen])

  const toggleSidebar = useCallback(() => setIsExpanded((v) => !v), [])
  const toggleMobileSidebar = useCallback(() => setIsMobileOpen((v) => !v), [])
  const closeMobileSidebar = useCallback(() => setIsMobileOpen(false), [])

  const value = useMemo(
    () => ({
      isExpanded: effectiveExpanded,
      isMobileOpen: effectiveMobileOpen,
      isDesktop,
      toggleSidebar,
      toggleMobileSidebar,
      closeMobileSidebar,
    }),
    [effectiveExpanded, effectiveMobileOpen, isDesktop, toggleSidebar, toggleMobileSidebar, closeMobileSidebar],
  )

  return <SidebarContext.Provider value={value}>{children}</SidebarContext.Provider>
}

// eslint-disable-next-line react-refresh/only-export-components -- hook lives with its provider
export function useSidebar() {
  const ctx = useContext(SidebarContext)
  if (!ctx) throw new Error('useSidebar must be used within SidebarProvider')
  return ctx
}
