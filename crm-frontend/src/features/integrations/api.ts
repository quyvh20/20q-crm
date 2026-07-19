import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';
import type {
  CreateSourceInput,
  CreatedLeadSource,
  FieldMap,
  IntegrationEvent,
  LeadSource,
  MappingView,
  TestLeadResult,
  UpdateSourceInput,
} from './types';

// The integrations API layer. Every call goes through lib/api's apiFetch (bearer +
// credentials + the single-flight 401→refresh) and parseJsonSafe (an HTML error
// page from a proxy must not surface as "Unexpected token '<'"). The backend
// envelope is {data} on success and {error: "message"} on failure; apiError
// unwraps both that and the {error:{message}} shape, so never hand-read json.error.

const BASE = '/api/integrations/sources';

/**
 * asList coerces a possibly-null `data` into an array.
 *
 * Go marshals a nil slice to `null`, not `[]` — so an org with no sources yet
 * returns data:null, and a bare `.map` on it white-screens the whole page under
 * the app-wide error boundary. This repo has already shipped that bug once.
 */
function asList<T>(data: unknown): T[] {
  return Array.isArray(data) ? (data as T[]) : [];
}

/**
 * asSource normalizes a source's array fields.
 *
 * Go marshals a nil slice to `null`, so owner_pool arrives null for every source
 * created before rotations existed — and a bare `.map` on it white-screens the whole
 * settings page under the app-wide error boundary. This repo has shipped that bug.
 */
function asSource(raw: unknown): LeadSource {
  const s = (raw ?? {}) as LeadSource;
  return {
    ...s,
    owner_pool: Array.isArray(s.owner_pool) ? s.owner_pool : [],
    owner_pool_inactive: Array.isArray(s.owner_pool_inactive) ? s.owner_pool_inactive : [],
  };
}

export async function listSources(): Promise<LeadSource[]> {
  const res = await apiFetch(BASE);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load lead sources');
  return asList<LeadSource>(json.data).map(asSource);
}

export async function getSource(id: string): Promise<LeadSource> {
  const res = await apiFetch(`${BASE}/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load the lead source');
  return asSource(json.data);
}

/**
 * createSource returns the source AND its plaintext key — the only time the key
 * exists. The caller must keep it in component state and drop it on dismiss;
 * putting it in a query cache would leave it in memory for the whole session.
 */
export async function createSource(input: CreateSourceInput): Promise<CreatedLeadSource> {
  const res = await apiFetch(BASE, { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create the lead source');
  const { plaintext_key: key, ...source } = json.data as LeadSource & { plaintext_key: string };
  return { source: asSource(source), plaintext_key: key };
}

export async function updateSource(id: string, input: UpdateSourceInput): Promise<LeadSource> {
  const res = await apiFetch(`${BASE}/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update the lead source');
  return asSource(json.data);
}

export async function deleteSource(id: string): Promise<void> {
  const res = await apiFetch(`${BASE}/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to delete the lead source');
}

/** rotateKey mints a new credential and invalidates the old one immediately. */
export async function rotateKey(id: string): Promise<CreatedLeadSource> {
  const res = await apiFetch(`${BASE}/${id}/rotate-key`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to rotate the key');
  const { plaintext_key: key, ...source } = json.data as LeadSource & { plaintext_key: string };
  return { source: asSource(source), plaintext_key: key };
}

export async function listEvents(id: string, limit = 50): Promise<IntegrationEvent[]> {
  const res = await apiFetch(`${BASE}/${id}/events?limit=${limit}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load the delivery log');
  return asList<IntegrationEvent>(json.data);
}

/**
 * sendTestLead drives a made-up lead through the real ingest pipeline.
 *
 * No body: the server builds the payload and the identity, so there is no wire on
 * which a caller could hand it an identity or an is_test flag.
 *
 * No timeoutMs, deliberately. The server's ingest context is detached from the
 * request, so aborting here would not stop the write — it would only hide it, and the
 * admin's natural retry would then be a genuine second concurrent pipeline.
 */
export async function sendTestLead(id: string): Promise<TestLeadResult> {
  const res = await apiFetch(`${BASE}/${id}/test-lead`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to send the test lead');
  const data = (json.data ?? {}) as Partial<TestLeadResult>;
  return {
    ...(data as TestLeadResult),
    // Go marshals nil slices to null; a bare .map on either would white-screen.
    quarantined: asList<string>(data.quarantined),
    uncovered: asList<string>(data.uncovered),
  };
}

export async function getMapping(id: string): Promise<MappingView> {
  const res = await apiFetch(`${BASE}/${id}/mapping`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load the field mapping');
  const data = (json.data ?? {}) as Partial<MappingView>;
  // Go marshals nil slices to null; a bare .map on either would white-screen.
  return {
    observed: Array.isArray(data.observed) ? data.observed : [],
    target_fields: Array.isArray(data.target_fields) ? data.target_fields : [],
    field_map: data.field_map ?? {},
  };
}

/**
 * saveMapping stores the field map. The server validates it against the target
 * object and returns per-source-key problems in `details` on a 400 — a mapping
 * that can never work should fail here, in front of the admin, not silently
 * quarantine every lead later.
 */
export async function saveMapping(id: string, field_map: FieldMap): Promise<LeadSource> {
  const res = await apiFetch(`${BASE}/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ field_map }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to save the field mapping');
  return asSource(json.data);
}
