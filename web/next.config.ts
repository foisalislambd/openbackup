import type { NextConfig } from 'next'

const isDev = process.env.NODE_ENV === 'development'

/**
 * The dashboard ships as a static export that is embedded into the Go server
 * binary. That is what keeps a self-hosted install to one binary and one data
 * directory: no Node runtime in production, no second container, and the UI is
 * served from the same origin as the API so the session cookie just works.
 *
 * Everything is rendered in the browser against the authenticated API, so
 * server-side rendering would buy nothing here.
 */
const nextConfig: NextConfig = {
  output: 'export',
  // The Go file server resolves /devices to devices.html itself, so the export
  // does not need directory-style URLs.
  trailingSlash: false,
  images: {
    // A static export cannot run the image optimiser, and the dashboard has no
    // photographs to optimise.
    unoptimized: true,
  },
  // Fail the build on a type error rather than shipping a broken dashboard
  // inside the server binary. Linting runs as its own step, since Next 16 no
  // longer wires ESLint into the build.
  typescript: { ignoreBuildErrors: false },

  // During `next dev` the API lives in a separate process, so proxy to it.
  // Rewrites do nothing in an export build, hence the guard.
  ...(isDev
    ? {
        async rewrites() {
          const server = process.env.OPENBACKUP_DEV_SERVER ?? 'http://localhost:18200'
          return [{ source: '/api/:path*', destination: `${server}/api/:path*` }]
        },
      }
    : {}),
}

export default nextConfig
