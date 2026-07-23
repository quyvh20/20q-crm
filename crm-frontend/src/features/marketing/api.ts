// Marketing API layer (M1: suppression & consent ledger). apiFetch (bearer +
// credentials + 401→refresh) and parseJsonSafe/apiError are shared from lib/api —
// never re-add auth headers or a second refresh here.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export interface Suppression {
  id: string;
  email: string;
  reason: string;
  scope: string;
  topic_id?: string | null;
  source: string;
  soft_bounce_count: number;
  created_at: string;
}

export interface SuppressionListResponse {
  data: Suppression[];
  meta: { total: number };
}

export interface MarketingStatus {
  email: string;
  suppressed: boolean;
  suppression_reasons: string[];
  sendable_marketing: boolean;
  not_sendable_reason: string;
  marketing_status: string; // '' when no state row exists
  consent_basis: string;    // '' when unset
}

export interface SuppressionListParams {
  q?: string;
  reason?: string;
  limit?: number;
  offset?: number;
}

export async function listSuppressions(params: SuppressionListParams = {}): Promise<SuppressionListResponse> {
  const qs = new URLSearchParams();
  if (params.q) qs.set('q', params.q);
  if (params.reason) qs.set('reason', params.reason);
  if (params.limit) qs.set('limit', String(params.limit));
  if (params.offset) qs.set('offset', String(params.offset));
  const res = await apiFetch(`/api/marketing/suppressions?${qs.toString()}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load suppressions');
  return {
    data: (json.data ?? []) as Suppression[],
    meta: (json.meta ?? { total: 0 }) as { total: number },
  };
}

export interface AddSuppressionInput {
  email: string;
  reason?: string;
  scope?: string;
  source?: string;
}

export async function addSuppression(input: AddSuppressionInput): Promise<{ suppression: Suppression; already: boolean }> {
  const res = await apiFetch('/api/marketing/suppressions', {
    method: 'POST',
    body: JSON.stringify(input),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to add suppression');
  return { suppression: json.data as Suppression, already: !!json.already_suppressed };
}

export async function removeSuppression(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/suppressions/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to remove suppression');
}

// getMarketingStatus posts the email in the BODY (never a URL/query param) so the
// address stays out of access logs.
export async function getMarketingStatus(email: string): Promise<MarketingStatus> {
  const res = await apiFetch('/api/marketing/status', {
    method: 'POST',
    body: JSON.stringify({ email }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load marketing status');
  return json.data as MarketingStatus;
}

// SUPPRESSION_REASONS drives the admin add form + filter. Labels are user-facing.
export const SUPPRESSION_REASONS: { value: string; label: string }[] = [
  { value: 'manual', label: 'Manual (do not market)' },
  { value: 'unsubscribe', label: 'Unsubscribe' },
  { value: 'complaint', label: 'Spam complaint' },
  { value: 'hard_bounce', label: 'Hard bounce' },
  { value: 'soft_bounce', label: 'Soft bounce' },
  { value: 'gdpr_erasure', label: 'GDPR erasure' },
];

export function reasonLabel(value: string): string {
  return SUPPRESSION_REASONS.find((r) => r.value === value)?.label ?? value;
}
