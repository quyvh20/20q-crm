// PUBLIC (unauthenticated) one-click unsubscribe + preference-center API (M3).
//
// These calls MUST NOT use apiFetch: an anonymous token-bearer has no session, so
// apiFetch's 401→/login redirect would bounce them off the page. They are raw
// fetches to the public backend endpoint (mirrors resetPassword/verifyEmail in
// lib/api). The opaque path token is the only credential — never send a bearer.
import { API_URL } from '../../lib/api';

export interface PreferenceInfo {
  ok: boolean;
  orgName?: string;
  topics: { id: string; name: string; description?: string }[];
}

// fetchPreferenceInfo loads the minimal data the preference page renders. The
// backend deliberately returns NO per-recipient subscription state (a forwarded
// token must not become a subscription oracle).
export async function fetchPreferenceInfo(token: string): Promise<PreferenceInfo> {
  const res = await fetch(`${API_URL}/api/marketing/u/${encodeURIComponent(token)}`, {
    method: 'GET',
    headers: { Accept: 'application/json' },
  });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((json?.error as string) || 'This unsubscribe link is invalid or has expired.');
  }
  const data = json?.data ?? {};
  return {
    ok: Boolean(data.ok),
    orgName: data.org_name || undefined,
    topics: Array.isArray(data.topics) ? data.topics : [],
  };
}

// submitUnsubscribe writes the opt-out. With no topicId it is a GLOBAL unsubscribe;
// with a topicId it is a per-topic opt-down. Returns nothing meaningful (the
// response is a constant confirmation regardless of prior state).
export async function submitUnsubscribe(token: string, topicId?: string): Promise<void> {
  const res = await fetch(`${API_URL}/api/marketing/u/${encodeURIComponent(token)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(topicId ? { topic_id: topicId } : {}),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json?.error as string) || 'Could not process your request. Please try again.');
  }
}
