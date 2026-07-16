const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

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

function postRefresh(orgId?: string): Promise<Response> {
  return fetch(`${API_URL}/api/auth/refresh`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': readCsrfToken() },
    body: JSON.stringify(orgId ? { org_id: orgId } : {}),
  });
}

export function refreshAccessToken(orgId?: string): Promise<string | null> {
  if (!refreshInFlight) {
    refreshInFlight = (async (): Promise<string | null> => {
      try {
        // Default to the SPA's last active workspace so a mid-session refresh stays
        // in the same org (P3) instead of the server silently picking default/first.
        const targetOrg = orgId ?? localStorage.getItem('active_workspace_id') ?? undefined;
        let res = await postRefresh(targetOrg);
        // 409 ORG_UNAVAILABLE: the active org is gone. Drop it and retry into the
        // default/first org so the session survives; a full reload will then route
        // to the chooser via the AuthProvider.
        if (res.status === 409) {
          localStorage.removeItem('active_workspace_id');
          res = await postRefresh(undefined);
        }
        if (!res.ok) return null;
        const json = await parseJsonSafe(res);
        const token = (json?.data?.access_token as string) ?? null;
        setAccessToken(token);
        // Keep active_workspace_id in sync with the org the server actually bound.
        const activeOrg = json?.data?.active_org_id as string | undefined;
        if (activeOrg) localStorage.setItem('active_workspace_id', activeOrg);
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

// The one authenticated fetch wrapper for the whole app: attaches the in-memory
// bearer token, sends credentials, transparently refreshes once on a 401 (and
// bounces to /login if the session is truly gone), and skips Content-Type for
// FormData. Exported so every feature api layer shares the SAME auth behavior.
// `timeoutMs` optionally aborts a hung request (e.g. a slow AI call behind a proxy).
// redirectToLoginExpired sends the user to /login with a "session expired"
// notice and a return-to path, instead of a silent hard boot that loses their
// place (U2). LoginPage reads both params.
function redirectToLoginExpired() {
  const next = window.location.pathname + window.location.search;
  const params = new URLSearchParams({ expired: '1' });
  if (next && next !== '/' && !next.startsWith('/login')) params.set('next', next);
  window.location.href = `/login?${params.toString()}`;
}

// The path the 2FA-enrollment-confined user is parked on (U6.4).
export const ENROLL_TWO_FACTOR_PATH = '/enroll-2fa';

// redirectToTwoFactorEnrollment handles the OTHER kind of "your session can't do
// this": the workspace requires 2FA and this user hasn't enrolled. The server
// hands them a REAL session (they need one to reach the enrollment endpoints) but
// 403s every /api/* route outside /api/auth/* with code `two_factor_required`.
// Without this the app would render as a wall of failed panels; instead we park
// them on the enrollment screen. Guarded so a page whose panels each 403 doesn't
// re-navigate (or loop) once we're already there.
function redirectToTwoFactorEnrollment() {
  if (window.location.pathname === ENROLL_TWO_FACTOR_PATH) return;
  window.location.href = ENROLL_TWO_FACTOR_PATH;
}

export async function apiFetch(path: string, options: RequestInit & { timeoutMs?: number } = {}): Promise<Response> {
  const { timeoutMs, ...init } = options;
  const buildHeaders = (): Record<string, string> => {
    const headers: Record<string, string> = {
      ...(init.headers as Record<string, string> || {}),
    };
    const token = getAccessToken();
    if (token) headers['Authorization'] = `Bearer ${token}`;
    // Don't set Content-Type for FormData (browser sets the boundary itself).
    if (!(init.body instanceof FormData)) headers['Content-Type'] = 'application/json';
    return headers;
  };

  const controller = timeoutMs ? new AbortController() : null;
  const timer = controller ? setTimeout(() => controller.abort(), timeoutMs) : null;
  const doFetch = () =>
    fetch(`${API_URL}${path}`, {
      ...init,
      headers: buildHeaders(),
      credentials: 'include',
      signal: controller?.signal ?? init.signal,
    });

  try {
    let res = await doFetch();
    if (res.status === 401) {
      const newToken = await refreshAccessToken();
      if (newToken) {
        res = await doFetch();
      } else {
        setAccessToken(null);
        redirectToLoginExpired();
      }
    }
    // Workspace 2FA policy (U6.4): a distinct 403 code means "enroll first", not
    // "you're not allowed". Read the body from a CLONE so the caller still gets an
    // unconsumed Response.
    if (res.status === 403) {
      const body = await res.clone().text().catch(() => '');
      if (body.includes('two_factor_required')) {
        try {
          if ((JSON.parse(body) as { code?: string }).code === 'two_factor_required') {
            redirectToTwoFactorEnrollment();
          }
        } catch {
          // Not JSON — leave it to the caller's error handling.
        }
      }
    }
    return res;
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * Read a JSON body defensively. A proxy, gateway timeout, auth wall, or 404 at the
 * edge can return an HTML page ("<!DOCTYPE html>…") instead of JSON; calling
 * res.json() on that throws a cryptic `Unexpected token '<'`. This reads the body
 * once and, on a non-JSON payload, throws a clear message keyed to the HTTP status
 * so the raw parse error never reaches the UI. Shared by every feature api layer.
 */
export async function parseJsonSafe(res: Response): Promise<any> {
  const text = await res.text();
  try {
    return text ? JSON.parse(text) : {};
  } catch {
    const hint =
      res.status === 401
        ? 'your session may have expired — please sign in again'
        : res.status === 403
          ? "you don't have permission for this action"
          : res.status === 404
          ? 'the service endpoint was not found'
          : res.status === 0 || res.status >= 500
            ? 'the service is temporarily unavailable — please try again'
            : 'the server returned an unexpected response';
    throw apiError(res, null, `Request failed (HTTP ${res.status || '000'}) — ${hint}.`);
  }
}

/**
 * ApiError is a failed request, carrying the server's own correlation id (U7.4).
 *
 * The backend has always stamped every response with an X-Request-ID and logged it
 * with zap — but the header was not in the CORS expose list, so the id existed on
 * both ends of a failed request and was visible to nobody. Now the browser can read
 * it, and this is what carries it to the user.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly requestId?: string;

  constructor(message: string, status: number, requestId?: string) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.requestId = requestId;
  }
}

/**
 * apiError builds the error thrown by every failed api call.
 *
 * The reference rides in the MESSAGE, not only in a field, and that is deliberate:
 * every error banner in this app already renders `err.message`, so putting it there
 * reaches all ~50 of them without touching a single component. It appears ONLY when
 * the server actually returned an id — so a mocked Response in a unit test (which
 * has no such header) still produces the plain message the test asserts on.
 *
 * The id is shortened to its first block: enough to grep the logs, short enough that
 * a human will actually read it out over the phone.
 */
export function apiError(res: Response, json: unknown, fallback: string): ApiError {
  // Two envelope shapes are in the wild: `error` as a plain string (the Go handlers'
  // domain.Err) and `error` as an object carrying `.message` (the automation/
  // notification layers). Reading only the first would render "[object Object]" at
  // the other, so both are unwrapped here.
  const raw = (json as { error?: string | { message?: string } } | null)?.error;
  const message =
    (typeof raw === 'string' ? raw : raw?.message) || fallback;
  const requestId = res.headers?.get?.('X-Request-ID') || undefined;
  const shown = requestId ? requestId.split('-')[0] : '';
  return new ApiError(
    shown ? `${message} (reference: ${shown})` : message,
    res.status,
    requestId,
  );
}

// asArray defensively coerces an API `data` field that MUST be a list into an
// array. An unexpected response shape (object, string, null) becomes [] instead
// of reaching a `.map`/`.filter`/`.length` consumer — a non-array there throws
// during render and, under the app-wide error boundary, white-screens the whole
// page ("Something went wrong") on load. The anomaly is logged (not swallowed
// silently) so the root cause stays discoverable in the console.
export function asArray<T>(data: unknown, ctx: string): T[] {
  if (Array.isArray(data)) return data as T[];
  if (data != null) console.warn(`[api] ${ctx}: expected an array, got ${typeof data}`);
  return [];
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
  const json = await parseJsonSafe(res);
  return { contacts: json.data as Contact[], meta: json.meta as CursorMeta };
}

export async function getContact(id: string) {
  const res = await apiFetch(`/api/contacts/${id}`);
  const json = await parseJsonSafe(res);
  return json.data as Contact;
}

export async function createContact(data: Partial<Contact> & { tag_ids?: string[] }) {
  const res = await apiFetch('/api/contacts', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create contact');
  return json.data as Contact;
}

export async function updateContact(id: string, data: Partial<Contact> & { tag_ids?: string[] }) {
  const res = await apiFetch(`/api/contacts/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update contact');
  return json.data as Contact;
}

export async function deleteContact(id: string) {
  const res = await apiFetch(`/api/contacts/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete contact');
  }
}

export async function importContacts(file: File, conflictMode: 'skip' | 'overwrite' = 'skip') {
  const formData = new FormData();
  formData.append('file', file);
  const res = await apiFetch(`/api/contacts/import?conflict_mode=${conflictMode}`, {
    method: 'POST',
    body: formData,
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Import failed');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Bulk action failed');
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
  const json = await parseJsonSafe(res);
  return json.data as Company[];
}

export async function getTags() {
  const res = await apiFetch('/api/tags');
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  return json.data as PipelineStage[];
}

export async function createStage(data: Partial<PipelineStage>) {
  const res = await apiFetch('/api/pipeline/stages', { method: 'POST', body: JSON.stringify(data) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create stage');
  return json.data as PipelineStage;
}

export async function updateStage(id: string, data: Partial<PipelineStage>) {
  const res = await apiFetch(`/api/pipeline/stages/${id}`, { method: 'PUT', body: JSON.stringify(data) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update stage');
  return json.data as PipelineStage;
}

export async function deleteStage(id: string) {
  const res = await apiFetch(`/api/pipeline/stages/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete stage');
  }
}

export async function seedDefaultStages(): Promise<PipelineStage[]> {
  const res = await apiFetch('/api/pipeline/stages/seed-defaults', { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to seed default stages');
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
  const json = await parseJsonSafe(res);
  return { deals: json.data as Deal[], meta: json.meta as CursorMeta };
}

export async function getDeal(id: string): Promise<Deal> {
  const res = await apiFetch(`/api/deals/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch deal');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create deal');
  return json.data as Deal;
}

export async function updateDeal(id: string, data: Partial<Deal>): Promise<Deal> {
  const res = await apiFetch(`/api/deals/${id}`, { method: 'PUT', body: JSON.stringify(data) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update deal');
  return json.data as Deal;
}

export async function deleteDeal(id: string) {
  const res = await apiFetch(`/api/deals/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete deal');
  }
}

export async function changeDealStage(dealId: string, stageId: string, lostReason?: string): Promise<Deal> {
  const res = await apiFetch(`/api/deals/${dealId}/stage`, {
    method: 'PATCH',
    body: JSON.stringify({ stage_id: stageId, lost_reason: lostReason }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to change stage');
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create activity');
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create task');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update task');
  return json.data as Task;
}

export async function deleteTask(id: string) {
  const res = await apiFetch(`/api/tasks/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete task');
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch AI usage');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch job status');
  return json.data as AIJobStatus;
}

export async function submitScoreDeal(dealId: string): Promise<{ status: string; job_id: string }> {
  const res = await apiFetch(`/api/deals/${dealId}/score`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to start scoring job');
  return json.data as { status: string; job_id: string };
}

export async function submitSummarizeMeeting(transcript: string, dealId?: string, contactId?: string): Promise<{ status: string; job_id: string }> {
  const res = await apiFetch('/api/ai/meeting/summarize', {
    method: 'POST',
    body: JSON.stringify({ transcript, deal_id: dealId, contact_id: contactId }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to start summary job');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch field definitions');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create field definition');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update field definition');
  return json.data as CustomFieldDef;
}

export async function deleteFieldDef(key: string): Promise<void> {
  const res = await apiFetch(`/api/settings/fields/${key}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete field definition');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch object definitions');
  return (json.data || []) as CustomObjectDef[];
}

export async function getObjectDef(slug: string): Promise<CustomObjectDef> {
  const res = await apiFetch(`/api/objects/${slug}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch object definition');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create object');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update object');
  return json.data as CustomObjectDef;
}

export async function deleteObjectDef(slug: string): Promise<void> {
  const res = await apiFetch(`/api/objects/${slug}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete object');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch records');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create record');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update record');
  return json.data as CustomObjectRecord;
}

export async function deleteObjectRecord(slug: string, id: string): Promise<void> {
  const res = await apiFetch(`/api/objects/${slug}/records/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete record');
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
  // U6: true when records of this object have an owner (contact, deal and every
  // custom object; company has none). Owner is NOT a registry field — it never
  // appears in `fields`, so the UI renders it as a dedicated control keyed off
  // this flag and writes it as `owner_user_id` inside the fields map.
  has_owner: boolean;
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
  // U6: the record's owner (absent on ownerless objects like company; null when
  // unassigned). Also mirrored into fields.owner_user_id — writes go through the
  // fields map ('' / null unassigns).
  owner_user_id?: string | null;
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch objects');
  return (json.data || []) as ObjectSummary[];
}

export async function getObjectSchema(slug: string): Promise<ObjectSchema> {
  const res = await apiFetch(`/api/registry/objects/${slug}/schema`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch object schema');
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
    throw apiError(res, json, 'Failed to update number prefix');
  }
}

// P8 — Layout admin CRUD ---------------------------------------------------

export async function listObjectLayouts(slug: string): Promise<ObjectLayout[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch layouts');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create layout');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update layout');
  return json.data as ObjectLayout;
}

export async function deleteObjectLayout(slug: string, id: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete layout');
  }
}

export async function setLayoutRoles(slug: string, id: string, roleIds: string[]): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/layouts/${id}/roles`, {
    method: 'PUT',
    body: JSON.stringify({ role_ids: roleIds }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to update layout roles');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch records');
  const page = (json.data || {}) as Partial<RecordPage>;
  return { records: page.records || [], next_cursor: page.next_cursor };
}

export async function getObjectRecordUnified(slug: string, id: string): Promise<UniformRecord> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch record');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch related lists');
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
  // U6: what the CALLER may do with THIS record (row-level, distinct from the
  // object-level OLS bits): 'manage' (owner/admin — may share), 'edit', 'view'.
  // Optional: an older server (or the per-endpoint fallback path) doesn't send
  // it — undefined means "unknown", and callers fail open (the server enforces).
  effective_level?: RecordLevel;
}

export async function getObjectRecordPage(slug: string, id: string): Promise<RecordPageData> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/page`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch record page');
  return json.data as RecordPageData;
}

export async function createObjectRecordUnified(slug: string, fields: Record<string, unknown>): Promise<UniformRecord> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records`, {
    method: 'POST',
    body: JSON.stringify({ fields }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create record');
  return json.data as UniformRecord;
}

export async function updateObjectRecordUnified(slug: string, id: string, fields: Record<string, unknown>): Promise<UniformRecord> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ fields }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update record');
  return json.data as UniformRecord;
}

export async function deleteObjectRecordUnified(slug: string, id: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete record');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch links');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to add link');
  return json.data as RecordLink;
}

export async function removeRecordLink(linkId: string): Promise<void> {
  const res = await apiFetch(`/api/registry/links/${linkId}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to remove link');
  }
}

export async function listRecordTags(slug: string, id: string): Promise<Tag[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/tags`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch tags');
  return (json.data || []) as Tag[];
}

export async function addRecordTag(slug: string, id: string, tagId: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/tags`, {
    method: 'POST',
    body: JSON.stringify({ tag_id: tagId }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to add tag');
  }
}

export async function removeRecordTag(slug: string, id: string, tagId: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/tags/${tagId}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to remove tag');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load permissions');
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
    throw apiError(res, json, 'Failed to save permission');
  }
}

// ============================================================
// Custom roles (P3) — capabilities + data_scope, clone-from. roles.manage gated.
// ============================================================

// The fixed capability vocabulary (mirrors domain.AllCapabilities). Rendered as
// checkboxes in the Roles manager.
export const ALL_CAPABILITIES = [
  'members.invite', 'members.manage', 'roles.manage', 'objects.manage',
  'workflows.manage', 'workflows.run_any', 'audit.view', 'analytics.view',
  'org.settings', 'data.export', 'pipeline.manage', 'knowledge.manage', 'records.write',
  'reports.manage', 'groups.manage', 'integrations.manage',
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
  'analytics.view': 'View analytics & forecasts',
  'org.settings': 'Edit org settings & templates',
  'data.export': 'Export report results (CSV)',
  'pipeline.manage': 'Manage pipeline stages',
  'knowledge.manage': 'Edit the knowledge base',
  'records.write': 'Create/edit tasks, activities, notes & tags',
  'reports.manage': "Edit/delete other members' reports",
  'groups.manage': 'Manage user groups & their members',
  'integrations.manage': 'Connect lead sources & mint capture keys',
};

// DataScope is a role's row scope (U6): 'own' = only records they own (plus
// records shared to them), 'team' = additionally every record owned by someone
// who shares a user group ("team") with them, 'all' = the whole workspace.
// Ordered narrowest → widest; 'own' is the safe default for anything unknown.
export const DATA_SCOPES = ['own', 'team', 'all'] as const;
export type DataScope = (typeof DATA_SCOPES)[number];

// asDataScope whitelists a server value into a DataScope, defaulting to the
// NARROWEST scope. The old coercion (x === 'own' ? 'own' : 'all') failed OPEN:
// any value it didn't recognize — including 'team' — rendered as full workspace
// access in the UI.
function asDataScope(v: unknown): DataScope {
  return (DATA_SCOPES as readonly string[]).includes(v as string) ? (v as DataScope) : 'own';
}

export interface RoleDetail {
  id: string;
  name: string;
  description: string;
  is_system: boolean;
  is_owner: boolean;
  data_scope: DataScope;
  template_key?: string | null;
  seeded_from_role_id?: string | null;
  capabilities: string[];
  member_count: number;
}

// RoleOption is the minimal role identity any member may read for role pickers
// (P6) — no capabilities, so it needs no roles.manage gate.
export interface RoleOption {
  id: string;
  name: string;
  description: string;
  is_system: boolean;
  is_owner: boolean;
  data_scope: DataScope;
}

// CapabilityInfo is the human-facing metadata for one capability (P6), served by
// GET /api/roles/catalog: label/description + the group it renders under + the
// sensitive flag (rendered as the "Sensitive" chip).
export interface CapabilityInfo {
  code: string;
  label: string;
  description: string;
  group: string;
  sensitive: boolean;
}

// ObjectAccessBits are the caller's OLS bits for one object (U3.7) — same
// flattened shape as PermissionCell minus the identifying keys.
export interface ObjectAccessBits {
  read: boolean;
  create: boolean;
  edit: boolean;
  delete: boolean;
}

// MyPermissions is the caller's full authorization context for the active org
// (P6): effective capability codes plus role identity + row scope. Drives the
// usePermissions() hook; the server still enforces every action independently.
// `objects` (U3.7) maps object slug → the caller's OLS bits so record-level
// Edit/Delete/Add buttons can hide instead of 403ing. It is undefined when the
// server predates U3 — treat "unknown" as visible (server still enforces).
export interface MyPermissions {
  capabilities: string[];
  data_scope: DataScope;
  role_id: string;
  role_name: string;
  is_owner: boolean;
  objects?: Record<string, ObjectAccessBits>;
}

// getMyPermissions returns the caller's effective capabilities + role identity for
// the active org (owner gets all capabilities). Fails closed to an empty, denied
// identity on any error so the UI never over-shows.
export async function getMyPermissions(): Promise<MyPermissions> {
  // Denied identity: no capabilities and the NARROWEST row scope. A denied
  // fetch must never be reported to the UI as "sees every record".
  const denied: MyPermissions = { capabilities: [], data_scope: 'own', role_id: '', role_name: '', is_owner: false };
  try {
    const res = await apiFetch('/api/auth/capabilities');
    if (!res.ok) return denied;
    const json = await parseJsonSafe(res);
    const d = json.data || {};
    return {
      capabilities: (d.capabilities || []) as string[],
      data_scope: asDataScope(d.data_scope),
      role_id: (d.role_id || '') as string,
      role_name: (d.role_name || '') as string,
      is_owner: !!d.is_owner,
      // Keep undefined (≠ {}) when the server doesn't send it: undefined means
      // "unknown, don't hide buttons"; {} means "known: no object access at all".
      objects: d.objects ? (d.objects as Record<string, ObjectAccessBits>) : undefined,
    };
  } catch {
    return denied;
  }
}

// getRoleOptions returns the minimal role list any member may read for pickers
// (member/invite dropdowns, the report Share dialog). Unblocks the callers that
// can't hit the roles.manage-gated getRoles (P6).
export async function getRoleOptions(): Promise<RoleOption[]> {
  const res = await apiFetch('/api/roles/options');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load roles');
  return asArray<RoleOption>(json.data, 'GET /api/roles/options');
}

// getRolesCatalog returns the capability metadata (labels/descriptions/groups/
// sensitive flags) + group display order — any member (P6).
export async function getRolesCatalog(): Promise<{ capabilities: CapabilityInfo[]; groups: string[] }> {
  const res = await apiFetch('/api/roles/catalog');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load capability catalog');
  const d = json.data || {};
  return { capabilities: (d.capabilities || []) as CapabilityInfo[], groups: (d.groups || []) as string[] };
}

export async function getRoles(): Promise<RoleDetail[]> {
  const res = await apiFetch('/api/roles');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load roles');
  return (json.data || []) as RoleDetail[];
}

export async function createRole(input: {
  name: string;
  description?: string;
  clone_from_id?: string;
  data_scope?: DataScope;
  capabilities?: string[];
}): Promise<RoleDetail> {
  const res = await apiFetch('/api/roles', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create role');
  return json.data as RoleDetail;
}

// duplicateRole clones a role (system or custom) into a new custom role,
// optionally moving the source's members onto the copy (P6).
export async function duplicateRole(id: string, input: { name: string; reassign_members?: boolean }): Promise<RoleDetail> {
  const res = await apiFetch(`/api/roles/${id}/duplicate`, { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to duplicate role');
  return json.data as RoleDetail;
}

export async function updateRole(id: string, input: { name?: string; description?: string; data_scope?: DataScope }): Promise<void> {
  const res = await apiFetch(`/api/roles/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to update role');
  }
}

// deleteRole deletes a custom role. When members still hold it, pass
// reassignToRoleId to move them onto another role in the same transaction; without
// it the server 409s (the "N people have this role — move them to:" flow, P6).
export async function deleteRole(id: string, reassignToRoleId?: string): Promise<void> {
  const qs = reassignToRoleId ? `?reassign_to=${encodeURIComponent(reassignToRoleId)}` : '';
  const res = await apiFetch(`/api/roles/${id}${qs}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete role');
  }
}

export async function setRoleCapabilities(id: string, capabilities: string[]): Promise<void> {
  const res = await apiFetch(`/api/roles/${id}/capabilities`, {
    method: 'PUT',
    body: JSON.stringify({ capabilities }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to save capabilities');
  }
}

// ============================================================
// Effective access per role (U3.2) — one merged payload answering "what can
// this role actually see?": identity + capabilities + per-object OLS bits +
// field restrictions + layout assignments. Owner is served synthesized full
// access (the permission tables hold no owner rows). roles.manage gated.
// ============================================================

export interface RoleAccessObject extends ObjectAccessBits {
  slug: string;
  label: string;
  icon: string;
  is_system: boolean;
  restricted_fields: Array<{ key: string; label: string; level: 'hidden' | 'read' }>;
}

export interface RoleAccessLayout {
  object_slug: string;
  layout_id: string;
  layout_name: string;
}

export interface RoleAccess {
  role: RoleDetail;
  objects: RoleAccessObject[];
  layouts: RoleAccessLayout[];
}

export async function getRoleAccess(id: string): Promise<RoleAccess> {
  const res = await apiFetch(`/api/roles/${id}/access`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load role access');
  const d = (json.data || {}) as Partial<RoleAccess>;
  return {
    role: (d.role || {}) as RoleDetail,
    objects: (d.objects || []).map((o) => ({ ...o, restricted_fields: o.restricted_fields || [] })),
    layouts: d.layouts || [],
  };
}

// ============================================================
// Record shares (U6) — grant ONE record to a user, role or group at view/edit.
// Parity with report sharing (same target vocabulary), minus 'comment', which is
// a reports-only level. Re-sharing an existing target upserts its level. The
// server rejects sharing to yourself and to the record's own owner.
// ============================================================

// RecordLevel is what a share grants (and what effective_level reports back;
// 'manage' is only ever produced by the server, never granted through a share).
export type RecordLevel = 'view' | 'edit' | 'manage';
export type RecordShareLevel = Extract<RecordLevel, 'view' | 'edit'>;

export interface RecordShareView {
  id: string;
  target_type: ShareTargetType;
  target_id: string;
  target_name: string;
  level: RecordShareLevel;
  created_at: string;
}

export async function getRecordShares(slug: string, id: string): Promise<RecordShareView[]> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/shares`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load shares');
  return (json.data || []) as RecordShareView[];
}

// shareRecord grants (or re-levels — the server upserts) one record to a target.
// The level is required on purpose: silently defaulting it is how a caller ends
// up granting more (or less) than the UI showed.
export async function shareRecord(
  slug: string,
  id: string,
  targetType: ShareTargetType,
  targetId: string,
  level: RecordShareLevel,
): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/share`, {
    method: 'POST',
    body: JSON.stringify({ target_type: targetType, target_id: targetId, level }),
  });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to share record');
  }
}

export async function unshareRecord(slug: string, id: string, shareID: string): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${slug}/records/${id}/share/${shareID}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to revoke share');
  }
}

// SharedRecordView is one row of "Shared with me" (U6): a record someone ELSE
// owns that reached the caller through a share (direct, via their role, or via
// a group), across every object.
export interface SharedRecordView {
  object_slug: string;
  object_label: string;
  record_id: string;
  display: string;
  level: RecordShareLevel;
  owner_name: string;
  updated_at: string;
}

export async function listSharedWithMe(
  slug?: string,
  limit?: number,
  offset?: number,
): Promise<{ records: SharedRecordView[]; total: number }> {
  const search = new URLSearchParams();
  if (slug) search.set('slug', slug);
  if (limit != null) search.set('limit', String(limit));
  if (offset != null) search.set('offset', String(offset));
  const qs = search.toString();
  const res = await apiFetch(`/api/registry/shared-with-me${qs ? '?' + qs : ''}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load shared records');
  return { records: (json.data || []) as SharedRecordView[], total: (json.total ?? 0) as number };
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load field permissions');
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
    throw apiError(res, json, 'Failed to save field permission');
  }
}

// bulkSetFieldPermissions sets one level for many fields of one role in a single
// request/transaction (U3.4 "set column") — one cache bust + one audit event
// server-side, instead of N of each from a PUT-per-cell loop.
export async function bulkSetFieldPermissions(input: {
  object_slug: string;
  role_id: string;
  field_keys: string[];
  level: FieldLevel;
}): Promise<void> {
  const res = await apiFetch(`/api/registry/objects/${input.object_slug}/field-permissions/bulk`, {
    method: 'PUT',
    body: JSON.stringify({ role_id: input.role_id, field_keys: input.field_keys, level: input.level }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to save field permissions');
  }
}

// getFieldPermissionSummary returns restriction counts per object slug (owner
// rows excluded) for the badges on the Field security object pills (U3.4).
export async function getFieldPermissionSummary(): Promise<Record<string, number>> {
  const res = await apiFetch('/api/registry/permissions/field-summary');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load field security summary');
  return ((json.data || {}).counts || {}) as Record<string, number>;
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load audit trail');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch KB sections');
  return json.data || [];
}

export async function getKBSection(section: string): Promise<KBEntry> {
  const res = await apiFetch(`/api/knowledge-base/${section}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Section not found');
  return json.data as KBEntry;
}

export async function upsertKBSection(section: string, data: { title: string; content: string }): Promise<KBEntry> {
  const res = await apiFetch(`/api/knowledge-base/${section}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(data),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to save section');
  return json.data as KBEntry;
}

export async function getKBAIPrompt(): Promise<string> {
  const res = await apiFetch('/api/knowledge-base/ai-prompt');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch AI prompt');
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
        redirectToLoginExpired();
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
    throw apiError(res, json, 'Failed to end session');
  }
}

export async function listChatSessions(page = 1, limit = 50): Promise<{ data: ChatSession[]; total: number }> {
  const res = await apiFetch(`/api/ai/sessions?page=${page}&limit=${limit}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch sessions');
  return { data: json.data || [], total: json.total || 0 };
}

export async function getChatSessionMessages(sessionId: string): Promise<ChatMessageItem[]> {
  const res = await apiFetch(`/api/ai/sessions/${sessionId}/messages`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch messages');
  return json.data || [];
}

export async function deleteChatSession(sessionId: string): Promise<void> {
  const res = await apiFetch(`/api/ai/sessions/${sessionId}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete session');
  }
}


export interface Workspace {
  org_id: string;
  org_name: string;
  org_type: string;
  role: string;
  status: string;
  member_count?: number;
}

export interface WorkspaceMember {
  user_id: string;
  email: string;
  first_name: string;
  last_name: string;
  full_name: string;
  avatar_url?: string;
  // role_id is the authoritative identity (member assignment is keyed by it, P6);
  // role is its name, for display.
  role_id: string;
  role: string;
  status: string;
  // Members-table columns (U4). Optional on the type (the server always sends
  // joined_at/email_verified; older callers and test fixtures may omit them) —
  // every consumer guards for undefined.
  joined_at?: string;
  email_verified?: boolean;
  last_active_at?: string;
  // Whether the member has enrolled in 2FA (U6.4) — the column an admin needs
  // before turning the workspace policy on. Optional for the same reason as the
  // U4 columns: older callers and test fixtures omit it, so consumers guard.
  two_factor_enabled?: boolean;
}

// MemberGroup / MemberDetail back the member detail drawer (U4).
export interface MemberGroup {
  id: string;
  name: string;
}

export interface MemberSession {
  id: string;
  device_label: string;
  ip: string;
  last_used_at?: string;
  created_at: string;
}

export interface MemberDetail {
  member: WorkspaceMember;
  groups: MemberGroup[];
  owned_contacts: number;
  owned_deals: number;
  sessions: MemberSession[];
}

export async function getMemberDetail(userId: string): Promise<MemberDetail> {
  const res = await apiFetch(`/api/workspaces/members/${userId}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load member');
  const d = (json.data || {}) as Partial<MemberDetail>;
  return {
    member: d.member as WorkspaceMember,
    groups: d.groups || [],
    owned_contacts: d.owned_contacts || 0,
    owned_deals: d.owned_deals || 0,
    sessions: d.sessions || [],
  };
}

// forceSignOutMember revokes all the member's sessions + bumps their token
// version so their access dies immediately (U4).
export async function forceSignOutMember(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/sessions`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to sign out member');
  }
}

export async function switchWorkspace(orgId: string, setDefault = false): Promise<{
  access_token: string;
  refresh_token: string;
  user: UserListItem;
  workspaces: Workspace[];
  active_org_id: string;
  default_org_id?: string;
  needs_chooser: boolean;
}> {
  const res = await apiFetch('/api/auth/switch-workspace', {
    method: 'POST',
    body: JSON.stringify({ org_id: orgId, set_default: setDefault }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to switch workspace');
  return json.data;
}

export async function getWorkspaces(): Promise<Workspace[]> {
  const res = await apiFetch('/api/workspaces');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch workspaces');
  return (json.data || []) as Workspace[];
}

// WorkspaceDetail backs the Workspace General settings page (U4).
export interface WorkspaceDetail {
  id: string;
  name: string;
  type: string;
  currency: string;
  locale: string;
  timezone: string;
  member_count: number;
  is_owner: boolean;
  // The workspace 2FA policy (U6.4): every member must enroll, and one who
  // hasn't is confined to the enrollment screen. org.settings-gated.
  require_two_factor: boolean;
  created_at: string;
}

export async function getCurrentWorkspace(): Promise<WorkspaceDetail> {
  const res = await apiFetch('/api/workspaces/current');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load workspace');
  return json.data as WorkspaceDetail;
}

export type UpdateWorkspaceInput = Partial<
  Pick<WorkspaceDetail, 'name' | 'currency' | 'locale' | 'timezone' | 'require_two_factor'>
>;

export async function updateWorkspace(patch: UpdateWorkspaceInput): Promise<void> {
  const res = await apiFetch('/api/workspaces/current', { method: 'PATCH', body: JSON.stringify(patch) });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to update workspace');
  }
}

// leaveWorkspace / deleteWorkspace are lifecycle actions (U4). Leaving is
// last-owner-guarded; deleting is owner-only — both enforced server-side.
export async function leaveWorkspace(): Promise<void> {
  const res = await apiFetch('/api/workspaces/leave', { method: 'POST' });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to leave workspace');
  }
}

export async function deleteWorkspace(): Promise<void> {
  const res = await apiFetch('/api/workspaces/current', { method: 'DELETE' });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to delete workspace');
  }
}

export async function getWorkspaceMembers(): Promise<WorkspaceMember[]> {
  const res = await apiFetch('/api/workspaces/members');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch members');
  return asArray<WorkspaceMember>(json.data, 'GET /api/workspaces/members');
}

// getTeammates returns only the members the caller shares a team (user group)
// with — the assignable set for a 'team'-scoped role (U6). Same shape as the
// unfiltered call.
export async function getTeammates(): Promise<WorkspaceMember[]> {
  const res = await apiFetch('/api/workspaces/members?scope=teammates');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch teammates');
  return (json.data || []) as WorkspaceMember[];
}

export async function inviteMember(email: string, roleId: string): Promise<{ member: WorkspaceMember; debug_token?: string }> {
  const res = await apiFetch('/api/workspaces/invites', {
    method: 'POST',
    body: JSON.stringify({ email, role_id: roleId }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to invite member');
  return json.data;
}

export async function updateMemberRole(userId: string, roleId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/role`, {
    method: 'PATCH',
    body: JSON.stringify({ role_id: roleId }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to update role');
  }
}

// Thrown when the backend answers the remove with 409 REASSIGNMENT_REQUIRED:
// the member still owns records and a strategy must be chosen. Carries the real
// owned-record counts; the modal keys off this class, never an error-message
// substring (U0.2).
export class ReassignmentRequiredError extends Error {
  code = 'REASSIGNMENT_REQUIRED' as const;
  owned: { contacts: number; deals: number };
  constructor(message: string, owned: { contacts: number; deals: number }) {
    super(message);
    this.name = 'ReassignmentRequiredError';
    this.owned = owned;
  }
}

export async function removeMember(userId: string, input?: { strategy?: 'transfer' | 'unassign'; reassign_to_user_id?: string }): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}`, {
    method: 'DELETE',
    body: input ? JSON.stringify(input) : undefined
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    if (json.code === 'REASSIGNMENT_REQUIRED') {
      throw new ReassignmentRequiredError(json.error || 'This member still owns records.', {
        contacts: json.owned?.contacts ?? 0,
        deals: json.owned?.deals ?? 0,
      });
    }
    throw apiError(res, json, 'Failed to remove member');
  }
}

export async function suspendMember(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/suspend`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to suspend member');
  }
}

export async function reinstateMember(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/reinstate`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to reinstate member');
  }
}

export async function transferOwnership(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/transfer`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to transfer ownership');
  }
}

// InvitationPreview is the public accept-page metadata (U4): who invited you to
// what, whether the link is still good, and whether your email already has an
// account. Read by raw token before committing to the invite.
export interface InvitationPreview {
  email: string;
  org_name: string;
  role_name: string;
  // valid | expired | revoked | accepted | invalid
  status: 'valid' | 'expired' | 'revoked' | 'accepted' | 'invalid';
  has_account: boolean;
}

export async function getInvitationPreview(token: string): Promise<InvitationPreview> {
  const res = await fetch(`${API_URL}/api/auth/invitations/${encodeURIComponent(token)}`, {
    headers: { 'Content-Type': 'application/json' },
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load invitation');
  return (json.data || { status: 'invalid' }) as InvitationPreview;
}

// IncomingInvitation is a pending invitation addressed to the logged-in user's own
// email (U4 item 6): the workspace + role they'd join. Distinct from Invitation (an
// admin's view of a workspace's OUTGOING invites).
export interface IncomingInvitation {
  id: string;
  org_id: string;
  org_name: string;
  role_name: string;
  expires_at: string;
}

// getMyInvitations lists the pending invitations addressed to the logged-in user's
// OWN email (U4 item 6) — the post-OAuth / zero-workspace "you've been invited to X"
// consent surface. Served under org-optional auth, so a zero-membership caller (e.g.
// a brand-new Google invitee with no personal org) can reach it. Accepting one goes
// through useAuth().acceptMyInvitation, which ingests the joined-workspace session.
export async function getMyInvitations(): Promise<IncomingInvitation[]> {
  const res = await apiFetch('/api/auth/me/invitations');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load invitations');
  return (json.data || []) as IncomingInvitation[];
}

// Invite-accept happens through useAuth().acceptInvitation (U4), which ingests
// the server's auto-login session — there is no standalone acceptInvite() fetch.

// Pending-invitation lifecycle (P2) — powers the members-settings panel.
export interface Invitation {
  id: string;
  email: string;
  role_id: string;
  role: string;
  status: string;
  expires_at: string;
  created_at: string;
  resent_at?: string;
}

export async function listInvitations(): Promise<Invitation[]> {
  const res = await apiFetch('/api/workspaces/invitations');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch invitations');
  return asArray<Invitation>(json.data, 'GET /api/workspaces/invitations');
}

export async function resendInvitation(id: string): Promise<{ debug_token?: string }> {
  const res = await apiFetch(`/api/workspaces/invitations/${id}/resend`, { method: 'POST' });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw apiError(res, json, 'Failed to resend invitation');
  return json.data || {};
}

export async function revokeInvitation(id: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/invitations/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to revoke invitation');
  }
}

// Admin "Send reset link" (P2): emails the member a self-serve reset. The admin
// never sees or sets the password — accounts are global across workspaces.
export async function sendMemberResetLink(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/send-reset-link`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to send reset link');
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
    throw apiError(res, json, 'Failed to send reset link');
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
    throw apiError(res, json, 'Failed to reset password');
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
    throw apiError(res, json, 'Failed to verify email');
  }
}

// resendVerification is called by a logged-in user from the banner, so it uses
// apiFetch to attach the bearer token.
export async function resendVerification(): Promise<void> {
  const res = await apiFetch(`/api/auth/resend-verification`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to resend verification email');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to fetch voice notes');
  return (json.data || []) as VoiceNote[];
}

export async function getVoiceNote(id: string): Promise<VoiceNote> {
  const res = await apiFetch(`/api/voice/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Voice note not found');
  return json.data as VoiceNote;
}

export async function applyVoiceNoteUpdates(id: string): Promise<void> {
  const res = await apiFetch(`/api/voice/${id}/apply-updates`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to apply updates');
  }
}

export async function deleteVoiceNote(id: string): Promise<void> {
  const res = await apiFetch(`/api/voice/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete voice note');
  }
}

export async function analyzeVoiceNote(id: string): Promise<void> {
  const res = await apiFetch(`/api/voice/${id}/analyze`, { method: 'POST' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to start analysis');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Search failed');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load audit log');
  return { events: (json.data || []) as AuditEvent[], total: (json.total || 0) as number };
}

// exportAuditCsv downloads the filtered audit log as a CSV blob (same filters as
// the on-screen view, minus pagination).
export async function exportAuditCsv(f: AuditEventFilters = {}): Promise<Blob> {
  const { limit: _l, offset: _o, ...rest } = f;
  const res = await apiFetch(`/api/audit/events/export.csv?${auditQuery(rest)}`);
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Export failed');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load sessions');
  return (json.data || []) as UserSession[];
}

export async function revokeSession(id: string): Promise<void> {
  const res = await apiFetch(`/api/auth/sessions/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to revoke session');
  }
}

// signOutEverywhere revokes all other sessions and re-mints this one; the server
// returns a fresh access token that the caller must adopt so the current tab
// keeps working (its old access token was invalidated by the token_version bump).
export async function signOutEverywhere(): Promise<void> {
  const res = await apiFetch('/api/auth/sessions', { method: 'DELETE' });
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw apiError(res, json, 'Failed to sign out other sessions');
  const token = (json?.data?.access_token as string) ?? null;
  if (token) setAccessToken(token);
}

// ============================================================
// My Account (U2) — self-serve profile, password, connected accounts
// ============================================================

export interface AuthMethods {
  password: boolean;
  google: boolean;
}

export interface ProfileUser {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  full_name?: string;
  avatar_url?: string;
  timezone: string;
  locale: string;
  onboarding_completed: boolean;
  email_verified_at?: string | null;
}

export interface MeResponse {
  user: ProfileUser;
  auth_methods: AuthMethods;
}

export async function getMe(): Promise<MeResponse> {
  const res = await apiFetch('/api/auth/me');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load your account');
  return json.data as MeResponse;
}

export interface UpdateProfileInput {
  first_name?: string;
  last_name?: string;
  avatar_url?: string;
  timezone?: string;
  locale?: string;
  onboarding_completed?: boolean;
}

export async function updateProfile(input: UpdateProfileInput): Promise<ProfileUser> {
  const res = await apiFetch('/api/auth/me', { method: 'PATCH', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to save your profile');
  return json.data.user as ProfileUser;
}

// changePassword / setPassword: the server signs out every other device and
// re-mints THIS one — adopt the fresh access token or the current tab dies on
// its next request (token_version bump).
export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await apiFetch('/api/auth/change-password', {
    method: 'POST',
    body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to change password');
  const token = (json?.data?.access_token as string) ?? null;
  if (token) setAccessToken(token);
}

export async function setPassword(newPassword: string): Promise<void> {
  const res = await apiFetch('/api/auth/set-password', {
    method: 'POST',
    body: JSON.stringify({ new_password: newPassword }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to set password');
  const token = (json?.data?.access_token as string) ?? null;
  if (token) setAccessToken(token);
}

export async function unlinkGoogle(): Promise<void> {
  const res = await apiFetch('/api/auth/unlink-google', { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to disconnect Google');
}

// ============================================================
// Two-factor authentication (U6.4)
// ============================================================
// TOTP + single-use backup codes, plus a workspace policy that can require it of
// everyone. Login gains a step: a 2FA-enrolled user gets a short-lived CHALLENGE
// instead of a session, and only a correct code exchanges it for tokens.

export interface TwoFactorStatus {
  enabled: boolean;
  enabled_at?: string;
  backup_codes_left: number;
  /** The workspace policy demands 2FA — the user may not turn theirs off. */
  required_by_workspace: boolean;
}

// TwoFactorSetupInfo is the enrollment payload. qr_data_uri is a ready-to-render
// PNG data URI — the SERVER draws the QR, so the app ships no QR library.
export interface TwoFactorSetupInfo {
  secret: string;
  otpauth_url: string;
  qr_data_uri: string;
}

// SessionUser mirrors the user object in an auth payload. Structurally identical
// to auth.tsx's User, so a verified challenge can be fed straight into saveAuth.
export interface SessionUser {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  full_name?: string;
  role?: string;
  avatar_url?: string;
  email_verified_at?: string | null;
  timezone?: string;
  locale?: string;
  onboarding_completed?: boolean;
}

// AuthSessionPayload is the `data` of a full auth response (login / 2FA verify).
export interface AuthSessionPayload {
  access_token: string;
  refresh_token: string;
  user: SessionUser;
  workspaces?: Workspace[];
  active_org_id?: string;
  default_org_id?: string;
  needs_chooser?: boolean;
  // A CHALLENGE, not a session: access_token/refresh_token are EMPTY and
  // challenge_token must be exchanged at POST /auth/2fa/verify (U6.4).
  two_factor_required?: boolean;
  challenge_token?: string;
  // A real session whose workspace requires 2FA the user hasn't set up: signed
  // in, but confined to enrolling.
  two_factor_enroll_required?: boolean;
}

// TwoFactorVerifyError carries the HTTP status so the challenge screen can tell a
// wrong code (401 — try again) from a dead challenge (429 — five wrong codes, the
// challenge is gone; sign in again) without matching on message text.
export class TwoFactorVerifyError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = 'TwoFactorVerifyError';
    this.status = status;
  }
}

// verifyTwoFactor exchanges a login challenge for a real session. Public by
// necessity (there is no session yet), so it uses a bare fetch — apiFetch would
// attach a stale bearer and hard-redirect on 401, which here is just "wrong code".
// credentials:'include' both receives the new refresh cookie AND sends the
// httpOnly 2fa_challenge cookie the Google redirect flow uses in place of a body
// token (challengeToken is '' in that flow).
export async function verifyTwoFactor(challengeToken: string, code: string): Promise<AuthSessionPayload> {
  const res = await fetch(`${API_URL}/api/auth/2fa/verify`, {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ challenge_token: challengeToken, code }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok || json.error) {
    throw new TwoFactorVerifyError(json.error || 'That code isn’t right', res.status);
  }
  return json.data as AuthSessionPayload;
}

export async function getTwoFactorStatus(): Promise<TwoFactorStatus> {
  const res = await apiFetch('/api/auth/2fa');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load two-factor status');
  return json.data as TwoFactorStatus;
}

export async function startTwoFactorSetup(): Promise<TwoFactorSetupInfo> {
  const res = await apiFetch('/api/auth/2fa/setup', { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to start two-factor setup');
  return json.data as TwoFactorSetupInfo;
}

// enableTwoFactor confirms enrollment and returns the backup codes — the ONLY
// time they exist in plaintext. Show them once, then they're gone forever.
export async function enableTwoFactor(code: string): Promise<string[]> {
  const res = await apiFetch('/api/auth/2fa/enable', { method: 'POST', body: JSON.stringify({ code }) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to turn on two-factor authentication');
  return (json.data?.codes || []) as string[];
}

// disableTwoFactor requires a live code (TOTP or backup): holding a session is not
// enough to drop a second factor. Refused with 403 when the workspace requires 2FA.
export async function disableTwoFactor(code: string): Promise<void> {
  const res = await apiFetch('/api/auth/2fa/disable', { method: 'POST', body: JSON.stringify({ code }) });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to turn off two-factor authentication');
  }
}

// regenerateBackupCodes invalidates every existing code and returns a fresh set.
export async function regenerateBackupCodes(code: string): Promise<string[]> {
  const res = await apiFetch('/api/auth/2fa/backup-codes', { method: 'POST', body: JSON.stringify({ code }) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to regenerate backup codes');
  return (json.data?.codes || []) as string[];
}

// resetMemberTwoFactor is the admin break-glass (members.manage) for a member who
// lost both their authenticator and their backup codes.
export async function resetMemberTwoFactor(userId: string): Promise<void> {
  const res = await apiFetch(`/api/workspaces/members/${userId}/two-factor`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, "Failed to reset the member's two-factor authentication");
  }
}

// ============================================================
// Personal API tokens (U6.5)
// ============================================================
// A token authenticates as its OWNER and can only ever do a SUBSET of what its
// owner can: its scopes intersect their real permissions. The creation form
// therefore offers only scopes the caller actually holds — the server rejects
// anything else with a 403.

export interface APIToken {
  id: string;
  name: string;
  /** Display hint ("crm_pat_a1b2…") — enough to recognize, useless as a credential. */
  prefix: string;
  scopes: string[];
  last_used_at?: string;
  expires_at?: string;
  revoked_at?: string;
  created_at: string;
}

// CreatedAPIToken carries the plaintext secret — returned EXACTLY ONCE.
export interface CreatedAPIToken {
  token: APIToken;
  secret: string;
}

export interface CreateAPITokenInput {
  name: string;
  scopes: string[];
  expires_in_days?: number;
}

// SCOPE_RECORDS_READ is a TOKEN-ONLY scope with no role capability behind it:
// reading records is gated by OLS, not a capability, so without it a narrowly
// scoped token would still read everything its owner can. Anyone may grant it —
// it confers nothing its owner doesn't already have.
export const SCOPE_RECORDS_READ = 'records.read';

// Every scope a token may carry: the role capability catalog plus records.read.
export const ALL_API_TOKEN_SCOPES: string[] = [SCOPE_RECORDS_READ, ...ALL_CAPABILITIES];

export const API_TOKEN_SCOPE_LABELS: Record<string, string> = {
  ...CAPABILITY_LABELS,
  [SCOPE_RECORDS_READ]: 'Read records',
};

// Server-side limits, mirrored for the UI (domain.MaxAPITokensPerUser / DefaultAPITokenDays).
export const MAX_API_TOKENS_PER_USER = 20;
export const DEFAULT_API_TOKEN_DAYS = 90;

export async function listApiTokens(): Promise<APIToken[]> {
  const res = await apiFetch('/api/auth/api-tokens');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load API tokens');
  return (json.data || []) as APIToken[];
}

export async function createApiToken(input: CreateAPITokenInput): Promise<CreatedAPIToken> {
  const res = await apiFetch('/api/auth/api-tokens', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create API token');
  return json.data as CreatedAPIToken;
}

export async function revokeApiToken(id: string): Promise<void> {
  const res = await apiFetch(`/api/auth/api-tokens/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await parseJsonSafe(res);
    throw apiError(res, json, 'Failed to revoke API token');
  }
}

// isTokenLive: a token still authenticates unless it was revoked or has expired.
export function isTokenLive(t: APIToken, now: number = Date.now()): boolean {
  if (t.revoked_at) return false;
  if (t.expires_at && new Date(t.expires_at).getTime() <= now) return false;
  return true;
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
  // The caller's effective level (view/comment/edit/manage), populated by
  // GET /api/reports/:id. Drives whether the Share/Edit controls appear.
  access_level?: 'view' | 'comment' | 'edit' | 'manage';
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load reports');
  return (json.data || []) as Report[];
}

export async function createReport(input: ReportInput): Promise<Report> {
  const res = await apiFetch('/api/reports', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create report');
  return json.data as Report;
}

export async function getReport(id: string): Promise<Report> {
  const res = await apiFetch(`/api/reports/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load report');
  return json.data as Report;
}

export async function updateReport(id: string, input: ReportInput): Promise<Report> {
  const res = await apiFetch(`/api/reports/${id}`, { method: 'PATCH', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update report');
  return json.data as Report;
}

export async function deleteReport(id: string): Promise<void> {
  const res = await apiFetch(`/api/reports/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to delete report');
  }
}

export async function runReport(id: string): Promise<ReportResult> {
  const res = await apiFetch(`/api/reports/${id}/run`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to run report');
  return json.data as ReportResult;
}

// previewReport runs an unsaved config — the builder's live preview.
export async function previewReport(objectSlug: string, config: ReportConfig): Promise<ReportResult> {
  const res = await apiFetch('/api/reports/preview', {
    method: 'POST',
    body: JSON.stringify({ object_slug: objectSlug, config }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Preview failed');
  return json.data as ReportResult;
}

export async function listReportFields(slug: string): Promise<ReportFieldDescriptor[]> {
  const res = await apiFetch(`/api/reports/objects/${slug}/fields`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load report fields');
  return (json.data || []) as ReportFieldDescriptor[];
}

export async function exportReportCsv(id: string): Promise<Blob> {
  const res = await apiFetch(`/api/reports/${id}/export.csv`);
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Export failed');
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load dashboard');
  return (json.data || []) as DashboardWidget[];
}

export async function addDashboardWidget(reportId: string, size: 'half' | 'full' = 'half'): Promise<DashboardWidget> {
  const res = await apiFetch('/api/dashboard/widgets', {
    method: 'POST',
    body: JSON.stringify({ report_id: reportId, size }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to pin report');
  return json.data as DashboardWidget;
}

export async function updateDashboardWidget(id: string, size: 'half' | 'full'): Promise<void> {
  const res = await apiFetch(`/api/dashboard/widgets/${id}`, {
    method: 'PATCH',
    body: JSON.stringify({ size }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to update widget');
  }
}

export async function removeDashboardWidget(id: string): Promise<void> {
  const res = await apiFetch(`/api/dashboard/widgets/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to remove widget');
  }
}

export async function reorderDashboardWidgets(widgetIds: string[]): Promise<void> {
  const res = await apiFetch('/api/dashboard/widgets/reorder', {
    method: 'PUT',
    body: JSON.stringify({ widget_ids: widgetIds }),
  });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw apiError(res, json, 'Failed to reorder widgets');
  }
}

// ============================================================
// User Groups — named member groups. These ARE the "teams" (U6): they define
// the 'team' data scope (a team-scoped role sees every record owned by anyone
// who shares a group with them) AND they are a share target for both records
// and reports.
// ============================================================

export interface GroupMember {
  user_id: string;
  name: string;
  email: string;
}

export interface UserGroup {
  id: string;
  name: string;
  description: string;
  member_count: number;
  members: GroupMember[];
  created_at: string;
}

export async function listGroups(): Promise<UserGroup[]> {
  const res = await apiFetch('/api/groups');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load groups');
  // members: the backend marshals a zero-member group's nil slice as null —
  // normalize here so no consumer can trip on .map during render.
  return asArray<UserGroup>(json.data, 'GET /api/groups').map((g) => ({
    ...g,
    members: g.members ?? [],
  }));
}

export async function createGroup(name: string, description = ''): Promise<UserGroup> {
  const res = await apiFetch('/api/groups', { method: 'POST', body: JSON.stringify({ name, description }) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create group');
  return json.data as UserGroup;
}

export async function updateGroup(id: string, name: string, description = ''): Promise<void> {
  const res = await apiFetch(`/api/groups/${id}`, { method: 'PATCH', body: JSON.stringify({ name, description }) });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to update group'); }
}

export async function deleteGroup(id: string): Promise<void> {
  const res = await apiFetch(`/api/groups/${id}`, { method: 'DELETE' });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to delete group'); }
}

export async function addGroupMember(groupId: string, userId: string): Promise<void> {
  const res = await apiFetch(`/api/groups/${groupId}/members`, { method: 'POST', body: JSON.stringify({ user_id: userId }) });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to add member'); }
}

export async function removeGroupMember(groupId: string, userId: string): Promise<void> {
  const res = await apiFetch(`/api/groups/${groupId}/members/${userId}`, { method: 'DELETE' });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to remove member'); }
}

// ── Report shares (granular sharing: users/roles/groups × view/comment/edit) ──

export type ShareTargetType = 'user' | 'role' | 'group';
export type ShareLevel = 'view' | 'comment' | 'edit';

export interface ReportShareView {
  id: string;
  target_type: ShareTargetType;
  target_id: string;
  target_name: string;
  level: ShareLevel;
  created_at: string;
}

export async function listReportShares(reportId: string): Promise<ReportShareView[]> {
  const res = await apiFetch(`/api/reports/${reportId}/shares`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load shares');
  return (json.data || []) as ReportShareView[];
}

export async function addReportShare(reportId: string, targetType: ShareTargetType, targetId: string, level: ShareLevel): Promise<void> {
  const res = await apiFetch(`/api/reports/${reportId}/shares`, {
    method: 'POST',
    body: JSON.stringify({ target_type: targetType, target_id: targetId, level }),
  });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to share report'); }
}

export async function removeReportShare(reportId: string, shareId: string): Promise<void> {
  const res = await apiFetch(`/api/reports/${reportId}/shares/${shareId}`, { method: 'DELETE' });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to remove share'); }
}

// ── Report comments (thread on a saved report; posting needs level >= comment) ──

export interface ReportCommentView {
  id: string;
  author_id?: string;
  author_name: string;
  body: string;
  created_at: string;
  can_delete: boolean;
}

export async function listReportComments(reportId: string): Promise<ReportCommentView[]> {
  const res = await apiFetch(`/api/reports/${reportId}/comments`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load comments');
  return (json.data || []) as ReportCommentView[];
}

export async function addReportComment(reportId: string, body: string): Promise<void> {
  const res = await apiFetch(`/api/reports/${reportId}/comments`, {
    method: 'POST',
    body: JSON.stringify({ body }),
  });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to post comment'); }
}

export async function deleteReportComment(reportId: string, commentId: string): Promise<void> {
  const res = await apiFetch(`/api/reports/${reportId}/comments/${commentId}`, { method: 'DELETE' });
  if (!res.ok) { const j = await res.json().catch(() => ({})); throw apiError(res, j, 'Failed to delete comment'); }
}
