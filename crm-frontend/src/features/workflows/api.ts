import type { Workflow, WorkflowRun, RunDetailResponse, WorkflowListResponse, TestRunResponse, ActionSpec, TriggerSpec, ConditionGroup } from './types';

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

async function apiFetch(path: string, options: RequestInit = {}): Promise<Response> {
  const token = localStorage.getItem('access_token');
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options.headers as Record<string, string> || {}),
  };
  if (token) headers['Authorization'] = `Bearer ${token}`;
  return fetch(`${API_URL}${path}`, { ...options, headers });
}

// --- Workflow CRUD ---

export async function getWorkflows(params?: { active?: boolean; q?: string; page?: number; size?: number }): Promise<WorkflowListResponse> {
  const qs = new URLSearchParams();
  if (params?.active !== undefined) qs.set('active', String(params.active));
  if (params?.q) qs.set('q', params.q);
  if (params?.page) qs.set('page', String(params.page));
  if (params?.size) qs.set('size', String(params.size));
  const res = await apiFetch(`/api/workflows?${qs.toString()}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch workflows');
  return json.data as WorkflowListResponse;
}

export async function getWorkflow(id: string): Promise<Workflow> {
  const res = await apiFetch(`/api/workflows/${id}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch workflow');
  return json.data as Workflow;
}

export async function createWorkflow(data: {
  name: string;
  description?: string;
  trigger: TriggerSpec;
  conditions?: ConditionGroup | null;
  actions: ActionSpec[];
}): Promise<Workflow> {
  const res = await apiFetch('/api/workflows', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) {
    const details = json.error?.details;
    if (details?.length) {
      throw new Error(details.map((d: { message: string }) => d.message).join('; '));
    }
    throw new Error(json.error?.message || 'Failed to create workflow');
  }
  return json.data as Workflow;
}

export async function updateWorkflow(id: string, data: {
  name?: string;
  description?: string;
  trigger?: TriggerSpec;
  conditions?: ConditionGroup | null;
  actions?: ActionSpec[];
}): Promise<Workflow> {
  const res = await apiFetch(`/api/workflows/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await res.json();
  if (!res.ok) {
    const details = json.error?.details;
    if (details?.length) {
      throw new Error(details.map((d: { message: string }) => d.message).join('; '));
    }
    throw new Error(json.error?.message || 'Failed to update workflow');
  }
  return json.data as Workflow;
}

export async function deleteWorkflow(id: string): Promise<void> {
  const res = await apiFetch(`/api/workflows/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error?.message || 'Failed to delete workflow');
  }
}

export async function toggleWorkflow(id: string): Promise<Workflow> {
  const res = await apiFetch(`/api/workflows/${id}/toggle`, { method: 'POST' });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to toggle workflow');
  return json.data as Workflow;
}

