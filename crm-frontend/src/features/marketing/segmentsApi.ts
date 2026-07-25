// M5 audiences API layer — saved segments (dynamic predicate or static list).
// apiFetch (bearer + credentials + 401→refresh) + parseJsonSafe/apiError are
// shared from lib/api; never re-add auth headers here.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export type SegmentType = 'static' | 'dynamic';

// SegmentFilter is the boolean AND/OR/NOT AST for a dynamic segment. It mirrors
// the backend domain.SegmentFilter exactly (lower-case group op; tag leaf takes
// precedence over a field leaf over a group). Exactly one kind per node.
export interface SegmentFilter {
  op?: 'and' | 'or' | 'not';
  rules?: SegmentFilter[];
  // field leaf
  field?: string;
  operator?: string;
  value?: unknown;
  // tag leaf
  tag_id?: string;
  tag_not?: boolean;
}

export interface Segment {
  id: string;
  org_id: string;
  name: string;
  type: SegmentType;
  definition: SegmentFilter;
  materialized: boolean;
  count_cached: number;
  count_cached_at?: string | null;
  refreshed_at?: string | null;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
  // Computed, read-only: set by the API when the caller's field access hides a field
  // this segment filters on — the definition is blanked and editing is blocked.
  definition_restricted?: boolean;
}

export interface SegmentInput {
  name: string;
  type: SegmentType;
  // definition is the AST for a dynamic segment; '{}' (or omitted) for a static list.
  definition?: SegmentFilter;
}

// SegmentFieldDescriptor is one whitelisted, already-FLS-filtered contact field the
// builder may filter on (mirrors the report field descriptor).
export interface SegmentFieldDescriptor {
  key: string;
  label: string;
  type: string;
  options?: string[];
}

export interface SegmentContactRow {
  id: string;
  name: string;
  email: string;
}

export interface SegmentPreview {
  contacts: SegmentContactRow[];
  count: number;
}

export async function listSegments(): Promise<Segment[]> {
  const res = await apiFetch('/api/marketing/segments');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load segments');
  return (json.data ?? []) as Segment[];
}

export async function getSegment(id: string): Promise<Segment> {
  const res = await apiFetch(`/api/marketing/segments/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load segment');
  return json.data as Segment;
}

export async function createSegment(input: SegmentInput): Promise<Segment> {
  const res = await apiFetch('/api/marketing/segments', {
    method: 'POST',
    body: JSON.stringify(input),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create segment');
  return json.data as Segment;
}

export async function updateSegment(id: string, input: SegmentInput): Promise<Segment> {
  const res = await apiFetch(`/api/marketing/segments/${id}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to save segment');
  return json.data as Segment;
}

export async function deleteSegment(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/segments/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to delete segment');
}

export async function previewSegment(id: string, limit = 50): Promise<SegmentPreview> {
  const res = await apiFetch(`/api/marketing/segments/${id}/preview?limit=${limit}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to preview segment');
  return json.data as SegmentPreview;
}

export async function countSegment(id: string): Promise<number> {
  const res = await apiFetch(`/api/marketing/segments/${id}/count`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to count segment');
  return (json.data?.count ?? 0) as number;
}

// previewDefinition dry-runs an UNSAVED dynamic definition (the builder's live
// count + sample). Nothing is persisted.
export async function previewDefinition(definition: SegmentFilter, limit = 25): Promise<SegmentPreview> {
  const res = await apiFetch('/api/marketing/segment-preview', {
    method: 'POST',
    body: JSON.stringify({ definition, limit }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to preview definition');
  return json.data as SegmentPreview;
}

export async function listSegmentFields(): Promise<SegmentFieldDescriptor[]> {
  const res = await apiFetch('/api/marketing/segment-fields');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load fields');
  return (json.data ?? []) as SegmentFieldDescriptor[];
}

export async function addStaticMembers(id: string, contactIds: string[]): Promise<number> {
  const res = await apiFetch(`/api/marketing/segments/${id}/members`, {
    method: 'POST',
    body: JSON.stringify({ contact_ids: contactIds }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to add members');
  return (json.data?.added ?? 0) as number;
}

export async function removeStaticMember(id: string, contactId: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/segments/${id}/members/${contactId}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to remove member');
}
