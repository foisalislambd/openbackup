import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// The dashboard shows this when the API has not been stamped (a `go run`
// without -ldflags). Release builds stamp the same VERSION.md into the binary,
// and the sidebar prefers that runtime value.
function readReleaseVersion(): string {
  const file = resolve(import.meta.dirname, '../VERSION.md')
  let text: string
  try {
    text = readFileSync(file, 'utf8')
  } catch (err) {
    throw new Error(`OpenBackup version file not found at ${file}`, { cause: err })
  }
  if (text.charCodeAt(0) === 0xfeff) text = text.slice(1)
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const bare = line.replace(/^[vV]/, '')
    if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$/.test(bare)) {
      throw new Error(`VERSION.md has invalid version ${line}`)
    }
    return `v${bare}`
  }
  throw new Error(`VERSION.md (${file}) has no version`)
}

const appVersion = readReleaseVersion()

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  define: {
    'import.meta.env.VITE_OPENBACKUP_VERSION': JSON.stringify(appVersion),
  },
  resolve: {
    alias: {
      '@': resolve(import.meta.dirname, 'src'),
    },
  },
  build: {
    // Copied into internal/server/web/dist by `make web` / scripts/build.ps1
    // and then embedded into the Go server binary.
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
  },
  server: {
    // During `vite` the API lives in a separate process, so proxy to it.
    proxy: {
      '/api': {
        target: process.env.OPENBACKUP_DEV_SERVER ?? 'http://127.0.0.1:18200',
        changeOrigin: true,
      },
    },
  },
})
