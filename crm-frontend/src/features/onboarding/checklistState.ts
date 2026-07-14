import { useSyncExternalStore } from 'react';

// Where the setup checklist's "is it showing?" state lives (U7.5).
//
// The thing this replaces was a ONE-SHOT blocking wizard: it appeared once, on a
// brand-new empty workspace, and the moment you dismissed it you could never get
// back to it. So the two flags here exist to make the successor RETURNABLE:
//
//   dismissed  — persisted (localStorage, per workspace). The user hid the card.
//   forceOpen  — in-memory, per session. The user asked for it back ("Setup guide"
//                in the account menu), which must win over `dismissed`, over the
//                server's onboarding_completed flag, and over "every step is done".
//
// Per WORKSPACE, not per user: the same person can be an established member of one
// org and the founder of an empty new one, and their setup state differs in each.
//
// There is no server field for this (and U7 adds no endpoint), so the persisted
// half is client-side by necessity. That's acceptable precisely because nothing is
// lost when it's wrong: a checklist that reappears on a new device is a mild
// annoyance, not data loss — and the card is one click from hidden again.

const KEY_PREFIX = 'setup_checklist_dismissed:';

/** The key the RETIRED welcome wizard wrote. Still honored as a suppressor so a
 *  user who dismissed that wizard isn't greeted by the checklist as if brand new. */
const LEGACY_KEY = 'onboarding_completed';

// Session-scoped: reopening is an act, not a preference — it shouldn't outlive the
// tab. Module scope (not React state) so the account menu can flip it for a card
// that lives on another route.
let forceOpen = false;

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

// Every read is guarded: localStorage throws in private-mode Safari and when a
// browser blocks storage for the origin. Failing to read a preference must never
// take the dashboard down with it.
function readFlag(key: string): boolean {
  try {
    return localStorage.getItem(key) === 'true';
  } catch {
    return false;
  }
}

export function isChecklistDismissed(orgId: string): boolean {
  return readFlag(KEY_PREFIX + orgId);
}

export function isLegacyOnboardingDone(): boolean {
  return readFlag(LEGACY_KEY);
}

/** Hide the card. Reversible: the account menu's "Setup guide" brings it back. */
export function dismissSetupChecklist(orgId: string) {
  try {
    localStorage.setItem(KEY_PREFIX + orgId, 'true');
  } catch {
    // Storage denied — the card stays hidden for this session via forceOpen=false
    // and simply comes back on the next visit. Better than a crash.
  }
  forceOpen = false;
  emit();
}

/** Bring the card back, whatever the persisted/server flags say. */
export function openSetupChecklist() {
  forceOpen = true;
  emit();
}

/** Test seam — resets the session flag between cases. */
export function resetSetupChecklistSession() {
  forceOpen = false;
  emit();
}

// One string snapshot (not an object): useSyncExternalStore compares with Object.is,
// so a fresh object every render would loop forever. Strings compare by value.
function getSnapshot(orgId: string): string {
  return `${isChecklistDismissed(orgId)}|${forceOpen}`;
}

export interface ChecklistVisibility {
  dismissed: boolean;
  forceOpen: boolean;
}

export function useChecklistVisibility(orgId: string): ChecklistVisibility {
  const snap = useSyncExternalStore(
    subscribe,
    () => getSnapshot(orgId),
    () => 'false|false',
  );
  const [dismissed, opened] = snap.split('|');
  return { dismissed: dismissed === 'true', forceOpen: opened === 'true' };
}
