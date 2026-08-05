import { useSyncExternalStore } from 'react';

// The app's ONE transient-notification store (R7.4).
//
// Before this, eleven pages hand-rolled the same thing: `useState` + a bare
// `setTimeout(..., 3000..4000)` + a copy-pasted `fixed right-4 top-4` div — and
// TWO of them (CampaignComposerPage, SegmentEditorPage) had grown a private
// `useToast()` hook with byte-identical bodies. Four defects were common to all
// of them, and all four are fixed here rather than eleven times:
//
//   1. The live region was mounted WITH its content (`{toast && <div
//      role="status">…}`). A screen reader only announces mutations to a region
//      that was already in the accessibility tree, so inserting the region and
//      its text in one commit is routinely announced as nothing at all. The
//      regions in <Toaster> are mounted permanently and empty; only their
//      CHILDREN change.
//   2. State lived in the page component, so a toast fired by a mutation that
//      also navigates (or that unmounts the page) died before it was read.
//      Module scope outlives any route.
//   3. The dismiss timer was never cleared, so a page unmounting mid-flight
//      left a setState aimed at a dead component.
//   4. Auto-dismiss was the ONLY exit — no way to close early, and no way to
//      re-read a message you missed. Every toast now carries a close control.
//
// Deliberately NOT a React context/provider. Call sites are `useToast()` inside
// components, but the state is a module-level external store, which means a page
// under unit test can fire a toast without every one of its tests being wrapped
// in a provider — and it means the store survives the <Suspense> fallback that
// R6's lazy routes swap in on navigation (a provider mounted inside the
// suspended subtree would be torn down along with the toast). Mirrors
// features/onboarding/checklistState.ts, which is the same shape for the same
// reason.

export type ToastTone = 'success' | 'error';

/** Optional control rendered inside the toast (e.g. "View run"). */
export interface ToastAction {
  label: string;
  onClick: () => void;
}

export interface Toast {
  id: number;
  message: string;
  tone: ToastTone;
  action?: ToastAction;
}

export interface ShowToastOptions {
  tone?: ToastTone;
  action?: ToastAction;
  /** Override the auto-dismiss delay, in ms. 0 keeps it up until dismissed. */
  durationMs?: number;
}

// An error is a thing the user has to act on, and it is usually longer prose
// than "Saved"; an actionable toast has to stay long enough to be clicked. Both
// get the long delay, plain confirmations the short one. (These numbers are
// invisible to the E2E suite by design — it never asserts on a toast, only on
// the durable consequence behind it.)
const SHORT_MS = 4000;
const LONG_MS = 6000;

// Enough to see a burst from a bulk action without papering over the page.
// Oldest falls off the top.
const MAX_VISIBLE = 3;

// One frozen empty array for the server snapshot: useSyncExternalStore compares
// with Object.is, so a fresh [] per call would loop forever.
const EMPTY: readonly Toast[] = Object.freeze([]);

let toasts: readonly Toast[] = EMPTY;
let nextId = 1;

const timers = new Map<number, ReturnType<typeof setTimeout>>();
const listeners = new Set<() => void>();

function emit() {
  listeners.forEach((l) => l());
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

function getSnapshot(): readonly Toast[] {
  return toasts;
}

function clearTimer(id: number) {
  const t = timers.get(id);
  if (t !== undefined) {
    clearTimeout(t);
    timers.delete(id);
  }
}

/** Remove one toast. Safe to call for an id that has already gone. */
export function dismissToast(id: number) {
  clearTimer(id);
  const next = toasts.filter((t) => t.id !== id);
  // Reference equality matters: bail out rather than emit a no-op re-render.
  if (next.length === toasts.length) return;
  toasts = next;
  emit();
}

/**
 * Raise a toast. Callable from anywhere — an event handler, a react-query
 * callback, a module with no component around it.
 *
 * Returns the id so a caller can dismiss early (e.g. a long-running job that
 * replaces "Publishing…" with the outcome).
 */
export function showToast(message: string, opts: ShowToastOptions = {}): number {
  const { tone = 'success', action } = opts;
  const id = nextId++;
  const toast: Toast = { id, message, tone, ...(action ? { action } : {}) };

  const overflow = [...toasts, toast];
  // Drop from the front, clearing the timers of anything evicted so a stale
  // callback cannot fire against an id that is no longer on screen.
  const kept = overflow.slice(Math.max(0, overflow.length - MAX_VISIBLE));
  for (const t of overflow) {
    if (!kept.includes(t)) clearTimer(t.id);
  }
  toasts = kept;

  const duration = opts.durationMs ?? (tone === 'error' || action ? LONG_MS : SHORT_MS);
  if (duration > 0) {
    timers.set(
      id,
      setTimeout(() => dismissToast(id), duration),
    );
  }

  emit();
  return id;
}

/** Test seam — drops every toast and its pending timer between cases. */
export function resetToasts() {
  timers.forEach((t) => clearTimeout(t));
  timers.clear();
  toasts = EMPTY;
  nextId = 1;
  emit();
}

/** Subscription for <Toaster>. Components that only RAISE toasts want useToast. */
export function useToasts(): readonly Toast[] {
  return useSyncExternalStore(subscribe, getSnapshot, () => EMPTY);
}

export interface ToastApi {
  /** `show('Saved')` · `show(msg, { tone: 'error' })` · `show(msg, { action })` */
  show: (message: string, opts?: ShowToastOptions) => number;
  /** Convenience for the overwhelmingly common error path. */
  error: (message: string) => number;
  dismiss: (id: number) => void;
}

// Stable across renders (module-level functions), so it is safe in a dependency
// array without memoising at every call site.
const API: ToastApi = {
  show: showToast,
  error: (message: string) => showToast(message, { tone: 'error' }),
  dismiss: dismissToast,
};

/** The call-site hook: `const { show } = useToast()`. */
export function useToast(): ToastApi {
  return API;
}
