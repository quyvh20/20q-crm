/**
 * relativeTime renders an ISO timestamp as a coarse "how long ago".
 *
 * Promoted out of IntegrationsSection, which is no longer the only screen that
 * needs it — the source detail page shows the same "last lead" fact, and two
 * copies of this would eventually disagree about what an absent timestamp reads
 * as. Deliberately coarse (m / h / d, no seconds and no calendar dates): the
 * question it answers is "is this thing still alive", not "when exactly".
 *
 * Callers that want a domain-specific phrase for the absent case should test the
 * timestamp themselves rather than pattern-match on "Never".
 */
export function relativeTime(iso?: string): string {
  if (!iso) return 'Never';
  const then = new Date(iso).getTime();
  if (Number.isNaN(then)) return 'Never';
  const mins = Math.floor((Date.now() - then) / 60000);
  if (mins < 1) return 'Just now';
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return `${Math.floor(hrs / 24)}d ago`;
}
