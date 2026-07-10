import type { Workflow, WorkflowRun, WorkflowStep, RunDetailResponse, WorkflowListResponse, TestRunResponse, ActionSpec, TriggerSpec, ConditionGroup } from './types';
// apiFetch (bearer + credentials + 401→refresh + optional timeout) and parseJsonSafe
// (the defensive body reader) are both shared from lib/api — single source of truth.
import { apiFetch, parseJsonSafe } from '../../lib/api';

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

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
  // Try twice: LLM endpoints hit transient hiccups (a cold start, a brief 5xx, a
  // dropped connection) that a quick retry clears — so the REAL AI draft wins more
  // often before any local fallback. Deterministic 4xx and timeouts are not retried
  // (retrying a slow-to-timeout call just doubles the wait).
  const MAX_ATTEMPTS = 2;
  let lastError = new Error('The AI could not draft a workflow.');
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt++) {
    let res: Response;
    try {
      res = await apiFetch('/api/workflows/ai/draft', { method: 'POST', body: JSON.stringify(body), timeoutMs: 45000 });
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') {
        throw new Error('The AI took too long to respond — please try again.');
      }
      lastError = new Error('Could not reach the AI service — check your connection and try again.');
      if (attempt < MAX_ATTEMPTS) { await sleep(700); continue; }
      throw lastError;
    }
    if (res.ok) {
      const json = await parseJsonSafe(res);
      return json.data as AIDraftResponse;
    }
    // 5xx is usually transient (gateway/timeout/cold start) — retry once. 4xx is
    // deterministic (bad request, unauthorized) — surface it now.
    if (res.status >= 500 && attempt < MAX_ATTEMPTS) {
      lastError = new Error(`The AI service is temporarily unavailable (HTTP ${res.status}).`);
      await sleep(700);
      continue;
    }
    const json = await parseJsonSafe(res);
    throw new Error(json.error?.message || json.error || `The AI could not draft a workflow (HTTP ${res.status}).`);
  }
  throw lastError;
}

/** The copilot AI-path health verdict (GET /api/workflows/ai/health). */
export interface AIHealth {
  ok: boolean;
  configured: boolean;
  model?: string;
  latency_ms: number;
  detail?: string;
}

/** Probe the copilot's AI path end-to-end (gateway + Cloudflare creds + model) via
 *  one tiny model call. The endpoint returns 200 when healthy and 503 otherwise —
 *  both carry the same `data` body, so read `.ok`/`.detail` rather than the status. */
export async function checkAiHealth(): Promise<AIHealth> {
  const res = await apiFetch('/api/workflows/ai/health', { timeoutMs: 20000 });
  const json = await parseJsonSafe(res);
  return (json.data ?? { ok: false, configured: false, latency_ms: 0, detail: 'No response from server' }) as AIHealth;
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
