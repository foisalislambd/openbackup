/** Formatting helpers shared by every view. */

/** bytes renders a size the way a person would say it. */
export function bytes(n?: number | null): string {
  if (n === undefined || n === null) return '—'
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

/**
 * relative renders a timestamp as "4 minutes ago".
 *
 * On a backup dashboard the age of the last backup matters far more than its
 * exact clock time, so that is what is shown first.
 */
export function relative(iso?: string | null): string {
  if (!iso) return 'never'
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return 'never'
  const seconds = Math.round((Date.now() - then) / 1000)
  if (seconds < 0) return 'just now'
  if (seconds < 60) return 'just now'
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes} minute${minutes === 1 ? '' : 's'} ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours} hour${hours === 1 ? '' : 's'} ago`
  const days = Math.round(hours / 24)
  if (days < 30) return `${days} day${days === 1 ? '' : 's'} ago`
  const months = Math.round(days / 30)
  if (months < 12) return `${months} month${months === 1 ? '' : 's'} ago`
  return `${Math.round(months / 12)} year${months < 18 ? '' : 's'} ago`
}

/** absolute renders a full local timestamp for tooltips and details. */
export function absolute(iso?: string | null): string {
  if (!iso) return '—'
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

/** count renders a number with thousands separators. */
export function count(n?: number | null): string {
  if (n === undefined || n === null) return '—'
  return n.toLocaleString()
}

/** platformLabel turns a platform id into something readable. */
export function platformLabel(platform: string): string {
  switch (platform) {
    case 'windows':
      return 'Windows'
    case 'darwin':
      return 'macOS'
    case 'linux':
      return 'Linux'
    case 'android':
      return 'Android'
    case 'ios':
      return 'iOS'
    default:
      return platform
  }
}

/** parentPath returns the folder containing a snapshot path. */
export function parentPath(path: string): string {
  const trimmed = path.replace(/\/+$/, '')
  const index = trimmed.lastIndexOf('/')
  return index <= 0 ? '' : trimmed.slice(0, index)
}

/** baseName returns the last segment of a path. */
export function baseName(path: string): string {
  const trimmed = path.replace(/\/+$/, '')
  const index = trimmed.lastIndexOf('/')
  return index < 0 ? trimmed : trimmed.slice(index + 1)
}
