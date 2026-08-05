// PUBLIC (unauthenticated) double-opt-in confirm API (R9). Mirrors
// publicUnsubApi.ts exactly, including the "no apiFetch" rule: an anonymous
// token-bearer has no session, so apiFetch's 401→/login redirect would bounce
// them off the page.
import { API_URL } from '../../lib/api';

export interface ConfirmInfo {
  ok: boolean;
  orgName?: string;
}

export async function fetchConfirmInfo(token: string): Promise<ConfirmInfo> {
  const res = await fetch(`${API_URL}/api/marketing/confirm/${encodeURIComponent(token)}`, {
    method: 'GET',
    headers: { Accept: 'application/json' },
  });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((json?.error as string) || 'This confirmation link is invalid or has expired.');
  }
  const data = json?.data ?? {};
  return { ok: Boolean(data.ok), orgName: data.org_name || undefined };
}

export async function submitConfirm(token: string): Promise<void> {
  const res = await fetch(`${API_URL}/api/marketing/confirm/${encodeURIComponent(token)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json?.error as string) || 'Could not confirm your subscription. Please try again.');
  }
}
