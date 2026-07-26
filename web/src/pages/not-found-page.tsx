import { Link } from 'react-router-dom'

export default function NotFoundPage() {
  return (
    <div className="py-16 text-center">
      <p className="text-sm font-semibold text-gray-900 dark:text-white">Page not found</p>
      <p className="mt-1 text-sm text-gray-500">
        That URL is not part of the dashboard.{' '}
        <Link className="font-semibold text-brand-500 hover:underline" to="/">
          Back to home
        </Link>
      </p>
    </div>
  )
}
