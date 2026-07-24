// Marketing campaign-content API (M6). apiFetch/parseJsonSafe/apiError shared from lib/api.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';
import type { BlockDocument } from './composer/blocks';

export interface CampaignContent {
  id: string;
  org_id: string;
  name: string;
  subject: string;
  preheader: string;
  body_json: BlockDocument;
  body_html_compiled: string;
  plain_text: string;
  merge_scope: string[];
  compiled_size_bytes: number;
  compiled_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface ContentInput {
  name: string;
  subject: string;
  preheader: string;
  body_json: BlockDocument;
  merge_scope: string[];
}

export interface PreviewResult {
  html?: string;
  plain_text?: string;
  size_bytes?: number;
  too_large?: boolean;
  validation_errors?: string[];
  warnings?: string[];
  compile_error?: string;
}

/** saveError is an Error carrying the backend's validation_errors for display. */
export interface SaveError extends Error {
  validationErrors?: string[];
}

export async function listContent(): Promise<CampaignContent[]> {
  const res = await apiFetch('/api/marketing/content');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load campaign content');
  return (json.data ?? []) as CampaignContent[];
}

export async function getContent(id: string): Promise<CampaignContent> {
  const res = await apiFetch(`/api/marketing/content/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load content');
  return json.data as CampaignContent;
}

function withValidation(res: Response, json: Record<string, unknown>, fallback: string): SaveError {
  const e = apiError(res, json, fallback) as SaveError;
  if (Array.isArray(json.validation_errors)) e.validationErrors = json.validation_errors as string[];
  return e;
}

export async function createContent(input: ContentInput): Promise<CampaignContent> {
  const res = await apiFetch('/api/marketing/content', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw withValidation(res, json, 'Failed to create content');
  return json.data as CampaignContent;
}

export async function updateContent(id: string, input: ContentInput): Promise<CampaignContent> {
  const res = await apiFetch(`/api/marketing/content/${id}`, { method: 'PUT', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw withValidation(res, json, 'Failed to save content');
  return json.data as CampaignContent;
}

export async function removeContent(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/content/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to delete content');
}

export async function previewContent(input: Omit<ContentInput, 'name'>): Promise<PreviewResult> {
  const res = await apiFetch('/api/marketing/content/preview', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to render preview');
  return (json.data ?? {}) as PreviewResult;
}

export async function testSendContent(id: string): Promise<string> {
  const res = await apiFetch(`/api/marketing/content/${id}/test-send`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to send test');
  return (json.data?.sent_to as string) ?? '';
}
