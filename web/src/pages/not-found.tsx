import { Link } from 'react-router-dom'

export default function NotFoundPage() {
  return (
    <div className="py-10 text-center">
      <p className="text-sm font-medium">Page not found</p>
      <p className="mt-1 text-sm text-[var(--color-ink-muted)]">
        That URL is not part of the dashboard.{' '}
        <Link className="text-[var(--color-brand)] underline" to="/">
          Back to overview
        </Link>
      </p>
    </div>
  )
}
