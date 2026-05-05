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

export async function getWorkflows(params?: { active?: boolean; page?: number; size?: number }): Promise<WorkflowListResponse> {
  const qs = new URLSearchParams();
  if (params?.active !== undefined) qs.set('active', String(params.active));
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
