// M9 analytics API layer — per-campaign engagement + org deliverability, derived at
// query time from the M4 event ledger. apiFetch (bearer + 401→refresh) +
// parseJsonSafe/apiError shared from lib/api; never re-add auth.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export interface CampaignAnalytics {
  sent: number;
  delivered: number;
  opened: number;         // unique recipients who opened
  opened_total: number;   // total opens (incl. repeats + machine)
  opened_engaged: number; // unique, excluding likely-automated (MPP) opens
  clicked: number;        // unique recipients who clicked (headline metric)
  clicked_total: number;
  bounced: number;
  complained: number;
}

export interface DeliverabilityRates {
  delivered: number;
  sent: number;
  complaints: number;
  hard_bounces: number;
  complaint_rate: number; // complaints / delivered
  bounce_rate: number;    // hard bounces / sent
}

export interface DeliverabilityThresholds {
  warn_rate: number;
  pause_rate: number;
  warn_min_volume: number;
  pause_min_volume: number;
  window_days: number;
}

export interface Deliverability {
  rates: DeliverabilityRates;
  thresholds: DeliverabilityThresholds;
}

export async function getCampaignAnalytics(id: string): Promise<CampaignAnalytics> {
  const res = await apiFetch(`/api/marketing/campaigns/${id}/analytics`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load campaign analytics');
  return json.data as CampaignAnalytics;
}

export async function getDeliverability(): Promise<Deliverability> {
  const res = await apiFetch('/api/marketing/deliverability');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load deliverability');
  return json.data as Deliverability;
}
