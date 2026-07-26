// The bridge to the Go side of the app.
//
// Wails exposes bound methods on window.go.main.App and events on window.runtime.
// Everything the window can do goes through this file, so there is exactly one
// place where the frontend's idea of the agent meets the real one.

import type {
  AppInfo,
  Check,
  ConnectRequest,
  ConnectResult,
  Entry,
  Folder,
  Overview,
  Page,
  RestoreProgress,
  RestoreRequest,
  Settings,
  Snapshot,
} from './types'

type Bound = Record<string, (...args: unknown[]) => Promise<unknown>>

interface WailsWindow {
  go?: { main?: { App?: Bound } }
  runtime?: {
    EventsOn(name: string, handler: (...data: unknown[]) => void): () => void
  }
}

function bound(): Bound {
  const app = (window as unknown as WailsWindow).go?.main?.App
  if (!app) {
    throw new Error(
      'This window is not connected to OpenBackup. Restart the app, and if it keeps happening, reinstall it.',
    )
  }
  return app
}

/** available reports whether the Go side is reachable, so a browser-only dev
 *  session can render without throwing on every call. */
export function available(): boolean {
  return Boolean((window as unknown as WailsWindow).go?.main?.App)
}

async function call<T>(method: string, ...args: unknown[]): Promise<T> {
  const fn = bound()[method]
  if (typeof fn !== 'function') {
    throw new Error(`OpenBackup is missing the ${method} action; the app needs updating.`)
  }
  return (await fn(...args)) as T
}

export const api = {
  status: () => call<Overview>('Status'),
  diagnostics: () => call<Check[]>('Diagnostics'),
  info: () => call<AppInfo>('Info'),

  connect: (req: ConnectRequest) => call<ConnectResult>('Connect', req),

  folders: () => call<Folder[]>('Folders'),
  suggestedFolders: () => call<Folder[]>('SuggestedFolders'),
  /** Opens the native picker and starts backing up the chosen folder. Resolves to
   *  null when the user cancels. */
  chooseFolder: () => call<Folder | null>('ChooseFolder'),
  addFolder: (path: string) => call<void>('AddFolder', path),
  removeFolder: (path: string) => call<void>('RemoveFolder', path),
  setFolderEnabled: (path: string, enabled: boolean) =>
    call<void>('SetFolderEnabled', path, enabled),
  revealFolder: (path: string) => call<void>('RevealFolder', path),

  backupNow: () => call<void>('BackupNow'),
  pause: (minutes: number) => call<void>('Pause', minutes),
  resume: () => call<void>('Resume'),
  startService: () => call<void>('StartService'),

  snapshots: () => call<Snapshot[]>('Snapshots'),
  browse: (snapshot: string, prefix: string, cursor = '') =>
    call<Page>('Browse', snapshot, prefix, cursor),
  search: (snapshot: string, query: string) => call<Entry[]>('Search', snapshot, query),
  chooseRestoreTarget: () => call<string>('ChooseRestoreTarget'),
  startRestore: (req: RestoreRequest) => call<void>('StartRestore', req),
  cancelRestore: () => call<void>('CancelRestore'),
  restoreProgress: () => call<RestoreProgress | null>('RestoreProgress'),

  settings: () => call<Settings>('Settings'),
  updateSettings: (s: Settings) => call<Settings>('UpdateSettings', s),
  recoveryCode: () => call<string>('RecoveryCode'),
  enableEncryption: (recoveryCode: string) => call<string>('EnableEncryption', recoveryCode),

  openDashboard: () => call<void>('OpenDashboard'),
  openLogFolder: () => call<void>('OpenLogFolder'),
  minimiseToTray: () => call<void>('MinimiseToTray'),
  quit: () => call<void>('Quit'),
}

/** on subscribes to an event pushed by the Go side, returning an unsubscribe
 *  function. Status arrives this way so the window updates without polling. */
export function on<T>(event: 'status' | 'restore', handler: (data: T) => void): () => void {
  const runtime = (window as unknown as WailsWindow).runtime
  if (!runtime) {
    return () => {}
  }
  return runtime.EventsOn(event, (...data: unknown[]) => handler(data[0] as T))
}

/** message turns anything thrown by the bridge into a sentence worth showing.
 *  Go errors arrive as strings, which would otherwise render as "[object Object]"
 *  or an empty toast. */
export function message(err: unknown): string {
  if (typeof err === 'string') return err
  if (err instanceof Error) return err.message
  if (err && typeof err === 'object' && 'message' in err) {
    return String((err as { message: unknown }).message)
  }
  return 'Something went wrong.'
}
