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
    const json = await res.json();
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

export async function createDeal(data: {
  title: string;
  contact_id?: string;
  company_id?: string;
  stage_id?: string;
  value?: number;
  probability?: number;
  expected_close_at?: string;
}): Promise<Deal> {
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
    const json = await res.json();
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
    const json = await res.json();
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
