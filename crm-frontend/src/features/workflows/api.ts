import type { Workflow, WorkflowRun, WorkflowStep, RunDetailResponse, WorkflowListResponse, TestRunResponse, ActionSpec, TriggerSpec, ConditionGroup } from './types';
import { getAccessToken } from '../../lib/api';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

async function apiFetch(path: string, options: RequestInit & { timeoutMs?: number } = {}): Promise<Response> {
  const { timeoutMs, ...init } = options;
  const token = getAccessToken();
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init.headers as Record<string, string> || {}),
  };
  if (token) headers['Authorization'] = `Bearer ${token}`;
  // Optional client-side timeout so a hung request (e.g. a slow AI call behind a
  // proxy) fails fast instead of spinning forever.
  const controller = timeoutMs ? new AbortController() : null;
  const timer = controller ? setTimeout(() => controller.abort(), timeoutMs) : null;
  try {
    return await fetch(`${API_URL}${path}`, {
      ...init,
      headers,
      credentials: 'include',
      signal: controller?.signal ?? init.signal,
    });
  } finally {
    if (timer) clearTimeout(timer);
  }
}

/**
 * Read a JSON body defensively. A proxy, gateway timeout, auth wall, or 404 at the
 * edge can return an HTML page ("<!DOCTYPE html>…") instead of JSON; calling
 * res.json() on that throws a cryptic `Unexpected token '<'`. This reads the body
 * once and, on a non-JSON payload, throws a clear message keyed to the HTTP status
 * — the raw parse error never reaches the UI.
 */
async function parseJsonSafe(res: Response): Promise<any> {
  const text = await res.text();
  try {
    return text ? JSON.parse(text) : {};
  } catch {
    const hint =
      res.status === 401 || res.status === 403
        ? 'your session may have expired — please sign in again'
        : res.status === 404
          ? 'the service endpoint was not found'
          : res.status === 0 || res.status >= 500
            ? 'the service is temporarily unavailable — please try again'
            : 'the server returned an unexpected response';
    throw new Error(`Request failed (HTTP ${res.status || '000'}) — ${hint}.`);
  }
}

// --- Workflow CRUD ---

