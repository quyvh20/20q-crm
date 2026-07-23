// Marketing sending-domain API layer (M2). Shares apiFetch/parseJsonSafe/apiError
// from lib/api — never re-add auth headers.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export interface DomainDNSRecord {
  record: string; // "SPF" | "DKIM" | "Tracking"
  name: string;
  type: string; // "MX" | "TXT" | "CNAME"
  ttl: string;
  status: string;
  value: string;
  priority?: number;
}

export interface EmailDomain {
  id: string;
  org_id: string;
  domain: string;
  resend_domain_id: string;
  region: string;
  send_subdomain: string;
  tracking_subdomain?: string | null;
  return_path: string;
  status: string; // not_started | pending | verified | partially_verified | partially_failed | failed | temporary_failure
  spf_verified: boolean;
  dkim_verified: boolean;
  dmarc_policy?: string | null; // none | quarantine | reject | null
  dns_records: DomainDNSRecord[];
  verified_at?: string | null;
  last_checked_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface DomainListResponse {
  data: EmailDomain[];
  meta: { can_bulk_send: boolean; reason: string };
}

export async function listDomains(): Promise<DomainListResponse> {
  const res = await apiFetch('/api/marketing/domains');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load sending domains');
  return {
    data: (json.data ?? []) as EmailDomain[],
    meta: (json.meta ?? { can_bulk_send: false, reason: 'no_domain' }) as DomainListResponse['meta'],
  };
}

export async function addDomain(input: { domain: string; tracking_subdomain?: string }): Promise<EmailDomain> {
  const res = await apiFetch('/api/marketing/domains', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to add domain');
  return json.data as EmailDomain;
}

export async function verifyDomain(id: string): Promise<EmailDomain> {
  const res = await apiFetch(`/api/marketing/domains/${id}/verify`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to trigger verification');
  return json.data as EmailDomain;
}

export async function refreshDomain(id: string): Promise<EmailDomain> {
  const res = await apiFetch(`/api/marketing/domains/${id}/refresh`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to re-check domain');
  return json.data as EmailDomain;
}

export async function removeDomain(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/domains/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to remove domain');
}

// Reason codes from the backend CanBulkSend summary → friendly copy.
export function sendingReasonLabel(reason: string): string {
  switch (reason) {
    case 'no_domain': return 'No sending domain added yet.';
    case 'spf_unverified': return 'SPF is not verified yet.';
    case 'dkim_unverified': return 'DKIM is not verified yet.';
    case 'dmarc_missing': return 'A DMARC record (p=none or stricter) is not published yet.';
    default: return '';
  }
}
