// These mirror the Go types in internal/agent/control and internal/api.
//
// They are written by hand rather than taken from Wails' generated bindings: the
// generated files are build output, and a hand-written contract fails the type
// check the moment the Go side changes shape, which is exactly when we want to
// hear about it.

/** How the backup is doing, and everything the home screen needs. */
export type Health =
  | 'protected'
  | 'working'
  | 'paused'
  | 'stale'
  | 'error'
  | 'never_run'
  | 'not_connected'
  | 'agent_stopped'

export interface Overview {
  connected: boolean
  server_url: string
  device_name: string
  device_id: string
  version: string
  platform: string

  agent_running: boolean
  health: Health
  headline: string
  detail: string

  state: string
  paused: boolean
  pause_reason?: string
  current_path?: string
  files_done: number
  files_total: number
  bytes_done: number
  last_error?: string

  tracked_files: number
  tracked_bytes: number

  last_backup_at?: string
  last_backup_size: number
  last_backup_files: number
  snapshot_count: number
  server_error?: string

  encrypted: boolean
  folder_count: number
  missing_folders: number
}

export interface Folder {
  name: string
  label: string
  path: string
  enabled: boolean
  detected: boolean
  exists: boolean
}

export interface ConnectRequest {
  server_url: string
  code: string
  device_name?: string
  encrypt: boolean
  recovery_code?: string
}

export interface ConnectResult {
  device_name: string
  server_url: string
  folders: Folder[]
  recovery_code?: string
}

export interface Settings {
  upload_bytes_per_sec: number
  max_cpu_percent: number
  pause_on_metered: boolean
  pause_on_battery: boolean
  pause_while_fullscreen: boolean
  full_scan_minutes: number
  encrypted: boolean
  recovery_code?: string
}

export interface Snapshot {
  id: string
  device_id: string
  device_name?: string
  kind: string
  status: string
  parent_id?: string
  started_at: string
  completed_at?: string
  file_count: number
  dir_count: number
  total_bytes: number
  uploaded_bytes: number
}

export interface Entry {
  path: string
  type: 'file' | 'dir' | 'symlink'
  size?: number
  mtime: string
}

export interface Page {
  entries: Entry[]
  next_cursor?: string
}

export interface RestoreRequest {
  snapshot: string
  path: string
  target: string
  conflict: 'skip' | 'overwrite' | 'rename'
  dry_run: boolean
}

export interface RestoreProgress {
  running: boolean
  target: string
  path: string
  files: number
  of_files: number
  bytes: number
  current: string
  finished: boolean
  error?: string
  restored: number
  skipped: number
  failed: number
}

export interface Check {
  name: string
  ok: boolean
  detail: string
}

export interface AppInfo {
  version: string
  platform: string
  config_path: string
  log_path: string
}
