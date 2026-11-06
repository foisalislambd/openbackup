/**
 * Typed client for the dashboard API.
 *
 * Authentication is a session cookie set by the server, so nothing here stores
 * or forwards a token: the browser does it, the cookie is HttpOnly, and there is
 * no credential for a cross-site script to steal.
 */

export type Device = {
  id: string
  name: string
  hostname: string
  platform: string
  os_version?: string
  agent_version?: string
  key_id?: string
  encrypted?: boolean
  created_at: string
  last_seen?: string | null
  state: string
  state_reason?: string
  queued_files?: number
  queued_bytes?: number
  last_error?: string
  current_path?: string
  files_done?: number
  files_total?: number
  snapshot_count?: number
  logical_bytes?: number
  last_backup_at?: string | null
  health: string
}

export type Snapshot = {
  id: string
  device_id: string
  device_name?: string
  kind: string
  status: string
  parent_id?: string
  started_at: string
  completed_at?: string | null
  file_count: number
  dir_count?: number
  total_bytes: number
  /** Bytes actually uploaded for this run after dedup (new data this backup). */
  uploaded_bytes: number
  encrypted?: boolean
  pinned?: boolean
}

export type Entry = {
  path: string
  type: 'file' | 'dir' | 'symlink'
  size?: number
  mtime: string
  chunks?: string[]
  digest?: string
  link_target?: string
}

export type FileVersion = {
  snapshot: Snapshot
  entry: Entry
}

export type Usage = {
  logical_bytes: number
  stored_bytes: number
  chunk_count: number
  device_count: number
  snapshot_count: number
  quota_bytes: number
  dedup_ratio: number
  free_disk_bytes: number
}

export type ActivityEvent = {
  id?: number
  at: string
  level: string
  message: string
  path?: string
  reason?: string
  device_id?: string
  device_name?: string
}

export type Me = {
  id: string
  email: string
  is_admin: boolean
  quota_bytes: number
  retention_days: number
}

export type Bootstrap = {
  needs_setup: boolean
  authenticated: boolean
  version: string
  public_url?: string
}

export type Settings = {
  quota_bytes: number
  retention_days: number
  max_upload_bytes_per_sec: number
  require_encryption: boolean
  keep_daily: number
  keep_weekly: number
  keep_monthly: number
  alert_after_hours: number
}

export type BackupAlert = {
  id: string
  device_id?: string
  device_name?: string
  kind: string
  level: string
  message: string
  opened_at: string
}

export type IntegrityReport = {
  checked: number
  failed: number
  detail?: string
  started_at?: string
  finished_at?: string
}

export type DashboardSession = {
  id: string
  created_at: string
  expires_at: string
  user_agent: string
  current: boolean
}

export type IgnoreRule = { pattern: string; reason: string }
export type IgnoreRules = {
  categories: Record<string, IgnoreRule[]>
  project_markers: string[]
  max_file_size?: number
}

/** ApiError carries the server's message so the UI can show it verbatim. */
export class ApiError extends Error {
  status: number
  code?: string

  constructor(message: string, status: number, code?: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`/api/v1/ui${path}`, {
    ...init,
    headers: {
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...init?.headers,
    },
    credentials: 'same-origin',
  })

  if (response.status === 204) {
    return undefined as T
  }

  const text = await response.text()
  let body: Record<string, unknown> = {}
  if (text) {
    try {
      body = JSON.parse(text) as Record<string, unknown>
    } catch {
      if (!response.ok) {
        throw new ApiError(`Request failed (${response.status})`, response.status)
      }
      throw new ApiError('The server returned an unexpected response', response.status)
    }
  }

  if (!response.ok) {
    const code = typeof body.code === 'string' ? body.code : undefined
    // Session middleware uses invalid_token. Wrong password on /password is also 401
    // but without that code — do not bounce the user to the login gate for that.
    if (
      response.status === 401 &&
      code === 'invalid_token' &&
      path !== '/bootstrap' &&
      path !== '/login' &&
      path !== '/setup'
    ) {
      window.location.assign('/')
    }
    throw new ApiError(
      typeof body.error === 'string' ? body.error : `Request failed (${response.status})`,
      response.status,
      code,
    )
  }
  return body as T
}

type UserResponse = { user: Me }

