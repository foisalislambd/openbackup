import { createContext, useContext } from 'react'

type Tone = 'ok' | 'bad'

export type ToastApi = {
  push: (text: string, tone?: Tone) => void
}

export const ToastContext = createContext<ToastApi>({ push: () => {} })

export function useToast() {
  return useContext(ToastContext)
}
