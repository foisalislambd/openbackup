// Formatting helpers. Sizes and times are the two things this app shows most, and
// getting them wrong is how software starts to feel untrustworthy.

/** bytes renders a size the way a person would say it. */
export function bytes(n: number | undefined): string {
  if (!n || n < 0) return '0 B'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let value = n
  for (const unit of units) {
    value /= 1024
    if (value < 1024) {
      return `${value < 10 ? value.toFixed(1) : Math.round(value)} ${unit}`
    }
  }
  return `${value.toFixed(1)} EB`
}

/** rate renders a speed, with 0 meaning no limit rather than "stopped". */
export function rate(bytesPerSec: number): string {
  if (!bytesPerSec) return 'Unlimited'
  return `${bytes(bytesPerSec)}/s`
}

/** ago renders how long ago something happened, in words. */
export function ago(iso: string | undefined): string {
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return 'never'
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000))

  if (seconds < 60) return 'just now'
  const units: [number, string][] = [
    [60, 'minute'],
    [60, 'hour'],
    [24, 'day'],
    [30, 'month'],
    [12, 'year'],
  ]
  let value = seconds
  let label = 'second'
  for (const [size, name] of units) {
    if (value < size) break
    value = Math.floor(value / size)
    label = name
  }
  return `${value} ${label}${value === 1 ? '' : 's'} ago`
}

/** when renders an absolute date for lists, where "3 days ago" hides the ordering. */
export function when(iso: string | undefined): string {
  if (!iso) return '-'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** count renders a file count with thousands separators. */
export function count(n: number | undefined): string {
  return (n ?? 0).toLocaleString()
}

/** fileName takes the last segment of a snapshot path. */
export function fileName(path: string): string {
  const parts = path.split('/').filter(Boolean)
  return parts[parts.length - 1] ?? path
}

/** parentPath drops the last segment, for "up one level". */
export function parentPath(path: string): string {
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return parts.length ? `${parts.join('/')}/` : ''
}

/** parseRate accepts "5MB", "500 kb" or a plain number of bytes per second. */
export function parseRate(input: string): number | null {
  const text = input.trim().toUpperCase().replace(/\/S$/, '')
  if (text === '' || text === '0') return 0
  const match = text.match(/^([\d.]+)\s*(K|KB|M|MB|G|GB|B)?$/)
  if (!match) return null
  const value = Number(match[1])
  if (!Number.isFinite(value) || value < 0) return null
  const multipliers: Record<string, number> = {
    B: 1,
    K: 1024,
    KB: 1024,
    M: 1024 ** 2,
    MB: 1024 ** 2,
    G: 1024 ** 3,
    GB: 1024 ** 3,
  }
  return Math.round(value * (multipliers[match[2] ?? 'B'] ?? 1))
}
