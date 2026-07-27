// M7 campaigns API layer — the bulk send engine's HTTP surface. apiFetch (bearer +
// 401→refresh) + parseJsonSafe/apiError are shared from lib/api; never re-add auth.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export type CampaignStatus =
  | 'draft' | 'scheduled' | 'snapshotting' | 'sending' | 'paused' | 'sent' | 'canceled';
export type SendLane = 'single' | 'batch';
export type RecipientLockMode = 'lock_on_schedule' | 'at_send';

export interface Campaign {
  id: string;
  org_id: string;
  name: string;
  content_id?: string | null;
  segment_ids: string[];
  exclude_segment_ids: string[];
  sending_domain_id?: string | null;
  topic_id?: string | null;
  status: CampaignStatus;
  send_lane: SendLane;
  scheduled_at?: string | null;
  recipient_lock_mode: RecipientLockMode;
  // M9 Part B — subject-line A/B (0 test_pct = off).
  ab_test_pct: number;
  ab_subject_b: string;
  ab_test_window_hours: number;
  ab_winner_variant: string;
  ab_decided_at?: string | null;
  created_by?: string | null;
  created_at: string;
  updated_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

export interface CampaignInput {
  name: string;
  content_id?: string | null;
  segment_ids: string[];
  exclude_segment_ids: string[];
  topic_id?: string | null;
  send_lane?: SendLane;
  scheduled_at?: string | null;
  recipient_lock_mode?: RecipientLockMode;
  ab_test_pct?: number;
  ab_subject_b?: string;
  ab_test_window_hours?: number;
}

// M9 Part B — A/B test results for the monitor's significance readout.
export interface ABVariantStat {
  variant: string;
  sent: number;
  opened: number;
  clicked: number;
}
export interface ABResults {
  enabled: boolean;
  test_pct?: number;
  subject_b?: string;
  variant_a?: ABVariantStat;
  variant_b?: ABVariantStat;
  winner?: string;            // '' until the decider runs
  decided_at?: string | null;
  projected_winner?: string;
  significant?: boolean;
  reason?: string;
}

export async function getABResults(id: string): Promise<ABResults> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}/ab`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load A/B results');
  return json.data as ABResults;
}

export interface LaunchCheck {
  key: string;
  label: string;
  ok: boolean;
  detail?: string;
}

export interface LaunchReadiness {
  ready: boolean;
  checks: LaunchCheck[];
  audience_count: number;
}

export interface CampaignProgress {
  status: CampaignStatus;
  counts: Record<string, number>;
  total: number;
}

export async function listCampaigns(): Promise<Campaign[]> {
  const res = await apiFetch('/api/marketing/campaigns');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load campaigns');
  return (json.data ?? []) as Campaign[];
}

export async function getCampaign(id: string): Promise<Campaign> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load campaign');
  return json.data as Campaign;
}

export async function createCampaign(input: CampaignInput): Promise<Campaign> {
  const res = await apiFetch('/api/marketing/campaigns', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create campaign');
  return json.data as Campaign;
}

export async function updateCampaign(id: string, input: CampaignInput): Promise<Campaign> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}`, { method: 'PUT', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to save campaign');
  return json.data as Campaign;
}

export async function deleteCampaign(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to delete campaign');
}

export async function getReadiness(id: string): Promise<LaunchReadiness> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}/readiness`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load readiness');
  return json.data as LaunchReadiness;
}

export async function launchCampaign(id: string): Promise<Campaign> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}/launch`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to launch campaign');
  return json.data as Campaign;
}

async function lifecycle(id: string, action: 'pause' | 'resume' | 'cancel'): Promise<void> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}/${action}`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, `Failed to ${action} campaign`);
}
export const pauseCampaign = (id: string) => lifecycle(id, 'pause');
export const resumeCampaign = (id: string) => lifecycle(id, 'resume');
export const cancelCampaign = (id: string) => lifecycle(id, 'cancel');

export async function getProgress(id: string): Promise<CampaignProgress> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}/progress`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load progress');
  return json.data as CampaignProgress;
}

// estimateAudience dry-runs the DISTINCT-email audience size for a set of segments —
// the composer's live count on unsaved selections.
export async function estimateAudience(segmentIds: string[], excludeSegmentIds: string[]): Promise<number> {
  const res = await apiFetch('/api/marketing/campaign-estimate', {
    method: 'POST',
    body: JSON.stringify({ segment_ids: segmentIds, exclude_segment_ids: excludeSegmentIds }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to estimate audience');
  return (json.data?.count ?? 0) as number;
}

// A campaign is still "in flight" (progress should keep refreshing) in these states.
export function isCampaignActive(status: CampaignStatus): boolean {
  return status === 'sending' || status === 'snapshotting' || status === 'paused';
}
