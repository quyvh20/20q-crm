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
  build: {
    rolldownOptions: {
      output: {
        // Vite 8 is rolldown-based. `rollupOptions.output.manualChunks` — the
        // option every code-splitting guide reaches for — is NOT read here:
        // `rollupOptions` is deprecated in favour of `rolldownOptions`, and
        // rolldown's replacement for manualChunks is `output.codeSplitting`
        // (`advancedChunks` is its deprecated predecessor). Writing manualChunks
        // would be silently ignored: a config that reads as present and does
        // nothing.
        //
        // NOTE ON WHAT THIS DOES AND DOES NOT BUY. Grouping moves ZERO bytes off
        // the first load — that is entirely the work of the lazy() split in
        // App.tsx. All this does is stop rolldown emitting one chunk per shared
        // module, which had the entry pulling 20 separate scripts, thirteen of
        // them under 2 kB. Gzip actively loses on files that small (a 110-byte
        // icon module compresses to 130 bytes). Groups are kept deliberately
        // narrow: a broad `test: /node_modules/` group would merge @xyflow,
        // recharts and TipTap into a chunk the entry statically references and
        // silently undo the entire split.
        codeSplitting: {
          groups: [
            {
              // The framework floor. Every route needs it, it is already in the
              // first load, and it only changes on a dependency upgrade — so as
              // one chunk it is both fewer requests AND a long-lived cache entry
              // that survives the several app deploys a day this repo does.
              name: 'react-vendor',
              test: /node_modules[\\/](react|react-dom|scheduler|react-router|react-router-dom)[\\/]/,
              priority: 20,
            },
            {
              // The shadcn primitives. Small, universally used, and already
              // scattered across ~8 sub-2 kB chunks in the first load.
              name: 'ui',
              test: /src[\\/]components[\\/]ui[\\/]/,
              priority: 10,
            },
          ],
        },
      },
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
