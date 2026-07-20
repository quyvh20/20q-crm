// Hands the template picker across a hard navigation.
//
// Every inline workspace creator finishes with window.location.assign('/') — a
// full document load that destroys all in-memory state, so React state or a
// route param cannot carry "show the picker" across it. sessionStorage can, and
// it is scoped to the tab and cleared when the tab closes.
//
// Deliberately NOT set when ACCEPTING AN INVITATION: that joins a workspace
// someone else already configured, where offering to install a pipeline over
// their setup would be wrong. Only CREATING a workspace arms it.

const KEY = 'template_picker_pending';

/** Arm the picker for the next page load. Call before the hard navigation. */
export function markTemplatePickerPending(): void {
  try {
    sessionStorage.setItem(KEY, '1');
  } catch {
    // Private mode / storage disabled: the picker just doesn't auto-open. It is
    // still reachable from the setup checklist, so this is a soft degradation.
  }
}

/**
 * Read-and-clear. Consuming on read is what makes the picker show EXACTLY once —
 * a re-render, a manual refresh, or navigating back to the dashboard must not
 * reopen a modal the user already dismissed.
 */
export function consumeTemplatePickerPending(): boolean {
  try {
    if (sessionStorage.getItem(KEY) !== '1') return false;
    sessionStorage.removeItem(KEY);
    return true;
  } catch {
    return false;
  }
}
