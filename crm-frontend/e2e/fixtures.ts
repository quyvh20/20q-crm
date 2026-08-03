import { test as base, expect, type Page } from '@playwright/test';

/**
 * Shared fixtures for the end-to-end smoke suite.
 *
 * ── Two rules every spec in this directory obeys ─────────────────────────────
 *
 * 1. NEVER `waitForLoadState('networkidle')`. AppLayout mounts a notification
 *    stream that opens a PERMANENT streaming `fetch('/api/events')` — with
 *    auto-reconnect — on every authenticated page. The network is never idle
 *    while signed in, so networkidle does not fire late: it does not fire at
 *    all, and the test dies on the navigation timeout. Use `expect(locator)`,
 *    which auto-waits and retries.
 *
 * 2. NEVER assert on a toast. There is no shared toast primitive; roughly a
 *    dozen pages hand-roll `setTimeout(() => setToast(null), 3000..4000)`.
 *    Whether the toast is still mounted when the assertion runs is a race
 *    against a wall-clock timer, which is the definition of a flaky test.
 *    Assert on the durable consequence instead — the row in the table, the
 *    URL, the state that persists.
 *
 * ── The auth-request budget (measured, and the reason for the navigation style)
 *
 * The whole /api/auth group sits behind a fixed-window IP limiter of 30 requests
 * per minute (authIPRateLimit in internal/delivery/http/ratelimit.go). It is
 * easy to blow through that without noticing, because a FULL PAGE LOAD is not
 * one auth request — it is two: the app boots, calls POST /api/auth/refresh to
 * rehydrate the session from the httpOnly cookie, and React StrictMode
 * double-invokes that effect in dev, which is exactly how CI serves the SPA.
 *
 * So each `page.goto()` of an app route costs 2 against the budget. Measured:
 * with every spec doing goto('/login') → sign in → goto(target), one suite run
 * consumed 21 of the 30 — and a single CI retry would have tipped it over,
 * producing 429s that read as random flake.
 *
 * The fix is the navigation style below: sign in once per test, then reach the
 * target route by CLICKING THE SIDEBAR. A client-side SPA navigation re-boots
 * nothing, so it costs zero auth requests — and it happens to exercise the
 * navigation a real user performs. Prefer that over `page.goto()` for any route
 * behind the login.
 *
 * Note also that a shared/pre-baked `storageState` is NOT a valid shortcut
 * here: refresh tokens ROTATE on every use, and the server treats a replayed
 * (already-rotated) token as theft — it revokes every session for the user.
 * Two tests restoring the same saved cookie would trigger exactly that.
 */

/**
 * Credentials are supplied by whoever brought the stack up — the CI workflow
 * generates a throwaway pair per run, and a developer exports their own local
 * seed values. There is deliberately NO default: a fallback credential in a
 * repository this size becomes a de-facto shared secret (see
 * crm-backend/scripts/lib/seedenv.js, which fails closed for the same reason
 * after a real leak). Failing loudly costs one env var and cannot leak.
 */
function credentials(): { email: string; password: string } {
  const email = process.env.E2E_EMAIL;
  const password = process.env.E2E_PASSWORD;
  if (!email || !password) {
    throw new Error(
      'E2E_EMAIL and E2E_PASSWORD must both be set — there is no default.\n' +
        'They must match the account seeded by crm-backend/scripts/seed_local_account.js\n' +
        'against the stack under test. See .github/workflows/e2e.yml for the full recipe.',
    );
  }
  return { email, password };
}

/**
 * Signs in through the real form at /login.
 *
 * Deliberately the UI and not an API call. The login form is the single most
 * load-bearing screen in the product, and driving it here means the smoke suite
 * proves the whole path — form submit, token exchange, the httpOnly refresh
 * cookie, and the authenticated shell rendering — rather than proving that a
 * fixture can mint a token.
 *
 * The account itself is seeded over the API rather than registered through the
 * UI on purpose: RegisterPage sets a sessionStorage flag that makes the starter
 * template modal auto-open once on the dashboard, which would sit on top of the
 * first assertion of whichever spec happened to run first.
 */
export async function login(page: Page): Promise<void> {
  const { email, password } = credentials();

  await page.goto('/login');

  // Stable ids, verified in src/pages/LoginPage.tsx.
  await page.locator('#login-email').fill(email);
  await page.locator('#login-password').fill(password);
  await page.getByRole('button', { name: 'Sign In' }).click();

  // PublicRoute bounces an authenticated user to '/'. Asserting on the
  // authenticated shell (the sidebar) rather than only the URL means a login
  // that "succeeds" into a broken app still fails here.
  await expect(page.getByRole('link', { name: 'Dashboard' })).toBeVisible();
}

/**
 * Navigates to an app route by clicking its sidebar link — a client-side SPA
 * navigation, which costs nothing against the auth-request budget described
 * above (unlike `page.goto`, which re-boots the app).
 *
 * `exact` is required: the dashboard's setup checklist renders its own links to
 * the same destinations under longer names ("Import your contacts — done"),
 * which a substring match would also select.
 */
export async function openFromSidebar(page: Page, linkName: string): Promise<void> {
  await page.getByRole('link', { name: linkName, exact: true }).click();
}

/**
 * `appPage` is a Page that has already signed in. Every journey spec except the
 * login spec itself takes it, so authentication is expressed once.
 *
 * Each test gets its own fresh browser context (Playwright's default), so each
 * signs in for itself. That is deliberate — see the storageState note above —
 * and it keeps every test independently runnable.
 */
export const test = base.extend<{ appPage: Page }>({
  // The second argument is Playwright's "hand the value to the test" callback.
  // It is conventionally called `use`, but it is positional, so it is named
  // `runTest` here: `react-hooks/rules-of-hooks` sees a bare `use(...)` call
  // inside a non-component, non-hook function and reports it as React 19's
  // `use` hook being called illegally. Renaming avoids the false positive
  // without an eslint-disable comment, and without relaxing the rule for the
  // whole directory.
  appPage: async ({ page }, runTest) => {
    await login(page);
    await runTest(page);
  },
});

export { expect };
