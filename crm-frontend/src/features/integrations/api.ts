import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';
import type {
  CreateSourceInput,
  CreatedLeadSource,
  FieldMap,
  IntegrationEvent,
  LeadSource,
  MappingView,
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

export async function listSources(): Promise<LeadSource[]> {
  const res = await apiFetch(BASE);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load lead sources');
  return asList<LeadSource>(json.data);
}

export async function getSource(id: string): Promise<LeadSource> {
  const res = await apiFetch(`${BASE}/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load the lead source');
  return json.data as LeadSource;
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
  return { source: source as LeadSource, plaintext_key: key };
}

export async function updateSource(id: string, input: UpdateSourceInput): Promise<LeadSource> {
  const res = await apiFetch(`${BASE}/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update the lead source');
  return json.data as LeadSource;
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
  return { source: source as LeadSource, plaintext_key: key };
}

export async function listEvents(id: string, limit = 50): Promise<IntegrationEvent[]> {
  const res = await apiFetch(`${BASE}/${id}/events?limit=${limit}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load the delivery log');
  return asList<IntegrationEvent>(json.data);
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
  return json.data as LeadSource;
}
