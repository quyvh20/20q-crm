import { lazy } from 'react';
import * as Sentry from '@sentry/react';

/**
 * Route-level code splitting, with the failure mode that route-level code
 * splitting actually has in production.
 *
 * This app is served from Cloudflare Pages and redeployed several times a day.
 * Every deploy emits new content-hashed asset filenames and drops the old ones.
 * A user who loaded the SPA before a deploy is holding an `index-<oldhash>.js`
 * whose lazy imports point at `Foo-<oldhash>.js`. The moment they navigate to a
 * route they had not visited yet, the browser requests a URL that no longer
 * exists, gets a 404 (in fact an HTML 404 body served with the wrong MIME type,
 * which fails the module's strict MIME check), and the dynamic import rejects.
 *
 * Before code splitting this could not happen — the whole app was one chunk that
 * was already in memory. Introducing `lazy()` introduces this failure, so the
 * handling ships with it rather than after it.
 *
 * The recovery is a reload, not a retry, because a reload is what fetches the
 * new `index.html` with the new hashes. The single retry in front of it exists
 * for the *other* cause — a genuinely flaky connection or an edge node that has
 * not warmed the asset yet — where a reload would be a heavier hammer than
 * needed. Note the retry is best-effort by design: browsers memoise module-map
 * failures per specifier, so on a hard 404 the second `load()` may resolve to
 * the same rejection without a second network request. It costs a tick and
 * sometimes wins; the reload is what is load-bearing.
 */

/** sessionStorage, not localStorage: the guard is per-tab, and must survive a reload. */
const RELOAD_MARKER = 'chunk-load:last-reload';

/**
 * A reload that does not fix the import must not reload again — that is an
 * infinite refresh loop, which is a far worse failure than the error screen it
 * is trying to avoid. Anything inside this window counts as "we already tried".
 */
const RELOAD_COOLDOWN_MS = 15_000;

/** Breathing room before the retry, so an instantaneous blip is not retried instantly. */
const RETRY_DELAY_MS = 250;

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

/**
 * Reload at most once per {@link RELOAD_COOLDOWN_MS}. Returns whether a reload
 * was actually started, so the caller knows whether to hang or to rethrow.
 *
 * Every storage access is guarded: Safari in private mode throws on
 * `sessionStorage` access outright, and a thrown guard must degrade to "do not
 * reload" (show the error) rather than to "reload unconditionally" (loop).
 */
function reloadOncePerWindow(): boolean {
  try {
    const last = Number(window.sessionStorage.getItem(RELOAD_MARKER)) || 0;
    if (Date.now() - last < RELOAD_COOLDOWN_MS) return false;
    window.sessionStorage.setItem(RELOAD_MARKER, String(Date.now()));
  } catch {
    // No usable sessionStorage means no loop guard, and an unguarded reload is
    // the one outcome worse than the error screen. Fail toward the error.
    return false;
  }
  window.location.reload();
  return true;
}

/**
 * Load a dynamically imported module, retrying once and then falling back to a
 * page reload for the stale-deploy case.
 *
 * Exported separately from {@link lazyRoute} so non-route dynamic imports can
 * reuse the same policy.
 */
export async function loadChunk<T>(load: () => Promise<T>): Promise<T> {
  try {
    return await load();
  } catch (firstError) {
    await sleep(RETRY_DELAY_MS);
    try {
      return await load();
    } catch (secondError) {
      Sentry.captureException(secondError, {
        tags: { chunk_load_failure: 'true' },
        extra: { firstError: String(firstError), reloading: true },
      });
      if (reloadOncePerWindow()) {
        // The document is being torn down. Resolving or rejecting here would
        // race the navigation and flash either a broken page or an error screen
        // the user cannot act on. Never settling keeps the Suspense fallback —
        // a spinner — on screen for the few ms until the reload takes over.
        return new Promise<T>(() => {});
      }
      // We already reloaded for this and it did not help, so this is a real
      // error (offline, asset genuinely gone, a throwing module). Let it reach
      // the error boundary, which offers a manual retry.
      throw secondError;
    }
  }
}

/**
 * Types derived from `React.lazy` itself rather than hand-written.
 *
 * `lazy` is declared as `<T extends ComponentType<any>>`, and spelling that
 * constraint out here would mean writing two explicit `any`s that
 * `@typescript-eslint/no-explicit-any` counts against the repo's warning
 * ratchet — for no gain, since route components take no props. Deriving the
 * types keeps this helper's signature automatically identical to the function
 * it wraps.
 */
type RouteLoader = Parameters<typeof lazy>[0];
type LazyRouteComponent = ReturnType<typeof lazy>;
type RouteComponent = Awaited<ReturnType<RouteLoader>>['default'];

/**
 * `React.lazy` with the retry/reload policy above applied.
 *
 * Usage is identical to `lazy()`:
 *   const ContactsPage = lazyRoute(() => import('./pages/ContactsPage'));
 *
 * The import specifier must stay a literal inside the arrow function so the
 * bundler can statically see it and emit a chunk — do not hoist it to a
 * variable.
 */
export function lazyRoute(load: RouteLoader): LazyRouteComponent {
  return lazy(() => loadChunk(load));
}

/**
 * Adapter for the modules that export their page as a NAMED export rather than
 * a default (NextBuilder, and SettingsLayout's index redirect). Keeps the call
 * site a one-liner instead of an inline `.then()` at every use.
 */
export function lazyRouteNamed<K extends string>(
  load: () => Promise<Record<K, RouteComponent>>,
  name: K,
): LazyRouteComponent {
  return lazy(() => loadChunk(load).then((mod) => ({ default: mod[name] })));
}
