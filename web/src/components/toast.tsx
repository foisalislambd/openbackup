import { useCallback, useMemo, useState, type ReactNode } from 'react'
import { ToastContext } from '@/lib/toast-context'

type Tone = 'ok' | 'bad'
type Toast = { id: number; text: string; tone: Tone }

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([])

  const push = useCallback((text: string, tone: Tone = 'ok') => {
    const id = Date.now() + Math.random()
    setToasts((current) => [...current.slice(-3), { id, text, tone }])
    window.setTimeout(() => {
      setToasts((current) => current.filter((toast) => toast.id !== id))
    }, 3200)
  }, [])

  const api = useMemo(() => ({ push }), [push])

  return (
    <ToastContext.Provider value={api}>
      {children}
      <div className="toast-stack" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={toast.tone === 'bad' ? 'toast toast-bad' : 'toast'} role="status">
            {toast.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  )
}
