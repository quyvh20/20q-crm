// Marketing sender-profile + topics API layer (M3). Shares apiFetch/parseJsonSafe/
// apiError from lib/api — never re-add auth headers.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export interface SenderProfile {
  from_name: string;
  reply_to: string;
  physical_postal_address: string;
  marketing_paused: boolean;
  sendable: boolean; // derived: profile complete AND not paused
  reason: string; // "" | no_profile | no_postal_address | no_from_name | marketing_paused
}

export interface MarketingTopic {
  id: string;
  org_id: string;
  name: string;
  description: string;
  opt_in_default: boolean;
  created_at: string;
  updated_at: string;
}

export interface SenderProfileInput {
  from_name: string;
  reply_to: string;
  physical_postal_address: string;
}

export async function getSenderProfile(): Promise<SenderProfile> {
  const res = await apiFetch('/api/marketing/sender-profile');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load the sender profile');
  return json.data as SenderProfile;
}

export async function saveSenderProfile(input: SenderProfileInput): Promise<SenderProfile> {
  const res = await apiFetch('/api/marketing/sender-profile', { method: 'PUT', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to save the sender profile');
  return json.data as SenderProfile;
}

export async function resumeMarketing(): Promise<SenderProfile> {
  const res = await apiFetch('/api/marketing/sender-profile/resume', { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to resume marketing');
  return json.data as SenderProfile;
}

export async function listTopics(): Promise<MarketingTopic[]> {
  const res = await apiFetch('/api/marketing/topics');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load topics');
  return (json.data ?? []) as MarketingTopic[];
}

export async function createTopic(input: { name: string; description: string; opt_in_default: boolean }): Promise<MarketingTopic> {
  const res = await apiFetch('/api/marketing/topics', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create the topic');
  return json.data as MarketingTopic;
}

export async function updateTopic(id: string, input: { name: string; description: string }): Promise<MarketingTopic> {
  const res = await apiFetch(`/api/marketing/topics/${id}`, { method: 'PUT', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to update the topic');
  return json.data as MarketingTopic;
}

export async function deleteTopic(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/topics/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to delete the topic');
}

// senderReasonLabel maps the profile's not-sendable reason to friendly copy.
export function senderReasonLabel(reason: string): string {
  switch (reason) {
    case 'no_profile': return 'Add a sender name and physical postal address to enable marketing sends.';
    case 'no_from_name': return 'Add a sender (from) name.';
    case 'no_postal_address': return 'Add a physical postal address (required by anti-spam law in every marketing email).';
    case 'marketing_paused': return 'Marketing is paused for this workspace — resume it below once the underlying issue is resolved.';
    default: return '';
  }
}
