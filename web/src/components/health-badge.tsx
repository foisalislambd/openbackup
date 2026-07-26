import { Badge } from './ui'

export function HealthBadge({ health, state }: { health: string; state?: string }) {
  switch (health) {
    case 'ok':
      return <Badge tone="good">{state === 'uploading' ? 'backing up' : 'healthy'}</Badge>
    case 'stale':
      return <Badge tone="warn">out of date</Badge>
    case 'error':
      return <Badge tone="bad">error</Badge>
    default:
      return <Badge>no backup yet</Badge>
  }
}
