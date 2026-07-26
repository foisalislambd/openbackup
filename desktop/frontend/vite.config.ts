import { writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

/**
 * keepDist restores the placeholder Go embeds after every build.
 *
 * `//go:embed all:frontend/dist` needs the directory to exist even in a checkout
 * where the frontend has never been built, so a placeholder is committed. Vite
 * empties the directory on each build and takes the placeholder with it, which
 * would leave the tree unable to compile after a `git clean`.
 */
function keepDist(): Plugin {
  return {
    name: 'openbackup-keep-dist',
    closeBundle() {
      writeFileSync(
        resolve(import.meta.dirname, 'dist/.gitkeep'),
        '# The window is built here by `wails build`, which runs `npm run build`.\n' +
          '# This file only exists so //go:embed has a directory to embed in a fresh\n' +
          '# checkout; the build output itself is not committed.\n',
      )
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), keepDist()],
  build: {
    // Wails embeds this directory into the binary.
    outDir: 'dist',
    emptyOutDir: true,
    // The window loads from the embedded filesystem, so there is nothing to gain
    // from source maps in a release build.
    sourcemap: false,
  },
})
