/**
 * useLoader is the one place the dashboard fetches data.
 *
 * Every view needs the same four things: load on mount, poll while the tab is
 * open so a running backup is visible, reload after an action, and drop the
 * result if the view is gone by the time it arrives. Doing that once here keeps
 * the pages declarative, and keeps state updates inside promise callbacks rather
 * than in an effect body, which is both what React recommends and what the
 * compiler's lint rules require.
 */

import { useCallback, useEffect, useRef, useState } from 'react'

export type Loader<T> = {
  data: T | undefined
  error: string | undefined
  /** loading is true until the first result or error arrives. */
  loading: boolean
  /** reload re-runs the fetch, keeping the current data on screen meanwhile. */
  reload: () => void
}

type Options = {
  /** pollMs re-fetches on an interval. Polling stops while the tab is hidden. */
  pollMs?: number
  /** deps re-runs the fetch when any of them change. */
  deps?: readonly unknown[]
}

export function useLoader<T>(fetcher: () => Promise<T>, options: Options = {}): Loader<T> {
  const { pollMs, deps = [] } = options
  const [result, setResult] = useState<{ data?: T; error?: string }>({})
  const [nonce, setNonce] = useState(0)
  const depsKey = JSON.stringify(deps)
  const depsKeyRef = useRef(depsKey)

  const fetcherRef = useRef(fetcher)
  useEffect(() => {
    fetcherRef.current = fetcher
  })

  const reload = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    let active = true
    const depsChanged = depsKeyRef.current !== depsKey
    depsKeyRef.current = depsKey
    // Drop stale rows when the query identity changes (folder, snapshot, …).
    // Poll/reload keeps the previous paint so the UI does not flash empty.
    if (depsChanged) {
      setResult({})
    }

    const run = () => {
      fetcherRef.current().then(
        (data) => {
          if (active) setResult({ data })
        },
        (err: unknown) => {
          if (active) {
            setResult((prev) => ({ data: prev.data, error: message(err) }))
          }
        },
      )
    }

    run()

    if (!pollMs) {
      return () => {
        active = false
      }
    }

    const timer = setInterval(() => {
      if (document.visibilityState === 'visible') run()
    }, pollMs)

    return () => {
      active = false
      clearInterval(timer)
    }
  }, [nonce, pollMs, depsKey])

  return {
    data: result.data,
    error: result.error,
    loading: result.data === undefined && result.error === undefined,
    reload,
  }
}

/** message turns whatever was thrown into something worth showing a user. */
export function message(err: unknown, fallback = 'Something went wrong'): string {
  if (err instanceof Error && err.message) return err.message
  if (typeof err === 'string' && err) return err
  return fallback
}

/**
 * useAction runs a one-shot mutation, tracking which action is in flight so a
 * row can disable just the button that was pressed.
 */
export function useAction() {
  const [busy, setBusy] = useState<string>()
  const [error, setError] = useState<string>()

  const run = useCallback(async (label: string, fn: () => Promise<unknown>, after?: () => void) => {
    setBusy(label)
    setError(undefined)
    try {
      await fn()
      after?.()
    } catch (err) {
      setError(message(err, 'That did not work'))
    } finally {
      setBusy(undefined)
    }
  }, [])

  return { busy, error, run, clearError: () => setError(undefined) }
}
