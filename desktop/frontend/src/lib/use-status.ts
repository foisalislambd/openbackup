// Status plumbing shared by every screen.

import { useCallback, useEffect, useRef, useState } from 'react'

import { api, available, message, on } from './bridge'
import type { Overview } from './types'

/** useStatus keeps the current overview in state.
 *
 *  The Go side pushes an update every couple of seconds, so this listens rather
 *  than polls; the one direct call is for the first paint, before the first event
 *  arrives. */
export function useStatus(): { status: Overview | null; error: string; refresh: () => void } {
  const [status, setStatus] = useState<Overview | null>(null)
  const [error, setError] = useState('')

  const refresh = useCallback(() => {
    if (!available()) {
      setError('This window is not connected to OpenBackup.')
      return
    }
    api
      .status()
      .then((next) => {
        setStatus(next)
        setError('')
      })
      .catch((err) => setError(message(err)))
  }, [])

  useEffect(() => {
    refresh()
    return on<Overview>('status', (next) => {
      setStatus(next)
      setError('')
    })
  }, [refresh])

  return { status, error, refresh }
}

/** useAsync runs a loader and re-runs it when told to, which is the pattern behind
 *  every list in the app. */
export function useAsync<T>(
  loader: () => Promise<T>,
  deps: unknown[] = [],
): { data: T | null; loading: boolean; error: string; reload: () => void } {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [nonce, setNonce] = useState(0)

  // The loader is usually an inline arrow function, so it is held in a ref rather
  // than in the dependency list: otherwise every render would refetch.
  const loaderRef = useRef(loader)
  useEffect(() => {
    loaderRef.current = loader
  })

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    loaderRef
      .current()
      .then((value) => {
        if (cancelled) return
        setData(value)
        setError('')
      })
      .catch((err) => {
        if (cancelled) return
        setError(message(err))
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce, ...deps])

  return { data, loading, error, reload: () => setNonce((n) => n + 1) }
}

/** useAction runs a one-off action, tracking whether it is in flight and what went
 *  wrong. Buttons need exactly this and nothing more. */
export function useAction(): {
  busy: boolean
  error: string
  clearError: () => void
  run: (action: () => Promise<unknown>, onDone?: () => void) => Promise<void>
} {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const run = useCallback(async (action: () => Promise<unknown>, onDone?: () => void) => {
    setBusy(true)
    setError('')
    try {
      await action()
      onDone?.()
    } catch (err) {
      setError(message(err))
    } finally {
      setBusy(false)
    }
  }, [])

  return { busy, error, clearError: () => setError(''), run }
}
