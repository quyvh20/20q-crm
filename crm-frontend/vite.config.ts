/// <reference types="vitest" />
import path from "path"
import { defineConfig, configDefaults } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    proxy: {
      // Same-origin dev topology: the SPA calls a relative /api/... and Vite
      // forwards it, which mirrors the production Cloudflare Pages Function
      // proxy exactly (first-party auth cookies, no CORS preflight).
      //
      // The target is overridable so CI — and any developer whose 8080 is
      // already taken — can point the proxy at another backend without editing
      // this file. The default is unchanged.
      '/api': {
        target: process.env.VITE_PROXY_TARGET ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    // The Playwright end-to-end suite lives in ./e2e and its specs are named
    // *.spec.ts, which vitest's default `include` also matches. Without this,
    // `vitest run` collects them, fails to resolve @playwright/test's runner
    // and reports 4 failed test FILES on an otherwise green suite. The two
    // runners are separate: `vitest run` for unit tests, `playwright test` for
    // e2e. Spread the defaults rather than replacing them so node_modules/dist
    // stay excluded too.
    exclude: [...configDefaults.exclude, 'e2e/**'],
  },
})