export async function getWorkflows(params?: { active?: boolean; q?: string; page?: number; size?: number }): Promise<WorkflowListResponse> {
  const qs = new URLSearchParams();
  if (params?.active !== undefined) qs.set('active', String(params.active));
  if (params?.q) qs.set('q', params.q);
  if (params?.page) qs.set('page', String(params.page));
  if (params?.size) qs.set('size', String(params.size));
  const res = await apiFetch(`/api/workflows?${qs.toString()}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch workflows');
  return json.data as WorkflowListResponse;
}

export async function getWorkflow(id: string): Promise<Workflow> {
  const res = await apiFetch(`/api/workflows/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch workflow');
  return json.data as Workflow;
}

export async function createWorkflow(data: {
  name: string;
  description?: string;
  trigger: TriggerSpec;
  conditions?: ConditionGroup | null;
  /** Steps are canonical (A1); the server derives the deprecated flat actions. */
  steps?: WorkflowStep[];
  /** Deprecated: only for legacy actions-only payloads; ignored when steps present. */
  actions?: ActionSpec[];
}): Promise<Workflow> {
  const res = await apiFetch('/api/workflows', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  const json = await parseJsonSafe(res);
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
  /** Steps are canonical (A1); the server derives the deprecated flat actions. */
  steps?: WorkflowStep[];
  /** Deprecated: only for legacy actions-only payloads; ignored when steps present. */
  actions?: ActionSpec[];
}): Promise<Workflow> {
  const res = await apiFetch(`/api/workflows/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to toggle workflow');
  return json.data as Workflow;
}

/** Dry-run (A3.5): a side-effect-free steps-tree walk. Prefer a sample entity
 *  (contact_id/deal_id) — the server resolves it into a realistic eval context;
 *  `context` is a raw override for advanced/test callers. */
export async function testRunWorkflow(
  id: string,
  body: { contact_id?: string; deal_id?: string; context?: Record<string, unknown> },
): Promise<TestRunResponse> {
  const res = await apiFetch(`/api/workflows/${id}/test-run`, {
    method: 'POST',
    body: JSON.stringify(body),
  });
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to run workflow');
  return json.data as RunNowResult;
}

// --- Runs ---

export async function getWorkflowRuns(workflowId: string, page = 1, size = 20): Promise<{
  runs: WorkflowRun[];
  total: number;
}> {
  const res = await apiFetch(`/api/workflows/${workflowId}/runs?page=${page}&size=${size}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch runs');
  return json.data as { runs: WorkflowRun[]; total: number };
}

export async function getRunDetail(runId: string): Promise<RunDetailResponse> {
  const res = await apiFetch(`/api/workflows/runs/${runId}`);
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to regenerate webhook secret');
  return json.data as WebhookSecretInfo;
}

// --- Email templates (A5) ---

/** A reusable, org-scoped email template. body_json is the TipTap document kept
 *  for lossless re-editing; body_html is the canonical send source. */
export interface EmailTemplate {
  id: string;
  org_id: string;
  name: string;
  subject: string;
  body_html: string;
  /** TipTap ProseMirror doc; opaque to callers other than the editor. */
  body_json?: unknown;
  /** Optional merge scope (e.g. "contact" / "deal"); "" = unscoped. */
  object_slug: string;
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

export interface EmailTemplateListResponse {
  templates: EmailTemplate[];
  total: number;
}

/** Create/update payload. Omitted fields are left unchanged on update. */
export interface SaveEmailTemplateInput {
  name?: string;
  subject?: string;
  body_html?: string;
  body_json?: unknown;
  object_slug?: string;
}

export async function getEmailTemplates(): Promise<EmailTemplateListResponse> {
  const res = await apiFetch('/api/workflows/email-templates');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch email templates');
  return json.data as EmailTemplateListResponse;
}

export async function getEmailTemplate(id: string): Promise<EmailTemplate> {
  const res = await apiFetch(`/api/workflows/email-templates/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch email template');
  return json.data as EmailTemplate;
}

export async function createEmailTemplate(data: SaveEmailTemplateInput): Promise<EmailTemplate> {
  const res = await apiFetch('/api/workflows/email-templates', { method: 'POST', body: JSON.stringify(data) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to create email template');
  return json.data as EmailTemplate;
}

export async function updateEmailTemplate(id: string, data: SaveEmailTemplateInput): Promise<EmailTemplate> {
  const res = await apiFetch(`/api/workflows/email-templates/${id}`, { method: 'PUT', body: JSON.stringify(data) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to update email template');
  return json.data as EmailTemplate;
}

export async function deleteEmailTemplate(id: string): Promise<void> {
  const res = await apiFetch(`/api/workflows/email-templates/${id}`, { method: 'DELETE' });
  if (!res.ok) {
    const json = await res.json().catch(() => ({}));
    throw new Error(json.error?.message || 'Failed to delete email template');
  }
}

/** POST /:id/test-send — renders the template against a sample record and emails
 *  it to the caller. Returns the address it was sent to. */
export async function testSendEmailTemplate(id: string): Promise<{ status: string; to: string }> {
  const res = await apiFetch(`/api/workflows/email-templates/${id}/test-send`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to send test email');
  return json.data as { status: string; to: string };
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch schema');
  return json.data as WorkflowSchema;
}

// --- AI copilot (A7): NL → workflow draft ---

/** A drafted workflow the copilot returns. Steps are id-normalized server-side, so
 *  it applies directly to the builder. Structurally compatible with the store's
 *  WorkflowDraftInput. */
export interface AIDraft {
  name: string;
  description?: string;
  trigger: TriggerSpec;
  conditions?: ConditionGroup | null;
  steps: WorkflowStep[];
}

export interface AIDraftValidation {
  valid: boolean;
  errors?: { field: string; message: string }[];
  warnings?: string[];
}

export interface AIDraftResponse {
  draft: AIDraft;
  validation: AIDraftValidation;
}

/** The workflow being edited, sent so the copilot edits it instead of drafting from
 *  scratch (A7.4). Omit for a from-scratch draft. */
export interface WorkflowEditContext {
  name?: string;
  trigger: TriggerSpec | null;
  conditions?: ConditionGroup | null;
  steps: WorkflowStep[];
}

/** POST /api/workflows/ai/draft — turn a natural-language prompt into a draft. The
 *  server never saves; the caller applies the draft through the same validation as
 *  a manual edit. When `current` is supplied the copilot applies the prompt as an
 *  edit against it (preserving unchanged parts) rather than drafting anew. */
export async function draftWorkflow(prompt: string, current?: WorkflowEditContext | null): Promise<AIDraftResponse> {
  const body = current ? { prompt, current_workflow: current } : { prompt };
  let res: Response;
  try {
    // Bounded timeout: an LLM call behind a slow proxy shouldn't hang the UI — on
    // timeout we reject so the copilot's local fallback can take over.
    res = await apiFetch('/api/workflows/ai/draft', { method: 'POST', body: JSON.stringify(body), timeoutMs: 45000 });
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') {
      throw new Error('The AI took too long to respond — please try again.');
    }
    throw new Error('Could not reach the AI service — check your connection and try again.');
  }
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || json.error || 'The AI could not draft a workflow.');
  return json.data as AIDraftResponse;
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
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch objects');
  return json.data as ObjectItem[];
}

/**
 * GET /api/workflows/schema/objects/:slug/fields?permission=read
 * Returns the fields for a specific object with name, type, label, and picklistValues.
 */
export async function getObjectFields(slug: string): Promise<FieldItem[]> {
  const res = await apiFetch(`/api/workflows/schema/objects/${encodeURIComponent(slug)}/fields?permission=read`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw new Error(json.error?.message || 'Failed to fetch fields for ' + slug);
  return json.data as FieldItem[];
}
