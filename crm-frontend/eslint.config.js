import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'

export default defineConfig([
  // `dist` is the Vite build output; `.wrangler` is the Cloudflare Pages dev/deploy
  // cache (gitignored, but present on any machine that has run `wrangler`). ESLint
  // currently *walks* 9 bundle artifacts under `.wrangler/` and reports nothing only
  // because the single rule-bearing block below is scoped to `**/*.{ts,tsx}`. Ignore
  // them explicitly so that adding any unscoped block later cannot silently start
  // linting build output.
  globalIgnores(['dist', '.wrangler']),

  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
    ],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    rules: {
      // The repo already uses the conventions this configures, and `tsc` runs with
      // `noUnusedLocals` + `noUnusedParameters` (tsconfig.app.json), which is the
      // check that actually catches dead code — and which natively exempts
      // `_`-prefixed names and rest-omit siblings.
      //
      // Of the 13 baseline violations, 5 were LOAD-BEARING omissions that "fixing"
      // by deletion would have regressed (MessageBubble.tsx drops the hast `node`
      // so it is not spread onto a DOM <code>; workflows/store.ts strips UI-only
      // `_fieldMeta` before POST; api.ts strips pagination from export filters;
      // BlockPalette.tsx drops dnd-kit's onKeyDown so it cannot swallow the tile
      // click). `ignoreRestSiblings` + the ignore patterns exempt exactly those,
      // rather than deleting them. The remaining unused catch bindings were fixed
      // in source instead (`catch (e) {}` -> `catch { /* ... */ }`).
      //
      // NOTE: `caughtErrors` is deliberately left at the preset default ('all').
      // Setting it to 'none' would disable a sub-check with NO backstop — tsc's
      // noUnusedLocals does not report unused catch-clause bindings — and it is
      // not needed, because no such binding remains.
      '@typescript-eslint/no-unused-vars': ['error', {
        args: 'after-used',
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
        ignoreRestSiblings: true,
        destructuredArrayIgnorePattern: '^_',
      }],

      // ── RATCHETS, NOT AMNESTIES ────────────────────────────────────────────
      // Each rule below is a real signal we still want printed, but erroring on it
      // today would force either a wave of inline suppressions or a refactor that
      // buys no correctness. `npm run lint:ci` pins the warning ceiling, so these
      // can only ever go DOWN. Promote any of them back to 'error' the moment its
      // count reaches zero.

      // A pure Vite-HMR developer-experience heuristic: it cannot detect a runtime
      // bug, only a fast-refresh boundary that will full-reload instead. All 20
      // sites are genuine cross-file imports, several are the exact seam the unit
      // tests are written against (`canRunWorkflowNow`, `SETTINGS_SECTIONS`,
      // `buildStep`), `badgeVariants`/`buttonVariants` are shadcn's own generated
      // shape that project doctrine says to compose rather than fork, and
      // `useAuth` (src/lib/auth.tsx) is referenced by 46 files. Erroring means 20
      // permanent inline suppressions, or a refactor that buys zero correctness.
      //
      // `allowConstantExport: true` is repeated on purpose: it is set by the
      // plugin's own `configs.vite` preset, and writing a bare 'warn' here would
      // REPLACE the whole entry and silently revert the option to false. This is
      // a severity downgrade, not an options change.
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],

      // 33 sites, of which only ~1 in 6 is mechanically fixable. The rest are two
      // deliberate, already-commented patterns: parent-prefetch hydration and
      // prop -> local-draft seeding. React's own recommended escape hatch for both
      // ("adjust state during render") trips a sibling rule in this same preset,
      // so there is no in-preset fix. Erroring today would misrepresent the cost.
      'react-hooks/set-state-in-effect': 'warn',

      // Outside tests the residual includes cases where `any` is the documented
      // answer, not laziness — e.g. the two Zod `z.lazy` recursive schemas in
      // src/features/workflows/schemas.ts:15,72, where an explicit
      // `z.ZodType<...>` annotation IS the pattern Zod's own docs prescribe.
      '@typescript-eslint/no-explicit-any': 'warn',
    },
  },

  // Files that do not run in a browser: the Vite/Vitest config, the Cloudflare
  // Pages Function, the test setup and the specs themselves. Without this they are
  // linted with `globals.browser` alone, so `process`, `__dirname`, `Buffer` and
  // the Node timer globals read as undefined identifiers.
  {
    files: [
      'vite.config.ts',
      'playwright.config.ts',
      'functions/**/*.{ts,tsx}',
      'src/test/**/*.{ts,tsx}',
      '**/__tests__/**/*.{ts,tsx}',
      '**/*.test.{ts,tsx}',
    ],
    languageOptions: {
      globals: { ...globals.node, ...globals.browser },
    },
  },

  // Tests are allowed `any`. 14 of the 52 baseline violations are adversarial
  // tests deliberately feeding malformed data across a boundary (`role: 123 as
  // any`, truncated payloads); a test whose whole job is to pass junk cannot do
  // it while the rule holds.
  {
    files: ['**/__tests__/**/*.{ts,tsx}', '**/*.test.{ts,tsx}'],
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },

  // Playwright end-to-end suite (crm-frontend/e2e). E2E specs run in Node, export fixtures
  // and page objects from files that also export helpers (which is what
  // react-refresh objects to), and drive untyped browser evaluate() payloads.
  {
    files: ['e2e/**/*.{ts,tsx}'],
    languageOptions: {
      globals: { ...globals.node },
    },
    rules: {
      'react-refresh/only-export-components': 'off',
      '@typescript-eslint/no-explicit-any': 'off',
    },
  },
])
