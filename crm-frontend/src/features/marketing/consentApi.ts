// R1: admin lawful-basis declaration + the org-level go-live preflight.
// apiFetch (bearer + credentials + 401→refresh) and parseJsonSafe/apiError are
// shared from lib/api — never re-add auth headers or a second refresh here.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';
import type { CheckItem } from './CheckList';

/** The bases an administrator may DECLARE on the org's behalf.
 *  Deliberately narrower than the full set of valid bases: `express` and
 *  `double_opt_in` are affirmative opt-ins that only the subscriber can give, and
 *  the backend rejects them because recording one would not make anyone mailable.
 *  The list is fetched rather than hardcoded so the picker can never offer a value
 *  the server will refuse. */
export interface GrantableBasis {
  value: string;
  requires_casl_expiry: boolean;
}

export const BASIS_LABELS: Record<string, string> = {
  existing_business_relationship: 'Existing business relationship',
  legitimate_interest: 'Legitimate interest',
  implied_transaction: 'Implied consent — transaction (CASL)',
  implied_inquiry: 'Implied consent — inquiry (CASL)',
};

export const BASIS_HELP: Record<string, string> = {
  existing_business_relationship:
    'A current or recent customer relationship. Standing basis — does not expire.',
  legitimate_interest:
    'A documented legitimate interest in receiving this marketing. Standing basis — does not expire.',
  implied_transaction:
    'CASL implied consent arising from a purchase or contract. Expires, so an expiry date is required.',
  implied_inquiry:
    'CASL implied consent arising from an enquiry. Expires, so an expiry date is required.',
};

export function basisLabel(v: string): string {
  return BASIS_LABELS[v] ?? v;
}

export interface GrantCounts {
  /** Every distinct normalized address the selection resolves to. */
  candidates: number;
  /** Rows the database actually wrote. Zero on a preview. */
  granted: number;
  /** Left alone because already unsubscribed or cleaned — a grant never
   *  resurrects an opt-out. */
  skipped: number;
  /** Carry a suppression entry: they may hold a basis but still will not be sent
   *  to, so "granted" must not be read as "mailable". */
  suppressed: number;
}

export interface ConsentGrantRequest {
  basis: string;
  source: string;
  segment_ids?: string[];
  exclude_segment_ids?: string[];
  contact_ids?: string[];
  casl_expires_at?: string | null;
}

export interface MarketingPreflight {
  ready: boolean;
  checks: CheckItem[];
  mailable_contacts: number;
  known_contacts: number;
}

export async function getGrantableBases(): Promise<GrantableBasis[]> {
  const res = await apiFetch('/api/marketing/consent/bases');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load consent bases');
  return (json.data ?? []) as GrantableBasis[];
}

export async function previewGrant(body: ConsentGrantRequest): Promise<GrantCounts> {
  const res = await apiFetch('/api/marketing/consent/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to preview the grant');
  return json.data as GrantCounts;
}

export async function grantLawfulBasis(body: ConsentGrantRequest): Promise<GrantCounts> {
  const res = await apiFetch('/api/marketing/consent/grant', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to record the lawful basis');
  return json.data as GrantCounts;
}

export async function getPreflight(): Promise<MarketingPreflight> {
  const res = await apiFetch('/api/marketing/preflight');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load marketing readiness');
  return json.data as MarketingPreflight;
}
