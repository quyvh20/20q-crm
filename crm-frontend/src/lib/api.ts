const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

// ── Session token handling (P2) ─────────────────────────────────────────────
// The access token lives in memory only — never localStorage — so an XSS can't
// steal a durable session. The refresh token is an httpOnly cookie the JS can't
// read; on a 401 we transparently refresh against it. Auth calls send
// credentials so the cookie rides along.

let accessToken: string | null = null;

export function setAccessToken(token: string | null): void {
  accessToken = token;
}
export function getAccessToken(): string | null {
  return accessToken;
}

// readCsrfToken pulls the readable csrf_token cookie for the double-submit header
// the server checks on /refresh and /logout.
export function readCsrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : '';
}

// refreshAccessToken performs a single-flight cookie-based refresh. Concurrent
// 401s MUST share one /refresh call — two parallel refreshes would race, and the
// second would present a just-rotated cookie and trip the server's reuse
// detection, nuking the session. Resolves to the new access token, or null when
// the session is truly gone. Optionally re-scopes to a workspace.
let refreshInFlight: Promise<string | null> | null = null;

export function refreshAccessToken(orgId?: string): Promise<string | null> {
  if (!refreshInFlight) {
    refreshInFlight = (async (): Promise<string | null> => {
      try {
        const res = await fetch(`${API_URL}/api/auth/refresh`, {
          method: 'POST',
          credentials: 'include',
          headers: {
            'Content-Type': 'application/json',
            'X-CSRF-Token': readCsrfToken(),
          },
          body: JSON.stringify(orgId ? { org_id: orgId } : {}),
        });
        if (!res.ok) return null;
        const json = await res.json();
        const token = (json?.data?.access_token as string) ?? null;
        setAccessToken(token);
        return token;
      } catch {
        return null;
      } finally {
        refreshInFlight = null;
      }
    })();
  }
  return refreshInFlight;
}

async function apiFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const buildHeaders = (): Record<string, string> => {
    const headers: Record<string, string> = {
      ...(options.headers as Record<string, string> || {}),
    };
    const token = getAccessToken();
    if (token) headers['Authorization'] = `Bearer ${token}`;
    // Don't set Content-Type for FormData (browser sets the boundary itself).
    if (!(options.body instanceof FormData)) headers['Content-Type'] = 'application/json';
    return headers;
  };

  let res = await fetch(`${API_URL}${path}`, { ...options, headers: buildHeaders(), credentials: 'include' });

  if (res.status === 401) {
    const newToken = await refreshAccessToken();
    if (newToken) {
      res = await fetch(`${API_URL}${path}`, { ...options, headers: buildHeaders(), credentials: 'include' });
    } else {
      setAccessToken(null);
      window.location.href = '/login';
    }
  }

  return res;
}

// authStreamFetch is the streaming counterpart to apiFetch: it attaches the
// in-memory bearer token, sends credentials, and transparently retries once
// after a single-flight refresh on 401. Returns the raw Response so the caller
// can read the SSE body. (P2)
async function authStreamFetch(path: string, init: RequestInit): Promise<Response> {
  const build = (): RequestInit => {
    const headers: Record<string, string> = { ...(init.headers as Record<string, string> || {}) };
    const token = getAccessToken();
    if (token) headers['Authorization'] = `Bearer ${token}`;
    return { ...init, headers, credentials: 'include' };
  };
  let res = await fetch(`${API_URL}${path}`, build());
  if (res.status === 401) {
    const newToken = await refreshAccessToken();
    if (newToken) res = await fetch(`${API_URL}${path}`, build());
  }
  return res;
}

// ============================================================
// Contact types
// ============================================================

export interface Contact {
  id: string;
  org_id: string;
  first_name: string;
  last_name: string;
  email?: string;
  phone?: string;
  company_id?: string;
  owner_user_id?: string;
  custom_fields: Record<string, unknown>;
  created_at: string;
  updated_at: string;
  company?: { id: string; name: string; industry?: string };
  owner?: { id: string; first_name: string; last_name: string };
  tags?: { id: string; name: string; color: string }[];
}

export interface ContactFilter {
  q?: string;
  cursor?: string;
  limit?: number;
  company_id?: string;
  owner_user_id?: string;
  tag_ids?: string[];
  semantic?: boolean;
}

export interface CursorMeta {
  next_cursor?: string;
  has_more: boolean;
  total: number;
}

export interface ImportResult {
  created: number;
  skipped: number;
  errors: number;
  error_details?: string[];
}

// ============================================================
// Contact API functions
// ============================================================

export async function getContacts(filter: ContactFilter = {}) {
  const params = new URLSearchParams();
  if (filter.q) params.set('q', filter.q);
  if (filter.cursor) params.set('cursor', filter.cursor);
  if (filter.limit) params.set('limit', String(filter.limit));
  if (filter.company_id) params.set('company_id', filter.company_id);
  if (filter.owner_user_id) params.set('owner_user_id', filter.owner_user_id);
  if (filter.tag_ids?.length) params.set('tag_ids', filter.tag_ids.join(','));
  if (filter.semantic) params.set('semantic', 'true');

  const res = await apiFetch(`/api/contacts?${params.toString()}`);
  const json = await res.json();
  return { contacts: json.data as Contact[], meta: json.meta as CursorMeta };
}

export async function getContact(id: string) {
  const res = await apiFetch(`/api/contacts/${id}`);
  const json = await res.json();
  return json.data as Contact;
}

