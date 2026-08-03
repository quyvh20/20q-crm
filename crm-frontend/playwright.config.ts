import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright configuration for the end-to-end smoke suite.
 *
 * Deliberately NO `webServer` block. The stack this suite drives is four moving
 * parts — Postgres (pgvector), Redis, the Go API and the Vite dev server — and
 * the API cannot start until golang-migrate has built the schema. Encoding that
 * ordering in a `webServer` array would duplicate, badly, what
 * .github/workflows/e2e.yml already orchestrates, and would leave the local and
 * CI paths free to drift. Bring the stack up first, then run the suite against
 * it.
 *
 * The suite assumes a same-origin topology: the SPA is served by Vite with an
 * EMPTY `VITE_API_URL` so its API base resolves to '' and every call is a
 * relative /api/... that Vite proxies to the backend. That mirrors the
 * production Cloudflare Pages Function proxy exactly — first-party auth cookies,
 * no CORS preflight — so an auth bug that only manifests cross-origin cannot
 * hide here.
 */
export default defineConfig({
  testDir: './e2e',

  // A journey here is "drive the real UI against a real Go API": a cold Vite
  // route compile plus a schema fetch plus a list fetch. 60s is generous for a
  // green run and still fails fast when something is genuinely wedged.
  timeout: 60_000,
  expect: {
    // Every wait in this suite is an auto-waiting `expect(locator)` (see the
    // networkidle ban in the specs), so this is the real "how long may the app
    // take to show me a thing" budget.
    timeout: 15_000,
  },

  // `test.only` left in a spec silently shrinks the suite to one test while
  // still reporting green. In CI that is a false pass, so make it an error.
  forbidOnly: !!process.env.CI,

  // One retry in CI absorbs genuine infrastructure flake (a cold container, a
  // dropped connection) without hiding a deterministic failure — a real bug
  // fails both attempts. Locally, zero: a flake should be visible immediately.
  retries: process.env.CI ? 1 : 0,

  // Single worker on purpose. Every spec authenticates as the SAME seeded user
  // in the SAME org, and two of them WRITE (a contact, a marketing content
  // row). Parallel workers would interleave those writes into each other's list
  // assertions — the classic "passes alone, fails in the suite" flake. The
  // whole suite is four specs; serialising costs seconds and buys determinism.
  workers: 1,
  fullyParallel: false,

  // Everything Playwright generates lands under one `*.local` directory, which
  // crm-frontend/.gitignore already ignores. Playwright's defaults
  // (`test-results/` and `playwright-report/`) are NOT ignored by this repo, so
  // leaving them would drop a pile of traces, screenshots and videos into
  // `git status` after every local run.
  outputDir: './e2e-artifacts.local/test-results',
  reporter: [
    ['list'],
    ['html', { open: 'never', outputFolder: './e2e-artifacts.local/report' }],
  ],

  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5173',

    // The app composes Radix primitives and tailwindcss-animate, so almost
    // every panel, modal and dropdown mounts behind a transition. Reduced
    // motion collapses those, which removes an entire class of "clicked while
    // the element was still flying in" flake.
    //
    // It MUST be nested under `contextOptions`. `reducedMotion` is not a member
    // of Playwright's `use` options — a bare `reducedMotion: 'reduce'` here is
    // accepted silently by the runner and does nothing, so the mitigation reads
    // as present while every transition still plays. (`tsc` catches it, which is
    // why e2e/ and this file are in tsconfig.node.json's `include`.)
    contextOptions: { reducedMotion: 'reduce' },

    // Artifacts on the retry only: a green first attempt produces nothing, and
    // a failure that survives the retry arrives with a full trace attached.
    trace: 'on-first-retry',
    video: 'on-first-retry',
    // `screenshot` has no 'on-first-retry' setting; 'only-on-failure' is its
    // nearest equivalent and captures the final failing attempt.
    screenshot: 'only-on-failure',

    actionTimeout: 15_000,
    navigationTimeout: 30_000,
  },

  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        // Wide enough that the marketing email builder renders its full
        // three-pane editor: below 768px CampaignContentEditor deliberately
        // degrades to a read-only preview and hides the toolbar the marketing
        // spec drives.
        viewport: { width: 1440, height: 900 },
      },
    },
  ],
});
