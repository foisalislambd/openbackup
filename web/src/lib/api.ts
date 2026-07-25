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
  created_at: string
  last_seen?: string | null
  state: string
  state_reason?: string
  queued_files?: number
  queued_bytes?: number
  last_error?: string
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
  uploaded_bytes?: number
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
}

export type IgnoreRule = { pattern: string; reason: string }
export type IgnoreRules = {
  categories: Record<string, IgnoreRule[]>
  project_markers: string[]
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
    // Same-origin cookies carry the session.
    credentials: 'same-origin',
  })

  if (response.status === 204) {
    return undefined as T
  }

  const text = await response.text()
  const body = text ? JSON.parse(text) : {}

  if (!response.ok) {
    throw new ApiError(body.error ?? `Request failed (${response.status})`, response.status, body.code)
  }
  return body as T
}

export const api = {
  bootstrap: () => request<Bootstrap>('/bootstrap'),
  setup: (email: string, password: string) =>
    request<Me>('/setup', { method: 'POST', body: JSON.stringify({ email, password }) }),
  login: (email: string, password: string) =>
    request<Me>('/login', { method: 'POST', body: JSON.stringify({ email, password }) }),
  logout: () => request<void>('/logout', { method: 'POST' }),
  me: () => request<Me>('/me'),
  changePassword: (current: string, next: string) =>
    request<void>('/password', {
      method: 'POST',
      body: JSON.stringify({ current_password: current, new_password: next }),
    }),

  devices: () => request<{ devices: Device[] }>('/devices').then((r) => r.devices ?? []),
  renameDevice: (id: string, name: string) =>
    request<void>(`/devices/${id}`, { method: 'PATCH', body: JSON.stringify({ name }) }),
  removeDevice: (id: string) => request<void>(`/devices/${id}`, { method: 'DELETE' }),
  sendCommand: (id: string, kind: string) =>
    request<void>(`/devices/${id}/commands`, { method: 'POST', body: JSON.stringify({ kind }) }),

  usage: () => request<Usage>('/usage'),
  history: (days = 30) => request<{ points: { date: string; bytes: number }[] }>(`/history?days=${days}`),

  snapshots: (deviceId?: string) =>
    request<{ snapshots: Snapshot[] }>(`/snapshots${deviceId ? `?device_id=${deviceId}` : ''}`).then(
      (r) => r.snapshots ?? [],
    ),
  snapshot: (id: string) => request<Snapshot>(`/snapshots/${id}`),
  deleteSnapshot: (id: string) => request<void>(`/snapshots/${id}`, { method: 'DELETE' }),
  browse: (id: string, prefix = '', cursor = '', limit = 200) =>
    request<{ entries: Entry[]; next_cursor: string }>(
      `/snapshots/${id}/browse?prefix=${encodeURIComponent(prefix)}&cursor=${encodeURIComponent(
        cursor,
      )}&limit=${limit}`,
    ),

  events: (limit = 100) => request<{ events: ActivityEvent[] }>(`/events?limit=${limit}`).then((r) => r.events ?? []),

  joinTokens: () => request<{ tokens: { id: string; label: string; expires_at: string; used: boolean }[] }>(
    '/join-tokens',
  ),
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

/** downloadUrl builds a restore link the browser can follow directly. */
export function downloadUrl(snapshotId: string, path: string): string {
  return `/api/v1/ui/snapshots/${snapshotId}/download?path=${encodeURIComponent(path)}`
}

/** archiveUrl builds a link that streams a folder as a ZIP. */
export function archiveUrl(snapshotId: string, prefix: string): string {
  return `/api/v1/ui/snapshots/${snapshotId}/archive?prefix=${encodeURIComponent(prefix)}`
}