export const api = {
  bootstrap: () => request<Bootstrap>('/bootstrap'),
  setup: (email: string, password: string) =>
    request<UserResponse>('/setup', { method: 'POST', body: JSON.stringify({ email, password }) }).then(
      (r) => r.user,
    ),
  login: (email: string, password: string) =>
    request<UserResponse>('/login', { method: 'POST', body: JSON.stringify({ email, password }) }).then(
      (r) => r.user,
    ),
  logout: () => request<void>('/logout', { method: 'POST' }),
  me: () => request<UserResponse>('/me').then((r) => r.user),
  changePassword: (current: string, next: string) =>
    request<void>('/password', {
      method: 'POST',
      body: JSON.stringify({ current, new: next }),
    }),

  devices: () => request<{ devices: Device[] }>('/devices').then((r) => r.devices ?? []),
  renameDevice: (id: string, name: string) =>
    request<void>(`/devices/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  removeDevice: (id: string) => request<void>(`/devices/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  sendCommand: (id: string, kind: string) =>
    request<void>(`/devices/${encodeURIComponent(id)}/commands`, {
      method: 'POST',
      body: JSON.stringify({ kind }),
    }),

  usage: () => request<Usage>('/usage'),

  snapshots: (deviceId?: string, limit = 200) =>
    request<{ snapshots: Snapshot[] }>(
      `/snapshots?limit=${limit}${deviceId ? `&device_id=${encodeURIComponent(deviceId)}` : ''}`,
    ).then((r) => r.snapshots ?? []),
  deleteSnapshot: (id: string) => request<void>(`/snapshots/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  pinSnapshot: (id: string, pinned: boolean) =>
    request<void>(`/snapshots/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify({ pinned }),
    }),
  searchSnapshot: (id: string, query: string) =>
    request<{ entries?: Entry[] }>(
      `/snapshots/${encodeURIComponent(id)}/search?q=${encodeURIComponent(query)}&limit=50`,
    ).then((r) => r.entries ?? []),

  alerts: () => request<{ alerts: BackupAlert[] }>('/alerts').then((r) => r.alerts ?? []),
  ackAlert: (id: string) => request<void>(`/alerts/${encodeURIComponent(id)}/ack`, { method: 'POST' }),

  sessions: () => request<{ sessions: DashboardSession[] }>('/sessions').then((r) => r.sessions ?? []),
  revokeSession: (id: string) => request<void>(`/sessions/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  integrity: () => request<IntegrityReport>('/integrity'),
  runIntegrity: () => request<IntegrityReport>('/integrity', { method: 'POST' }),
  browse: (id: string, prefix = '', cursor = '', limit = 200) =>
    request<{ entries?: Entry[]; next_cursor?: string; encrypted?: boolean }>(
      `/snapshots/${encodeURIComponent(id)}/browse?children=1&prefix=${encodeURIComponent(
        prefix,
      )}&cursor=${encodeURIComponent(cursor)}&limit=${limit}`,
    ).then((r) => {
      const extra = r as { entries?: Entry[]; next_cursor?: string }
      return {
        entries: r.entries ?? extra.entries ?? [],
        next_cursor: r.next_cursor ?? extra.next_cursor ?? '',
        encrypted: r.encrypted,
      }
    }),

  fileVersions: (path: string, deviceId?: string) =>
    request<{ path: string; versions: FileVersion[] }>(
      `/files/versions?path=${encodeURIComponent(path)}${deviceId ? `&device_id=${encodeURIComponent(deviceId)}` : ''}`,
    ).then((r) => r.versions ?? []),

  events: (limit = 100) => request<{ events: ActivityEvent[] }>(`/events?limit=${limit}`).then((r) => r.events ?? []),

  createJoinToken: (label: string) =>
    request<{ code: string; expires_at: string; server_url: string }>('/join-tokens', {
      method: 'POST',
      body: JSON.stringify({ label }),
    }),

  settings: () => request<Settings>('/settings'),
  updateSettings: (settings: Partial<Settings>) =>
    request<Settings>('/settings', { method: 'PUT', body: JSON.stringify(settings) }),

  ignoreRules: () => request<IgnoreRules>('/ignore-rules'),
}

/** downloadUrl builds a restore link the browser can follow directly. Kept
 * module-internal plus exported helpers below; callers should use the async
 * download functions so errors surface in the UI. */
function downloadUrl(snapshotId: string, path: string): string {
  return `/api/v1/ui/snapshots/${encodeURIComponent(snapshotId)}/download?path=${encodeURIComponent(path)}`
}

/** archiveUrl builds a link that streams a folder as a ZIP. */
function archiveUrl(snapshotId: string, prefix: string): string {
  return `/api/v1/ui/snapshots/${encodeURIComponent(snapshotId)}/archive?prefix=${encodeURIComponent(prefix)}`
}

/** downloadSnapshotFile fetches a file with credentials and surfaces API errors. */
export async function downloadSnapshotFile(snapshotId: string, path: string): Promise<void> {
  await saveDownload(downloadUrl(snapshotId, path), baseNameOf(path))
}

/** fetchSnapshotFile loads file bytes for in-browser preview. */
export async function fetchSnapshotFile(snapshotId: string, path: string): Promise<Blob> {
  const response = await fetch(`${downloadUrl(snapshotId, path)}&inline=1`, { credentials: 'same-origin' })
  if (!response.ok) {
    const text = await response.text()
    let message = `Could not open file (${response.status})`
    let code: string | undefined
    try {
      const body = JSON.parse(text) as { error?: string; code?: string }
      if (body.error) message = body.error
      code = body.code
    } catch {
      /* keep status message */
    }
    throw new ApiError(message, response.status, code)
  }
  return response.blob()
}

/** downloadSnapshotArchive fetches a folder ZIP with credentials. */
export async function downloadSnapshotArchive(snapshotId: string, prefix: string): Promise<void> {
  const name = prefix ? `${baseNameOf(prefix)}.zip` : 'backup.zip'
  await saveDownload(archiveUrl(snapshotId, prefix), name)
}

async function saveDownload(url: string, filename: string): Promise<void> {
  const response = await fetch(url, { credentials: 'same-origin' })
  if (!response.ok) {
    const text = await response.text()
    let message = `Download failed (${response.status})`
    let code: string | undefined
    try {
      const body = JSON.parse(text) as { error?: string; code?: string }
      if (body.error) message = body.error
      code = body.code
    } catch {
      /* keep status message */
    }
    throw new ApiError(message, response.status, code)
  }
  const blob = await response.blob()
  const objectUrl = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = objectUrl
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(objectUrl)
}

function baseNameOf(path: string): string {
  const parts = path.replace(/\/+$/, '').split('/').filter(Boolean)
  return parts[parts.length - 1] || 'download'
}
