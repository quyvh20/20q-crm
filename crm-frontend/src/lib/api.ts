const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

async function apiFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const token = localStorage.getItem('access_token');
  const headers: Record<string, string> = {
    ...(options.headers as Record<string, string> || {}),
  };

  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  // Don't set Content-Type for FormData (browser sets boundary automatically)
  if (!(options.body instanceof FormData)) {
    headers['Content-Type'] = 'application/json';
  }

  const res = await fetch(`${API_URL}${path}`, { ...options, headers });

  if (res.status === 401) {
    // Try refresh
    const refreshToken = localStorage.getItem('refresh_token');
    if (refreshToken) {
      const refreshRes = await fetch(`${API_URL}/api/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (refreshRes.ok) {
        const data = await refreshRes.json();
        localStorage.setItem('access_token', data.data.access_token);
        localStorage.setItem('refresh_token', data.data.refresh_token);
        headers['Authorization'] = `Bearer ${data.data.access_token}`;
        return fetch(`${API_URL}${path}`, { ...options, headers });
      }
    }
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
    window.location.href = '/login';
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
  const API_BASE = (import.meta as any).env?.VITE_API_URL || 'http://localhost:8080';
  const token = localStorage.getItem('access_token');
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'text/event-stream',
  };
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/ai/chat`, {
    method: 'POST',
    headers,
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
  const API_BASE = (import.meta as any).env?.VITE_API_URL || 'http://localhost:8080';
  const token = localStorage.getItem('access_token');
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'text/event-stream',
  };
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}/api/ai/email/compose`, {
    method: 'POST',
    headers,
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

export type FieldType = 'text' | 'number' | 'date' | 'select' | 'boolean' | 'url';
export type EntityType = 'contact' | 'company' | 'deal';

export interface CustomFieldDef {
  key: string;
  label: string;
  type: FieldType;
  entity_type?: EntityType;
  options?: string[];
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

    // Helper to make the fetch with current token
    const doFetch = () => {
      const token = localStorage.getItem('access_token');
      return fetch(`${API_URL}/api/ai/command-sync`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: jsonBody,
      });
    };

    let res = await doFetch();

    // Auto-refresh token on 401 and retry once
    if (res.status === 401) {
      const refreshToken = localStorage.getItem('refresh_token');
      if (refreshToken) {
        const refreshRes = await fetch(`${API_URL}/api/auth/refresh`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refresh_token: refreshToken }),
        });
        if (refreshRes.ok) {
          const data = await refreshRes.json();
          localStorage.setItem('access_token', data.data.access_token);
          localStorage.setItem('refresh_token', data.data.refresh_token);
          res = await doFetch(); // retry with fresh token
        } else {
          // Refresh failed — session truly expired
          localStorage.removeItem('access_token');
          localStorage.removeItem('refresh_token');
          onError?.('Session expired. Please log in again.');
          window.location.href = '/login';
          return;
        }
      } else {
        onError?.('Session expired. Please log in again.');
        window.location.href = '/login';
        return;
      }
    }

    if (!res.ok) {
      const json = await res.json().catch(() => ({}));
      onError?.(json.error || `HTTP ${res.status}`);
      return;
    }

    const json = await res.json();
    const events: CommandEvent[] = json.events || [];

    // Replay events sequentially for smooth UX
    for (const event of events) {
      onEvent?.(event);
      if (event.type === 'done') {
        onDone?.();
        return;
      }
    }
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

    const token = localStorage.getItem('access_token');
    const xhr = new XMLHttpRequest();

    xhr.open('POST', `${API_URL}/api/voice/upload`);

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


