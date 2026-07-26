import { resolve } from 'node:path'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
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