export async function testRunWorkflow(id: string, context: Record<string, unknown>): Promise<TestRunResponse> {
  const res = await apiFetch(`/api/workflows/${id}/test-run`, {
    method: 'POST',
    body: JSON.stringify({ context }),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to run test');
  return json.data as TestRunResponse;
}

export interface RunNowResult {
  /** Created Workflow_Run id */
  id: string;
  /** Run status, e.g. "pending" */
  status: string;
}

/**
 * POST /api/workflows/:id/run — real, single-workflow execution against a sample
 * contact or deal. Exactly one of contact_id / deal_id must be provided.
 */
export async function runNowWorkflow(
  id: string,
  entity: { contact_id: string } | { deal_id: string },
): Promise<RunNowResult> {
  const res = await apiFetch(`/api/workflows/${id}/run`, {
    method: 'POST',
    body: JSON.stringify(entity),
  });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to run workflow');
  return json.data as RunNowResult;
}

// --- Runs ---

export async function getWorkflowRuns(workflowId: string, page = 1, size = 20): Promise<{
  runs: WorkflowRun[];
  total: number;
}> {
  const res = await apiFetch(`/api/workflows/${workflowId}/runs?page=${page}&size=${size}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch runs');
  return json.data as { runs: WorkflowRun[]; total: number };
}

export async function getRunDetail(runId: string): Promise<RunDetailResponse> {
  const res = await apiFetch(`/api/workflows/runs/${runId}`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch run detail');
  return json.data as RunDetailResponse;
}

export interface RetryRunResult {
  /** The re-queued run's id — the same run resumes; it is not cloned. */
  id: string;
  /** Run status after re-queueing, e.g. "pending". */
  status: string;
}

/**
 * POST /api/workflows/runs/:runId/retry — re-queue a FAILED run so it resumes from the
 * step that failed (P21). Completed steps are not re-executed. Admin/manager only; the
 * server rejects non-failed runs with 409.
 */
export async function retryRun(runId: string): Promise<RetryRunResult> {
  const res = await apiFetch(`/api/workflows/runs/${runId}/retry`, { method: 'POST' });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to retry run');
  return json.data as RetryRunResult;
}

// --- Inbound webhook setup (P17) ---

export interface WebhookTokenInfo {
  /** Org token embedded in the inbound URL path. */
  token: string;
  /** HMAC-SHA256 signing secret, masked for display (last 4 chars only). */
  secret_masked: string;
  /** Absolute URL external systems POST inbound webhooks to. */
  url: string;
}

export interface WebhookSecretInfo {
  /** Org token (unchanged by rotation; the inbound URL stays stable). */
  token: string;
  /** The full, freshly-rotated signing secret — returned exactly once. */
  secret: string;
  /** Absolute URL external systems POST inbound webhooks to. */
  url: string;
}

/**
 * GET /api/webhooks/token — returns (creating on first call) the org's inbound
 * webhook URL, token, and a MASKED signing secret. Admin/manager only. The full
 * secret is never returned here — use regenerateWebhookSecret() to obtain it.
 */
export async function getWebhookToken(): Promise<WebhookTokenInfo> {
  const res = await apiFetch('/api/webhooks/token');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch webhook token');
  return json.data as WebhookTokenInfo;
}

/**
 * POST /api/webhooks/reveal-secret — returns the org's CURRENT signing secret in
 * full, for on-demand reveal/copy in the setup UI. Does not rotate. Admin/manager
 * only. Kept separate from the (masked) token GET so the secret leaves the server
 * only on an explicit, auditable action.
 */
export async function revealWebhookSecret(): Promise<string> {
  const res = await apiFetch('/api/webhooks/reveal-secret', { method: 'POST' });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to reveal webhook secret');
  return (json.data as { secret: string }).secret;
}

/**
 * POST /api/webhooks/regenerate-secret — rotates the org's signing secret and
 * returns the new secret in FULL, exactly once. This invalidates the previous
 * secret (inbound requests signed with it stop verifying). Admin/manager only.
 */
export async function regenerateWebhookSecret(): Promise<WebhookSecretInfo> {
  const res = await apiFetch('/api/webhooks/regenerate-secret', { method: 'POST' });
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to regenerate webhook secret');
  return json.data as WebhookSecretInfo;
}

// --- Schema (for builder field pickers) ---

export interface SchemaField {
  path: string;
  label: string;
  type: 'string' | 'number' | 'boolean' | 'array' | 'select' | 'date';
  picker_type?: 'tag' | 'stage' | 'user';
  options?: string[];
  /** Minimum value for number fields (enforced in NumberInput) */
  min?: number;
  /** Maximum value for number fields (enforced in NumberInput) */
  max?: number;
}

export interface SchemaEntity {
  key: string;
  label: string;
  icon: string;
  fields: SchemaField[];
}

export interface SchemaStage {
  id: string;
  name: string;
  color: string;
  order: number;
}

export interface SchemaTag {
  id: string;
  name: string;
  color: string;
}

export interface SchemaUser {
  id: string;
  name: string;
  email: string;
}

export interface WorkflowSchema {
  entities: SchemaEntity[];
  custom_objects: SchemaEntity[];
  stages: SchemaStage[];
  tags: SchemaTag[];
  users: SchemaUser[];
}

export async function getWorkflowSchema(): Promise<WorkflowSchema> {
  const res = await apiFetch('/api/workflows/schema');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch schema');
  return json.data as WorkflowSchema;
}

// --- New API contracts: Objects & Fields ---

export interface ObjectItem {
  name: string;
  label: string;
  icon: string;
}

export interface FieldItem {
  name: string;
  label: string;
  type: 'text' | 'number' | 'date' | 'boolean' | 'picklist' | 'reference';
  picklist_values?: string[];
}

/**
 * GET /api/workflows/schema/objects?permission=read
 * Returns a flat list of objects the current user has read permission to.
 */
export async function getSchemaObjects(): Promise<ObjectItem[]> {
  const res = await apiFetch('/api/workflows/schema/objects?permission=read');
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch objects');
  return json.data as ObjectItem[];
}

/**
 * GET /api/workflows/schema/objects/:slug/fields?permission=read
 * Returns the fields for a specific object with name, type, label, and picklistValues.
 */
export async function getObjectFields(slug: string): Promise<FieldItem[]> {
  const res = await apiFetch(`/api/workflows/schema/objects/${encodeURIComponent(slug)}/fields?permission=read`);
  const json = await res.json();
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch fields for ' + slug);
  return json.data as FieldItem[];
}