export async function createContact(data: Partial<Contact> & { tag_ids?: string[] }) {
  const res = await apiFetch('/api/contacts', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create contact');
  return json.data as Contact;
}

export async function updateContact(id: string, data: Partial<Contact> & { tag_ids?: string[] }) {
  const res = await apiFetch(`/api/contacts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update contact');
  return json.data as Contact;
}

export async function deleteContact(id: string) {
  const res = await apiFetch(`/api/contacts/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete contact');
  }
}

export async function importContacts(file: File, conflictMode: 'skip' | 'overwrite' = 'skip') {
  const formData = new FormData();
  formData.append('file', file);
  const res = await apiFetch(`/api/contacts/import?conflict_mode=${conflictMode}`, {
    method: 'POST',
    body: formData,
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Import failed');
  return json.data as ImportResult;
}

export interface BulkActionResult {
  affected: number;
  message: string;
}

export async function bulkAction(
  action: 'delete' | 'assign_tag',
  contactIds: string[],
  tagId?: string,
): Promise<BulkActionResult> {
  const res = await apiFetch('/api/contacts/bulk-action', {
    method: 'POST',
    body: JSON.stringify({ action, contact_ids: contactIds, tag_id: tagId ?? null }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Bulk action failed');
  return json.data as BulkActionResult;
}

// ============================================================
// Companies and Tags
// ============================================================

export interface Company {
  id: string;
  name: string;
  industry?: string;
}

export interface Tag {
  id: string;
  name: string;
  color: string;
}

export async function getCompanies() {
  const res = await apiFetch('/api/companies');
  const json = await res.json();
  return json.data as Company[];
}

export async function getTags() {
  const res = await apiFetch('/api/tags');
  const json = await res.json();
  return json.data as Tag[];
}

// ============================================================
// Pipeline Stages
// ============================================================

export interface PipelineStage {
  id: string;
  org_id: string;
  name: string;
  position: number;
  color: string;
  is_won: boolean;
  is_lost: boolean;
}

export async function getStages(): Promise<PipelineStage[]> {
  const res = await apiFetch('/api/pipeline/stages');
  const json = await res.json();
  return json.data as PipelineStage[];
}

export async function createStage(data: Partial<PipelineStage>) {
  const res = await apiFetch('/api/pipeline/stages', { method: 'POST', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create stage');
  return json.data as PipelineStage;
}

export async function updateStage(id: string, data: Partial<PipelineStage>) {
  const res = await apiFetch(`/api/pipeline/stages/${id}`, { method: 'PUT', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update stage');
  return json.data as PipelineStage;
}

export async function deleteStage(id: string) {
  const res = await apiFetch(`/api/pipeline/stages/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete stage');
  }
}

export async function seedDefaultStages(): Promise<PipelineStage[]> {
  const res = await apiFetch('/api/pipeline/stages/seed-defaults', { method: 'POST' });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to seed default stages');
  return json.data as PipelineStage[];
}


// ============================================================
// Deals
// ============================================================

export interface Deal {
  id: string;
  org_id: string;
  title: string;
  contact_id?: string;
  company_id?: string;
  stage_id?: string;
  value: number;
  probability: number;
  owner_user_id?: string;
  expected_close_at?: string;
  is_won: boolean;
  is_lost: boolean;
  closed_at?: string;
  created_at: string;
  updated_at: string;
  contact?: { id: string; first_name: string; last_name: string; email?: string };
  company?: { id: string; name: string };
  stage?: PipelineStage;
  owner?: { id: string; first_name: string; last_name: string };
}

export interface DealFilter {
  q?: string;
  stage_id?: string;
  owner_user_id?: string;
  cursor?: string;
  limit?: number;
}

export async function getDeals(filter: DealFilter = {}) {
  const params = new URLSearchParams();
  if (filter.q) params.set('q', filter.q);
  if (filter.stage_id) params.set('stage_id', filter.stage_id);
  if (filter.cursor) params.set('cursor', filter.cursor);
  if (filter.limit) params.set('limit', String(filter.limit));
  const res = await apiFetch(`/api/deals?${params.toString()}`);
  const json = await res.json();
  return { deals: json.data as Deal[], meta: json.meta as CursorMeta };
}

export async function getDeal(id: string): Promise<Deal> {
  const res = await apiFetch(`/api/deals/${id}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch deal');
  return json.data as Deal;
}

export interface CreateDealFormInput {
  title: string;
  contact_id?: string;
  company_id?: string;
  stage_id?: string;
  value?: number;
  probability?: number;
  expected_close_at?: string;
}

export async function createDeal(data: CreateDealFormInput): Promise<Deal> {
  const res = await apiFetch('/api/deals', { method: 'POST', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create deal');
  return json.data as Deal;
}

export async function updateDeal(id: string, data: Partial<Deal>): Promise<Deal> {
  const res = await apiFetch(`/api/deals/${id}`, { method: 'PUT', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update deal');
  return json.data as Deal;
}

export async function deleteDeal(id: string) {
  const res = await apiFetch(`/api/deals/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete deal');
  }
}

export async function changeDealStage(dealId: string, stageId: string, lostReason?: string): Promise<Deal> {
  const res = await apiFetch(`/api/deals/${dealId}/stage`, {
    method: 'PATCH',
    body: JSON.stringify({ stage_id: stageId, lost_reason: lostReason }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to change stage');
  return json.data as Deal;
}

// ============================================================
// Forecast
// ============================================================

export interface ForecastRow {
  month: string;
  expected_revenue: number;
  deals_count: number;
}

export async function getForecast(): Promise<ForecastRow[]> {
  const res = await apiFetch('/api/pipeline/forecast');
  const json = await res.json();
  return (json.data || []) as ForecastRow[];
}

// ============================================================
// Activities
// ============================================================

export interface Activity {
  id: string;
  org_id: string;
  type: string;
  deal_id?: string;
  contact_id?: string;
  user_id?: string;
  title?: string;
  body?: string;
  duration_minutes?: number;
  occurred_at: string;
  sentiment?: string;
  created_at: string;
}

export async function getActivities(filter: { deal_id?: string; contact_id?: string }): Promise<Activity[]> {
  const params = new URLSearchParams();
  if (filter.deal_id) params.set('deal_id', filter.deal_id);
  if (filter.contact_id) params.set('contact_id', filter.contact_id);
  const res = await apiFetch(`/api/activities?${params.toString()}`);
  const json = await res.json();
  return (json.data || []) as Activity[];
}

export async function createActivity(data: {
  type: string;
  deal_id?: string;
  contact_id?: string;
  title: string;
  body?: string;
  duration_minutes?: number;
  occurred_at?: string;
}): Promise<Activity> {
  const res = await apiFetch('/api/activities', { method: 'POST', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create activity');
  return json.data as Activity;
}

// ============================================================
// Tasks
// ============================================================

export interface Task {
  id: string;
  org_id: string;
  title: string;
  deal_id?: string;
  contact_id?: string;
  assigned_to?: string;
  due_at?: string;
  completed_at?: string;
  priority: string;
  created_at: string;
  updated_at: string;
}

export interface TaskFilter {
  deal_id?: string;
  contact_id?: string;
  assigned_to?: string;
  completed?: boolean;
}

export async function getTasks(filter: TaskFilter = {}): Promise<Task[]> {
  const params = new URLSearchParams();
  if (filter.deal_id) params.set('deal_id', filter.deal_id);
  if (filter.contact_id) params.set('contact_id', filter.contact_id);
  if (filter.assigned_to) params.set('assigned_to', filter.assigned_to);
  if (filter.completed !== undefined) params.set('completed', String(filter.completed));
  const res = await apiFetch(`/api/tasks?${params.toString()}`);
  const json = await res.json();
  return (json.data || []) as Task[];
}

export async function createTask(data: {
  title: string;
  deal_id?: string;
  contact_id?: string;
  assigned_to?: string;
  due_at?: string;
  priority?: string;
}): Promise<Task> {
  const res = await apiFetch('/api/tasks', { method: 'POST', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create task');
  return json.data as Task;
}

export async function updateTask(id: string, data: Partial<{
  title: string;
  assigned_to: string;
  due_at: string;
  priority: string;
  completed: boolean;
}>): Promise<Task> {
  const res = await apiFetch(`/api/tasks/${id}`, { method: 'PUT', body: JSON.stringify(data) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update task');
  return json.data as Task;
}

export async function deleteTask(id: string) {
  const res = await apiFetch(`/api/tasks/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete task');
  }
}

// ============================================================
// Users (for assignee dropdowns)
// ============================================================

export interface UserListItem {
  id: string;
  first_name: string;
  last_name: string;
  email: string;
}

export async function getUsers(): Promise<UserListItem[]> {
  const res = await apiFetch('/api/users');
  const json = await res.json();
  return (json.data || []) as UserListItem[];
}

// ============================================================
// AI
// ============================================================

export interface AIUsage {
  used_tokens: number;
  limit_tokens: number;
  reset_at: string;
  percent: number;
}

export async function getAIUsage(): Promise<AIUsage> {
  const res = await apiFetch('/api/ai/usage');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch AI usage');
  return json.data as AIUsage;
}

export async function streamChat(
  message: string,
  onChunk: (chunk: string) => void,
  onDone: () => void,
  onError: (err: string) => void,
  contextId?: string,
) {
  const res = await authStreamFetch('/api/ai/chat', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ message, context_id: contextId }),
  });

  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    onError(json.error || 'AI unavailable');
    return;
  }

  const reader = res.body?.getReader();
  if (!reader) { onError('No stream body'); return; }

  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() ?? '';
    for (const line of lines) {
      if (!line.startsWith('data: ')) continue;
      const data = line.slice(6);
      if (data === '[DONE]') { onDone(); return; }
      onChunk(data);
    }
  }
  onDone();
}

export async function composeEmail(
  instruction: string,
  tone: string,
  onChunk: (chunk: string) => void,
  onDone: () => void,
  onError: (err: string) => void,
  contactId?: string,
  dealId?: string,
) {
  const res = await authStreamFetch('/api/ai/email/compose', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
    body: JSON.stringify({ instruction, tone, contact_id: contactId, deal_id: dealId }),
  });

  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    onError(json.error || 'AI unavailable');
    return;
  }

  const reader = res.body?.getReader();
  if (!reader) { onError('No stream body'); return; }

  const decoder = new TextDecoder();
  let buffer = '';

  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() ?? '';
    for (const line of lines) {
      if (!line.startsWith('data: ')) continue;
      const data = line.slice(6);
      if (data === '[DONE]') { onDone(); return; }
      try {
        const textChunk = JSON.parse(data);
        if (typeof textChunk === 'string') {
          onChunk(textChunk);
        }
      } catch (e) {
        // Parsing error, ignore invalid chunks.
      }
    }
  }
  onDone();
}

// Async Background Queue Commands
export interface AIJobStatus {
  job_id: string;
  task_type: string;
  status: string;
  result?: any;
  error?: string;
  created_at: string;
}

export async function getJobStatus(jobId: string): Promise<AIJobStatus> {
  const res = await apiFetch(`/api/ai/jobs/${jobId}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch job status');
  return json.data as AIJobStatus;
}

export async function submitScoreDeal(dealId: string): Promise<{ status: string; job_id: string }> {
  const res = await apiFetch(`/api/deals/${dealId}/score`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to start scoring job');
  return json.data as { status: string; job_id: string };
}

export async function submitSummarizeMeeting(transcript: string, dealId?: string, contactId?: string): Promise<{ status: string; job_id: string }> {
  const res = await apiFetch('/api/ai/meeting/summarize', {
    method: 'POST',
    body: JSON.stringify({ transcript, deal_id: dealId, contact_id: contactId }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to start summary job');
  return json.data as { status: string; job_id: string };
}

// ============================================================
// Custom Field Definitions (Settings)
// ============================================================

export type FieldType = 'text' | 'number' | 'date' | 'select' | 'boolean' | 'url' | 'relation' | 'mirror';
export type EntityType = 'contact' | 'company' | 'deal';

export interface CustomFieldDef {
  key: string;
  label: string;
  type: FieldType;
  entity_type?: EntityType;
  options?: string[];
  // target_slug is the related object's slug for a relation (lookup) field.
  target_slug?: string;
  // via_field/source_field configure a mirror field: follow the relation named
  // via_field to the linked record and display its source_field.
  via_field?: string;
  source_field?: string;
  required: boolean;
  position: number;
}

export async function getFieldDefs(entityType?: string): Promise<CustomFieldDef[]> {
  const params = new URLSearchParams();
  if (entityType) params.set('entity_type', entityType);
  const res = await apiFetch(`/api/settings/fields?${params.toString()}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch field definitions');
  return json.data as CustomFieldDef[];
}

export async function createFieldDef(data: {
  key: string;
  label: string;
  type: string;
  entity_type: string;
  options?: string[];
  target_slug?: string;
  via_field?: string;
  source_field?: string;
  required?: boolean;
  position?: number;
}): Promise<CustomFieldDef> {
  const res = await apiFetch('/api/settings/fields', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create field definition');
  return json.data as CustomFieldDef;
}

export async function updateFieldDef(key: string, data: {
  label?: string;
  type?: string;
  options?: string[];
  target_slug?: string;
  via_field?: string;
  source_field?: string;
  required?: boolean;
  position?: number;
}): Promise<CustomFieldDef> {
  const res = await apiFetch(`/api/settings/fields/${key}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update field definition');
  return json.data as CustomFieldDef;
}

export async function deleteFieldDef(key: string): Promise<void> {
  const res = await apiFetch(`/api/settings/fields/${key}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete field definition');
  }
}

// ============================================================
// Custom Object types
// ============================================================

export interface CustomObjectDef {
  id: string;
  org_id: string;
  slug: string;
  label: string;
  label_plural: string;
  icon: string;
  fields: CustomFieldDef[];
  searchable?: boolean;
  created_at: string;
  updated_at: string;
}

export interface CustomObjectRecord {
  id: string;
  org_id: string;
  object_def_id: string;
  display_name: string;
  data: Record<string, unknown>;
  contact_id?: string;
  deal_id?: string;
  created_by?: string;
  contact?: Contact;
  deal?: { id: string; title: string };
  created_at: string;
  updated_at: string;
}

// ============================================================
// Custom Object Definition API
// ============================================================

export async function getObjectDefs(): Promise<CustomObjectDef[]> {
  const res = await apiFetch('/api/objects');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch object definitions');
  return (json.data || []) as CustomObjectDef[];
}

export async function getObjectDef(slug: string): Promise<CustomObjectDef> {
  const res = await apiFetch(`/api/objects/${slug}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch object definition');
  return json.data as CustomObjectDef;
}

export async function createObjectDef(data: {
  slug: string;
  label: string;
  label_plural: string;
  icon?: string;
  fields?: CustomFieldDef[];
  searchable?: boolean;
}): Promise<CustomObjectDef> {
  const res = await apiFetch('/api/objects', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create object');
  return json.data as CustomObjectDef;
}

export async function updateObjectDef(slug: string, data: {
  label?: string;
  label_plural?: string;
  icon?: string;
  fields?: CustomFieldDef[];
  searchable?: boolean;
}): Promise<CustomObjectDef> {
  const res = await apiFetch(`/api/objects/${slug}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update object');
  return json.data as CustomObjectDef;
}

export async function deleteObjectDef(slug: string): Promise<void> {
  const res = await apiFetch(`/api/objects/${slug}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete object');
  }
}

// ============================================================
// Custom Object Record API
// ============================================================

export async function getObjectRecords(slug: string, params?: {
  limit?: number;
  offset?: number;
  q?: string;
}): Promise<{ records: CustomObjectRecord[]; total: number }> {
  const search = new URLSearchParams();
  if (params?.limit) search.set('limit', String(params.limit));
  if (params?.offset) search.set('offset', String(params.offset));
  if (params?.q) search.set('q', params.q);
  const qs = search.toString();
  const res = await apiFetch(`/api/objects/${slug}/records${qs ? '?' + qs : ''}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch records');
  return { records: (json.data || []) as CustomObjectRecord[], total: json.total || 0 };
}

export async function createObjectRecord(slug: string, data: {
  data: Record<string, unknown>;
  contact_id?: string;
  deal_id?: string;
}): Promise<CustomObjectRecord> {
  const res = await apiFetch(`/api/objects/${slug}/records`, {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create record');
  return json.data as CustomObjectRecord;
}

export async function updateObjectRecord(slug: string, id: string, data: {
  data?: Record<string, unknown>;
  display_name?: string;
  contact_id?: string;
  deal_id?: string;
}): Promise<CustomObjectRecord> {
  const res = await apiFetch(`/api/objects/${slug}/records/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update record');
  return json.data as CustomObjectRecord;
}

export async function deleteObjectRecord(slug: string, id: string): Promise<void> {
  const res = await apiFetch(`/api/objects/${slug}/records/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete record');
  }
}

// ============================================================
// Object Registry (P2 schema) + uniform records (P3)
//
// One shape for every object — system (contact/deal/company) and custom alike —
// served by the unified RecordService at /api/registry/objects. The frontend
// renders any object from this single schema (features/objects).
// ============================================================

export type ObjectFieldType = FieldType | 'relation';

export interface ObjectFieldDescriptor {
  key: string;
  label: string;
  type: ObjectFieldType;
  options?: string[];
  target_slug?: string;
  // Mirror-field config: follow via_field to the linked record and show source_field.
  via_field?: string;
  source_field?: string;
  is_system: boolean;
  required: boolean;
  unique?: boolean;
}

// P8 — Per-role detail layouts ----------------------------------------

export interface LayoutField {
  key: string;
  width?: 'full' | 'half'; // grid span within a 2-column section; ignored for 1-col
}

export interface LayoutSection {
  id: string;
  label: string;
  columns: 1 | 2;
  fields: LayoutField[];
}

export interface ObjectLayout {
  id: string;
  org_id: string;
  object_slug: string;
  name: string;
  layout: LayoutSection[];
  is_default: boolean;
  role_ids: string[];
  created_at: string;
  updated_at: string;
}

// -----------------------------------------------------------------------

export interface ObjectSchema {
  slug: string;
  label: string;
  label_plural: string;
  icon: string;
  color: string;
  is_system: boolean;
  searchable: boolean;
  display_field: string;
  // Label prefix for record numbers (e.g. "DEAL" → DEAL-0001); admin-editable.
  number_prefix?: string;
  fields: ObjectFieldDescriptor[];
  // P8: resolved effective layout, already FLS-filtered. Absent/empty → flat field order.
  layout?: LayoutSection[];
}

export interface ObjectSummary {
  slug: string;
  label: string;
  label_plural: string;
  icon: string;
  color: string;
  is_system: boolean;
  field_count: number;
  searchable: boolean;
}

export interface UniformRecord {
  id: string;
  object: string;
  display: string;
  // Human-readable record number (e.g. "DEAL-0001"); absent until the backend has
  // assigned one.
  number?: string;
  fields: Record<string, unknown>;
  created_at: string;
  updated_at: string;
}

export interface RecordPage {
  records: UniformRecord[];
  next_cursor?: string;
}

export async function listRegistryObjects(): Promise<ObjectSummary[]> {
  const res = await apiFetch('/api/registry/objects');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch objects');
  return (json.data || []) as ObjectSummary[];
}

export async function getObjectSchema(slug: string): Promise<ObjectSchema> {
  const res = await apiFetch(`/api/registry/objects/${slug}/schema`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch object schema');
  return json.data as ObjectSchema;
}

// setObjectNumberPrefix updates an object's record-number prefix (admin only). An
// empty prefix resets to the slug default (e.g. DEAL).
export async function setObjectNumberPrefix(slug: string, prefix: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/number-prefix`, {
    method: 'PUT',
    body: JSON.stringify({ number_prefix: prefix }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to update number prefix');
  }
}

// P8 — Layout admin CRUD ---------------------------------------------------

export async function listObjectLayouts(slug: string): Promise<ObjectLayout[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch layouts');
  return (json.data || []) as ObjectLayout[];
}

export async function createObjectLayout(
  slug: string,
  payload: { name: string; layout: LayoutSection[]; is_default: boolean; role_ids: string[] }
): Promise<ObjectLayout> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts`, {
    method: 'POST',
    body: JSON.stringify(payload),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create layout');
  return json.data as ObjectLayout;
}

export async function updateObjectLayout(
  slug: string,
  id: string,
  payload: { name?: string; layout?: LayoutSection[]; is_default?: boolean }
): Promise<ObjectLayout> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(payload),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update layout');
  return json.data as ObjectLayout;
}

export async function deleteObjectLayout(slug: string, id: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to delete layout');
  }
}

export async function setLayoutRoles(slug: string, id: string, roleIds: string[]): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts/${id}/roles`, {
    method: 'PUT',
    body: JSON.stringify({ role_ids: roleIds }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to update layout roles');
  }
}

// --------------------------------------------------------------------------

export async function listObjectRecordsUnified(slug: string, params?: {
  limit?: number;
  q?: string;
  cursor?: string;
  /** Relation/field filters, e.g. { company: id } or { stage: id }. */
  filters?: Record<string, string>;
  /** Filter by tag ids (any-match), uniform across every object. */
  tagIds?: string[];
  /** Switch to semantic/vector search (contacts). */
  semantic?: boolean;
}): Promise<RecordPage> {
  const search = new URLSearchParams();
  if (params?.limit) search.set('limit', String(params.limit));
  if (params?.q) search.set('q', params.q);
  if (params?.cursor) search.set('cursor', params.cursor);
  if (params?.semantic) search.set('semantic', 'true');
  if (params?.tagIds?.length) search.set('tag_ids', params.tagIds.join(','));
  if (params?.filters) {
    for (const [k, v] of Object.entries(params.filters)) {
      if (v) search.set(k, v);
    }
  }
  const qs = search.toString();
  const res = await apiFetch(`/api/registry/objects/${slug}/records${qs ? '?' + qs : ''}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch records');
  const page = (json.data || {}) as Partial<RecordPage>;
  return { records: page.records || [], next_cursor: page.next_cursor };
}

export async function getObjectRecordUnified(slug: string, id: string): Promise<UniformRecord> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch record');
  return json.data as UniformRecord;
}

// RelatedList is one "reverse" relationship group on a record's page: the child
// object's records that point back at this record through a typed relation field
// (e.g. on a Contact, the "Deals" whose `contact` field is this contact). Derived
// from the registry, so it appears wherever a relation field targets this object.
export interface RelatedList {
  object: string;       // child object slug (e.g. "deal")
  label: string;        // child object plural label (e.g. "Deals")
  icon: string;         // child object icon
  field_key: string;    // the relation field on the child that points back
  field_label: string;  // that field's label (e.g. "Contact")
  records: UniformRecord[];
  count: number;
  // true when more children exist than the capped preview returned (show "N+").
  has_more?: boolean;
}

export async function listRecordRelatedLists(slug: string, id: string): Promise<RelatedList[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/related-lists`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch related lists');
  return (json.data || []) as RelatedList[];
}

// RecordPageData is the one-shot payload for a record detail page: schema,
// record, related lists, both tag sets, plus server-resolved relation labels
// and mirror values. One request instead of five-plus, which is what makes the
// page fast against a remote backend where every round trip costs real time.
export interface RecordPageData {
  schema: ObjectSchema;
  record: UniformRecord;
  related_lists: RelatedList[];
  tags: Tag[];
  all_tags: Tag[];
  relation_labels: Record<string, string>;
  mirror_values: Record<string, string>;
}

export async function getObjectRecordPage(slug: string, id: string): Promise<RecordPageData> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/page`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch record page');
  return json.data as RecordPageData;
}

export async function createObjectRecordUnified(slug: string, fields: Record<string, unknown>): Promise<UniformRecord> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records`, {
    method: 'POST',
    body: JSON.stringify({ fields }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create record');
  return json.data as UniformRecord;
}

export async function updateObjectRecordUnified(slug: string, id: string, fields: Record<string, unknown>): Promise<UniformRecord> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ fields }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update record');
  return json.data as UniformRecord;
}

export async function deleteObjectRecordUnified(slug: string, id: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete record');
  }
}

// ============================================================
// Universal relationships + tags (P4)
// ============================================================
// Every object — system or custom — relates to any other object and is taggable
// through the same endpoints. RecordService hides the contact_tags vs object_links
// split, so the client treats all objects identically.

export interface RecordLink {
  id: string;
  relation_key: string;
  to_slug: string;
  to_id: string;
  to_display: string;
}

export async function listRecordLinks(slug: string, id: string): Promise<RecordLink[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/links`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch links');
  return (json.data || []) as RecordLink[];
}

export async function addRecordLink(
  slug: string,
  id: string,
  data: { relation_key: string; to_slug: string; to_id: string },
): Promise<RecordLink> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/links`, {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to add link');
  return json.data as RecordLink;
}

export async function removeRecordLink(linkId: string): Promise<void> {
  const res = await apiFetch(`/api/registry/links/${linkId}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to remove link');
  }
}

export async function listRecordTags(slug: string, id: string): Promise<Tag[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/tags`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch tags');
  return (json.data || []) as Tag[];
}

export async function addRecordTag(slug: string, id: string, tagId: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/tags`, {
    method: 'POST',
    body: JSON.stringify({ tag_id: tagId }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to add tag');
  }
}

export async function removeRecordTag(slug: string, id: string, tagId: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/tags/${tagId}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to remove tag');
  }
}

// ============================================================
// Object-Level Security + audit (P5a)
// ============================================================
// The role × object access matrix RecordService enforces, plus the per-record
// audit trail. Admin-only; mounted under the registry prefix.

export interface PermObjectInfo {
  slug: string;
  label: string;
  icon: string;
  is_system: boolean;
}

export interface PermRoleInfo {
  id: string;
  name: string;
  is_system: boolean;
  is_owner: boolean;
}

// PermissionCell flattens the access bits (read/create/edit/delete) alongside the
// (role_id, object_slug) it applies to — the backend embeds ObjectAccess, so the
// four bits sit at the top level of each cell.
export interface PermissionCell {
  role_id: string;
  object_slug: string;
  read: boolean;
  create: boolean;
  edit: boolean;
  delete: boolean;
}

export interface PermissionGrid {
  objects: PermObjectInfo[];
  roles: PermRoleInfo[];
  matrix: PermissionCell[];
}

export type PermissionAction = 'read' | 'create' | 'edit' | 'delete';

export async function getPermissionGrid(): Promise<PermissionGrid> {
  const res = await apiFetch('/api/registry/permissions');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load permissions');
  const data = (json.data || {}) as Partial<PermissionGrid>;
  return { objects: data.objects || [], roles: data.roles || [], matrix: data.matrix || [] };
}

export async function setObjectPermission(input: {
  role_id: string;
  object_slug: string;
  can_read: boolean;
  can_create: boolean;
  can_edit: boolean;
  can_delete: boolean;
}): Promise<void> {
  const res = await apiFetch('/api/registry/permissions', {
    method: 'PUT',
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to save permission');
  }
}

// ============================================================
// Custom roles (P3) — capabilities + data_scope, clone-from. roles.manage gated.
// ============================================================

// The fixed capability vocabulary (mirrors domain.AllCapabilities). Rendered as
// checkboxes in the Roles manager.
export const ALL_CAPABILITIES = [
  'members.invite', 'members.manage', 'roles.manage', 'objects.manage',
  'workflows.manage', 'workflows.run_any', 'audit.view', 'billing.manage',
  'org.settings', 'data.export', 'pipeline.manage', 'knowledge.manage', 'records.write',
  'reports.manage',
] as const;
export type Capability = (typeof ALL_CAPABILITIES)[number];

export const CAPABILITY_LABELS: Record<string, string> = {
  'members.invite': 'Invite members',
  'members.manage': 'Manage members (roles, suspend, remove)',
  'roles.manage': 'Manage roles & permission grids',
  'objects.manage': 'Manage objects, fields & layouts',
  'workflows.manage': 'Manage workflows',
  'workflows.run_any': 'Run any workflow',
  'audit.view': 'View audit log',
  'billing.manage': 'Manage billing',
  'org.settings': 'Edit org settings & templates',
  'data.export': 'Export data',
  'pipeline.manage': 'Manage pipeline stages',
  'knowledge.manage': 'Edit the knowledge base',
  'records.write': 'Create/edit tasks, activities, notes & tags',
  'reports.manage': "Edit/delete other members' reports",
};

export type DataScope = 'own' | 'all';

export interface RoleDetail {
  id: string;
  name: string;
  is_system: boolean;
  is_owner: boolean;
  data_scope: DataScope;
  capabilities: string[];
  member_count: number;
}

// getMyCapabilities returns the current user's effective capability codes for the
// active org (owner gets all). Drives permission-aware UI; the server still
// enforces every action. Returns [] on any error so the UI fails closed.
export async function getMyCapabilities(): Promise<string[]> {
  try {
    const res = await apiFetch('/api/auth/capabilities');
    if (!res.ok) return [];
    const json = await res.json();
    return (json.data?.capabilities || []) as string[];
  } catch {
    return [];
  }
}

export async function getRoles(): Promise<RoleDetail[]> {
  const res = await apiFetch('/api/roles');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load roles');
  return (json.data || []) as RoleDetail[];
}

export async function createRole(input: {
  name: string;
  clone_from_id?: string;
  data_scope?: DataScope;
  capabilities?: string[];
}): Promise<RoleDetail> {
  const res = await apiFetch('/api/roles', { method: 'POST', body: JSON.stringify(input) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create role');
  return json.data as RoleDetail;
}

export async function updateRole(id: string, input: { name?: string; data_scope?: DataScope }): Promise<void> {
  const res = await apiFetch(`/api/roles/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to update role');
  }
}

export async function deleteRole(id: string): Promise<void> {
  const res = await apiFetch(`/api/roles/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete role');
  }
}

export async function setRoleCapabilities(id: string, capabilities: string[]): Promise<void> {
  const res = await apiFetch(`/api/roles/${id}/capabilities`, {
    method: 'PUT',
    body: JSON.stringify({ capabilities }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to save capabilities');
  }
}

// ============================================================
// Record shares (P3, I2) — grant a specific record to a member; the escape hatch
// for 'own'-scoped roles.
// ============================================================

export interface RecordShareView {
  id: string;
  grantee_user_id: string;
  grantee_name: string;
  permission_level: string;
  created_at: string;
}

export async function getRecordShares(slug: string, id: string): Promise<RecordShareView[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/shares`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load shares');
  return (json.data || []) as RecordShareView[];
}

export async function shareRecord(slug: string, id: string, granteeUserID: string, level = 'read'): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/share`, {
    method: 'POST',
    body: JSON.stringify({ grantee_user_id: granteeUserID, permission_level: level }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to share record');
  }
}

export async function unshareRecord(slug: string, id: string, shareID: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/share/${shareID}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to revoke share');
  }
}

// ============================================================
// Field-Level Security (P5b) — per-object field × role visibility/edit grid.
// Admin-only; opt-in. A field with no cell is fully accessible (level 'edit').
// RecordService strips 'hidden' fields from responses and rejects writes to
// 'hidden'/'read' fields server-side.
// ============================================================

export type FieldLevel = 'edit' | 'read' | 'hidden';

export interface FieldPermFieldInfo {
  key: string;
  label: string;
  type: string;
  is_system: boolean;
}

export interface FieldPermissionCell {
  role_id: string;
  field_key: string;
  level: FieldLevel;
}

export interface FieldPermissionGrid {
  slug: string;
  label: string;
  fields: FieldPermFieldInfo[];
  roles: PermRoleInfo[];
  matrix: FieldPermissionCell[];
}

export async function getFieldPermissionGrid(slug: string): Promise<FieldPermissionGrid> {
  const res = await apiFetch(`/api/registry/objects/${slug}/field-permissions`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load field permissions');
  const data = (json.data || {}) as Partial<FieldPermissionGrid>;
  return {
    slug: data.slug || slug,
    label: data.label || slug,
    fields: data.fields || [],
    roles: data.roles || [],
    matrix: data.matrix || [],
  };
}

export async function setFieldPermission(input: {
  object_slug: string;
  role_id: string;
  field_key: string;
  level: FieldLevel;
}): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${input.object_slug}/field-permissions`, {
    method: 'PUT',
    body: JSON.stringify({ role_id: input.role_id, field_key: input.field_key, level: input.level }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to save field permission');
  }
}

export interface AuditEntry {
  id: string;
  action: 'create' | 'update' | 'edit' | 'delete';
  actor_id?: string;
  actor_name: string;
  changes: Record<string, { old?: unknown; new?: unknown }>;
  created_at: string;
  object_slug: string;
  record_id: string;
}

export async function getRecordAudit(slug: string, id: string): Promise<AuditEntry[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/audit`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load audit trail');
  return (json.data || []) as AuditEntry[];
}

// ============================================================
// Knowledge Base
// ============================================================

export interface KBEntry {
  id: string;
  org_id: string;
  section: string;
  title: string;
  content: string;
  is_active: boolean;
  updated_at: string;
  created_at: string;
}

export async function getKBSections(): Promise<KBEntry[]> {
  const res = await apiFetch('/api/knowledge-base');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch KB sections');
  return json.data || [];
}

export async function getKBSection(section: string): Promise<KBEntry> {
  const res = await apiFetch(`/api/knowledge-base/${section}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Section not found');
  return json.data as KBEntry;
}

export async function upsertKBSection(section: string, data: { title: string; content: string }): Promise<KBEntry> {
  const res = await apiFetch(`/api/knowledge-base/${section}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to save section');
  return json.data as KBEntry;
}

export async function getKBAIPrompt(): Promise<string> {
  const res = await apiFetch('/api/knowledge-base/ai-prompt');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch AI prompt');
  return json.data?.prompt || '';
}


// ============================================================
// AI Command Center
// ============================================================

export interface CommandEvent {
  type: 'thinking' | 'planning' | 'tool_result' | 'response' | 'confirm' | 'navigate' | 'form' | 'error' | 'done';
  message?: string;
  tool?: string;
  data?: unknown;
  done?: boolean;
}

export interface HistoryMessage {
  role: 'user' | 'assistant';
  content: string;
}

export interface WorkspaceContext {
  org_name: string;
  role: string;
}

export async function sendCommand(
  message: string,
  sessionId: string,
  history: HistoryMessage[],
  confirmed?: boolean,
  confirmedTool?: string,
  confirmedArgs?: Record<string, unknown>,
  onEvent?: (event: CommandEvent) => void,
  onDone?: () => void,
  onError?: (err: string) => void,
  workspaces?: WorkspaceContext[],
): Promise<void> {
  try {
    const body: Record<string, unknown> = {
      message,
      session_id: sessionId,
      history: history.slice(-10),
      workspaces: workspaces || [],
    };
    if (confirmed) {
      body.confirmed = true;
      body.confirmed_tool = confirmedTool;
      body.confirmed_args = confirmedArgs;
    }

    const jsonBody = JSON.stringify(body);

    // Helper to make the fetch with the current in-memory token.
    const doFetch = () => {
      const token = getAccessToken();
      return fetch(`${API_URL}/api/ai/command`, {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          Accept: 'text/event-stream',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: jsonBody,
      });
    };

    let res = await doFetch();

    // Auto-refresh (single-flight, cookie-based) on 401 and retry once.
    if (res.status === 401) {
      const newToken = await refreshAccessToken();
      if (newToken) {
        res = await doFetch(); // retry with the fresh token
      } else {
        setAccessToken(null);
        onError?.('Session expired. Please log in again.');
        window.location.href = '/login';
        return;
      }
    }

    if (!res.ok) {
      // Non-SSE error responses (budget exceeded, plan error, etc.)
      const json = await res.json().catch(() => ({}));
      onError?.(json.error || `HTTP ${res.status}`);
      return;
    }

    // ── Stream SSE events as they arrive ──────────────────────────────
    const reader = res.body?.getReader();
    if (!reader) {
      onError?.('No response stream available');
      return;
    }

    const decoder = new TextDecoder();
    let buffer = '';

    while (true) {
      const { done, value } = await reader.read();
      if (done) break;

      buffer += decoder.decode(value, { stream: true });
      const lines = buffer.split('\n');
      buffer = lines.pop() ?? '';

      for (const line of lines) {
        if (!line.startsWith('data: ')) continue;
        const raw = line.slice(6);
        if (raw === '[DONE]') {
          onDone?.();
          return;
        }
        try {
          // Parse the SSE event JSON directly — json.Marshal on the backend
          // already produces valid JSON with properly escaped characters.
          const event = JSON.parse(raw) as CommandEvent;
          onEvent?.(event);
          if (event.type === 'done') {
            onDone?.();
            return;
          }
        } catch {
          // Skip malformed SSE chunks gracefully
        }
      }
    }

    // Stream ended without explicit done — still call onDone
    onDone?.();
  } catch (err) {
    onError?.(err instanceof Error ? err.message : 'Command failed');
  }
}

// ── Chat session management ───────────────────────────────────────────────────

export interface ChatSession {
  id: string;
  org_id: string;
  user_id: string;
  title: string;
  role: string;
  created_at: string;
  ended_at?: string;
  user?: { id: string; first_name: string; last_name: string; email: string };
}

export interface ChatMessageItem {
  id: string;
  session_id: string;
  role: 'user' | 'assistant';
  content: string;
  created_at: string;
}

export async function endChatSession(sessionId: string): Promise<void> {
  const res = await apiFetch(`/api/ai/sessions/${sessionId}/end`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to end session');
  }
}

export async function listChatSessions(page = 1, limit = 50): Promise<{ data: ChatSession[]; total: number }> {
  const res = await apiFetch(`/api/ai/sessions?page=${page}&limit=${limit}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch sessions');
  return { data: json.data || [], total: json.total || 0 };
}

export async function getChatSessionMessages(sessionId: string): Promise<ChatMessageItem[]> {
  const res = await apiFetch(`/api/ai/sessions/${sessionId}/messages`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch messages');
  return json.data || [];
}

export async function deleteChatSession(sessionId: string): Promise<void> {
  const res = await apiFetch(`/api/ai/sessions/${sessionId}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete session');
  }
}


export interface Workspace {
  org_id: string;
  org_name: string;
  org_type: string;
  role: string;
  status: string;
}

export interface WorkspaceMember {
  user_id: string;
  email: string;
  first_name: string;
  last_name: string;
  full_name: string;
  avatar_url?: string;
  role: string;
  status: string;
}

export async function switchWorkspace(orgId: string): Promise<{
  access_token: string;
  refresh_token: string;
  user: UserListItem;
  workspaces: Workspace[];
}> {
  const res = await apiFetch('/api/auth/switch-workspace', {
    method: 'POST',
    body: JSON.stringify({ org_id: orgId }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to switch workspace');
  return json.data;
}

export async function getWorkspaces(): Promise<Workspace[]> {
  const res = await apiFetch('/api/workspaces');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch workspaces');
  return (json.data || []) as Workspace[];
}

export async function getWorkspaceMembers(): Promise<WorkspaceMember[]> {
  const res = await apiFetch('/api/workspaces/members');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch members');
  return (json.data || []) as WorkspaceMember[];
}

export async function inviteMember(email: string, role: string): Promise<{ member: WorkspaceMember; debug_token?: string }> {
  const res = await apiFetch('/api/workspaces/invites', {
    method: 'POST',
    body: JSON.stringify({ email, role }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to invite member');
  return json.data;
}

export async function updateMemberRole(userId: string, role: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/role`, {
    method: 'PATCH',
    body: JSON.stringify({ role }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to update role');
  }
}

export async function removeMember(userId: string, input?: { strategy?: 'transfer' | 'unassign'; reassign_to_user_id?: string }): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}`, { 
    method: 'DELETE',
    body: input ? JSON.stringify(input) : undefined
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to remove member');
  }
}

export async function suspendMember(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/suspend`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to suspend member');
  }
}

export async function reinstateMember(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/reinstate`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to reinstate member');
  }
}

export async function transferOwnership(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/transfer`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to transfer ownership');
  }
}

export async function acceptInvite(token: string): Promise<void> {
  const res = await fetch(`${API_URL}/api/auth/accept-invite`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to accept invitation');
  }
}

// ============================================================
// Account recovery + email verification (P1)
// ============================================================
// forgot/reset/verify happen while logged out, so they use bare fetch (not
// apiFetch, which injects a stale bearer token and hard-redirects on 401).

export async function forgotPassword(email: string): Promise<{ debug_token?: string }> {
  const res = await fetch(`${API_URL}/api/auth/forgot-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email }),
  });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error(json.error || 'Failed to send reset link');
  }
  return json.data || {};
}

export async function resetPassword(token: string, password: string): Promise<void> {
  const res = await fetch(`${API_URL}/api/auth/reset-password`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token, password }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to reset password');
  }
}

export async function verifyEmail(token: string): Promise<void> {
  const res = await fetch(`${API_URL}/api/auth/verify-email`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to verify email');
  }
}

// resendVerification is called by a logged-in user from the banner, so it uses
// apiFetch to attach the bearer token.
export async function resendVerification(): Promise<void> {
  const res = await apiFetch(`/api/auth/resend-verification`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to resend verification email');
  }
}

// ============================================================
// Voice Notes
// ============================================================

export type VoiceNoteStatus = 'uploaded' | 'pending' | 'processing' | 'done' | 'error';

export interface ExtractedContactUpdates {
  phone_numbers?: string[];
  emails?: string[];
  budget?: string;
  next_meeting_date?: string;
  company_name?: string;
  notes?: string;
}

export interface VoiceNoteActionItem {
  title: string;
  due?: string;
  priority: 'low' | 'medium' | 'high';
}

export interface VoiceNote {
  id: string;
  org_id: string;
  user_id: string;
  contact_id?: string;
  deal_id?: string;
  file_url: string;
  duration_seconds: number;
  language_code: string;
  status: VoiceNoteStatus;
  transcript?: string;
  summary?: string;
  key_points?: string[];
  action_items?: VoiceNoteActionItem[];
  extracted_contact_updates?: ExtractedContactUpdates;
  sentiment?: string;
  error_message?: string;
  created_at: string;
  updated_at: string;
  contact?: { id: string; first_name: string; last_name: string; email?: string };
  deal?: { id: string; title: string };
}

export interface VoiceNoteFilter {
  contact_id?: string;
  deal_id?: string;
  limit?: number;
}

export function uploadVoiceNote(
  audioBlob: Blob,
  filename: string,
  languageCode: string,
  contactId?: string,
  dealId?: string,
  durationSeconds?: number,
  onProgress?: (percent: number) => void,
  autoAnalyze: boolean = false
): Promise<{ voice_note: VoiceNote; job_id: string }> {
  return new Promise((resolve, reject) => {
    const formData = new FormData();
    formData.append('file', audioBlob, filename);
    formData.append('language_code', languageCode);
    formData.append('analyze', autoAnalyze ? 'true' : 'false');
    if (contactId) formData.append('contact_id', contactId);
    if (dealId) formData.append('deal_id', dealId);
    if (durationSeconds) formData.append('duration_seconds', String(durationSeconds));

    const token = getAccessToken();
    const xhr = new XMLHttpRequest();

    xhr.open('POST', `${API_URL}/api/voice/upload`);
    xhr.withCredentials = true;

    if (token) {
      xhr.setRequestHeader('Authorization', `Bearer ${token}`);
    }

    if (xhr.upload && onProgress) {
      xhr.upload.onprogress = (event) => {
        if (event.lengthComputable) {
          const percentComplete = Math.round((event.loaded / event.total) * 100);
          onProgress(percentComplete);
        }
      };
    }

    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const json = JSON.parse(xhr.responseText);
          resolve(json.data);
        } catch (err) {
          reject(new Error('Invalid response from server'));
        }
      } else {
        try {
          const json = JSON.parse(xhr.responseText);
          reject(new Error(json.error || 'Upload failed'));
        } catch (err) {
          reject(new Error('Upload failed'));
        }
      }
    };

    xhr.onerror = () => reject(new Error('Network error occurred during upload'));
    xhr.send(formData);
  });
}

export async function getVoiceNotes(filter: VoiceNoteFilter = {}): Promise<VoiceNote[]> {
  const params = new URLSearchParams();
  if (filter.contact_id) params.set('contact_id', filter.contact_id);
  if (filter.deal_id) params.set('deal_id', filter.deal_id);
  if (filter.limit) params.set('limit', String(filter.limit));
  const res = await apiFetch(`/api/voice?${params.toString()}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to fetch voice notes');
  return (json.data || []) as VoiceNote[];
}

export async function getVoiceNote(id: string): Promise<VoiceNote> {
  const res = await apiFetch(`/api/voice/${id}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Voice note not found');
  return json.data as VoiceNote;
}

export async function applyVoiceNoteUpdates(id: string): Promise<void> {
  const res = await apiFetch(`/api/voice/${id}/apply-updates`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to apply updates');
  }
}

export async function deleteVoiceNote(id: string): Promise<void> {
  const res = await apiFetch(`/api/voice/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to delete voice note');
  }
}

export async function analyzeVoiceNote(id: string): Promise<void> {
  const res = await apiFetch(`/api/voice/${id}/analyze`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error || 'Failed to start analysis');
  }
}

// ============================================================
// Global search (P6)
//
// One cross-object query that spans every searchable object (custom objects via
// the generic index, contacts via their native index), returning OLS/FLS-safe
// records grouped by object. Backed by GET /api/registry/search.
// ============================================================

export interface SearchHit {
  record: UniformRecord;
  score?: number;
}

export interface SearchGroup {
  object: string;
  label: string;
  label_plural: string;
  icon: string;
  hits: SearchHit[];
}

export interface SearchResult {
  query: string;
  groups: SearchGroup[];
}

export async function globalSearch(query: string, limit = 10): Promise<SearchResult> {
  const search = new URLSearchParams({ q: query });
  if (limit) search.set('limit', String(limit));
  const res = await apiFetch(`/api/registry/search?${search.toString()}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Search failed');
  const data = (json.data || {}) as Partial<SearchResult>;
  return { query: data.query || query, groups: data.groups || [] };
}



// ── Admin/auth audit log (P4) ───────────────────────────────────────────────
// The append-only who-did-what over auth_events. Read + CSV export are gated on
// the audit.view capability server-side.

export interface AuditEvent {
  id: string;
  org_id?: string;
  actor_id?: string;
  actor_name: string;
  actor_email: string;
  target_id?: string;
  category: string; // 'auth' | 'admin' | 'security'
  event_type: string;
  ip?: string;
  user_agent?: string;
  metadata: Record<string, unknown>;
  created_at: string;
}

export interface AuditEventFilters {
  category?: string;
  type?: string;
  actor?: string;
  from?: string; // RFC3339
  to?: string; // RFC3339
  limit?: number;
  offset?: number;
}

function auditQuery(f: AuditEventFilters): string {
  const p = new URLSearchParams();
  if (f.category) p.set('category', f.category);
  if (f.type) p.set('type', f.type);
  if (f.actor) p.set('actor', f.actor);
  if (f.from) p.set('from', f.from);
  if (f.to) p.set('to', f.to);
  if (f.limit != null) p.set('limit', String(f.limit));
  if (f.offset != null) p.set('offset', String(f.offset));
  return p.toString();
}

export async function getAuditEvents(
  f: AuditEventFilters = {},
): Promise<{ events: AuditEvent[]; total: number }> {
  const res = await apiFetch(`/api/audit/events?${auditQuery(f)}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load audit log');
  return { events: (json.data || []) as AuditEvent[], total: (json.total || 0) as number };
}

// exportAuditCsv downloads the filtered audit log as a CSV blob (same filters as
// the on-screen view, minus pagination).
export async function exportAuditCsv(f: AuditEventFilters = {}): Promise<Blob> {
  const { limit: _l, offset: _o, ...rest } = f;
  const res = await apiFetch(`/api/audit/events/export.csv?${auditQuery(rest)}`);
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Export failed');
  }
  return res.blob();
}

// ── Session / device management (P4) ────────────────────────────────────────

export interface UserSession {
  id: string;
  device_label: string;
  ip: string;
  last_used_at?: string;
  created_at: string;
  current: boolean;
}

export async function getSessions(): Promise<UserSession[]> {
  const res = await apiFetch('/api/auth/sessions');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load sessions');
  return (json.data || []) as UserSession[];
}

export async function revokeSession(id: string): Promise<void> {
  const res = await apiFetch(`/api/auth/sessions/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to revoke session');
  }
}

// signOutEverywhere revokes all other sessions and re-mints this one; the server
// returns a fresh access token that the caller must adopt so the current tab
// keeps working (its old access token was invalidated by the token_version bump).
export async function signOutEverywhere(): Promise<void> {
  const res = await apiFetch('/api/auth/sessions', { method: 'DELETE' });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((json as { error?: string }).error || 'Failed to sign out other sessions');
  const token = (json?.data?.access_token as string) ?? null;
  if (token) setAccessToken(token);
}

// ============================================================
// Reports (P9) — saved report definitions + per-viewer execution
// ============================================================

export type ReportChart = 'bar' | 'line' | 'pie' | 'donut' | 'kpi' | 'table';
export type ReportVisibility = 'private' | 'org';
export type ReportDateBucket = 'day' | 'week' | 'month' | 'quarter' | 'year';
export type ReportAggregateFn = 'count' | 'count_distinct' | 'sum' | 'avg' | 'min' | 'max';

// Filter shape mirrors the automation ConditionGroup JSON so both builders
// speak one filter language.
export interface ReportFilterRule {
  field?: string;
  operator?: string;
  value?: unknown;
  op?: 'AND' | 'OR';
  rules?: ReportFilterRule[];
}

export interface ReportFilterGroup {
  op?: 'AND' | 'OR';
  rules?: ReportFilterRule[];
}

export interface ReportConfig {
  version?: number;
  chart: ReportChart;
  filters?: ReportFilterGroup;
  group_by?: { field: string; bucket?: ReportDateBucket };
  aggregate?: { fn: ReportAggregateFn; field?: string };
  columns?: string[];
  sort?: { by: string; dir: 'asc' | 'desc' };
  limit?: number;
}

export interface Report {
  id: string;
  org_id: string;
  name: string;
  description: string;
  object_slug: string;
  config: ReportConfig;
  visibility: ReportVisibility;
  created_by?: string;
  created_at: string;
  updated_at: string;
}

export interface ReportGroupRow {
  key: unknown;
  label: string;
  value: number;
  count: number;
}

export interface ReportResult {
  kind: 'groups' | 'rows' | 'scalar';
  groups?: ReportGroupRow[];
  columns?: string[];
  rows?: Record<string, unknown>[];
  value: number;
  row_count: number;
}

// One queryable field for the report builder: registry fields plus the
// report-only virtual fields (created_at, owner, deal lifecycle), already
// FLS-filtered for the caller.
export interface ReportFieldDescriptor {
  key: string;
  label: string;
  type: ObjectFieldType;
  options?: string[];
}

export interface ReportInput {
  name: string;
  description?: string;
  object_slug: string;
  visibility?: ReportVisibility;
  config: ReportConfig;
}

export async function listReports(): Promise<Report[]> {
  const res = await apiFetch('/api/reports');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load reports');
  return (json.data || []) as Report[];
}

export async function createReport(input: ReportInput): Promise<Report> {
  const res = await apiFetch('/api/reports', { method: 'POST', body: JSON.stringify(input) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to create report');
  return json.data as Report;
}

export async function getReport(id: string): Promise<Report> {
  const res = await apiFetch(`/api/reports/${id}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load report');
  return json.data as Report;
}

export async function updateReport(id: string, input: ReportInput): Promise<Report> {
  const res = await apiFetch(`/api/reports/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to update report');
  return json.data as Report;
}

export async function deleteReport(id: string): Promise<void> {
  const res = await apiFetch(`/api/reports/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to delete report');
  }
}

export async function runReport(id: string): Promise<ReportResult> {
  const res = await apiFetch(`/api/reports/${id}/run`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to run report');
  return json.data as ReportResult;
}

// previewReport runs an unsaved config — the builder's live preview.
export async function previewReport(objectSlug: string, config: ReportConfig): Promise<ReportResult> {
  const res = await apiFetch('/api/reports/preview', {
    method: 'POST',
    body: JSON.stringify({ object_slug: objectSlug, config }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Preview failed');
  return json.data as ReportResult;
}

export async function listReportFields(slug: string): Promise<ReportFieldDescriptor[]> {
  const res = await apiFetch(`/api/reports/objects/${slug}/fields`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load report fields');
  return (json.data || []) as ReportFieldDescriptor[];
}

export async function exportReportCsv(id: string): Promise<Blob> {
  const res = await apiFetch(`/api/reports/${id}/export.csv`);
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Export failed');
  }
  return res.blob();
}

// ── Dashboard widgets (P9 Phase B) ──────────────────────────────────────────
// Each user pins saved reports to their own dashboard. Widgets carry layout
// only; the data comes from runReport per widget, re-authorized per viewer.

export interface DashboardWidget {
  id: string;
  org_id: string;
  user_id: string;
  report_id: string;
  position: number;
  size: 'half' | 'full';
  created_at: string;
  updated_at: string;
  // Present on GET /api/dashboard/widgets (widgets whose report is deleted or
  // no longer shared are dropped server-side).
  report?: Report;
}

export async function listDashboardWidgets(): Promise<DashboardWidget[]> {
  const res = await apiFetch('/api/dashboard/widgets');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to load dashboard');
  return (json.data || []) as DashboardWidget[];
}

export async function addDashboardWidget(reportId: string, size: 'half' | 'full' = 'half'): Promise<DashboardWidget> {
  const res = await apiFetch('/api/dashboard/widgets', {
    method: 'POST',
    body: JSON.stringify({ report_id: reportId, size }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error || 'Failed to pin report');
  return json.data as DashboardWidget;
}

export async function updateDashboardWidget(id: string, size: 'half' | 'full'): Promise<void> {
  const res = await apiFetch(`/api/dashboard/widgets/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ size }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to update widget');
  }
}

export async function removeDashboardWidget(id: string): Promise<void> {
  const res = await apiFetch(`/api/dashboard/widgets/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to remove widget');
  }
}

export async function reorderDashboardWidgets(widgetIds: string[]): Promise<void> {
  const res = await apiFetch('/api/dashboard/widgets/reorder', {
    method: 'PUT',
    body: JSON.stringify({ widget_ids: widgetIds }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error((json as { error?: string }).error || 'Failed to reorder widgets');
  }
}
