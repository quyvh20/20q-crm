// M8 drip-sequences API layer. A "sequence" is an automation workflow (delay +
// channel=marketing send steps); this surface wires an M5 segment into one and the
// backend feeder drains it. apiFetch (bearer + 401→refresh) + parseJsonSafe/apiError are
// shared from lib/api; never re-add auth.
import { apiFetch, parseJsonSafe, apiError } from '../../lib/api';

export type SequenceStatus = 'active' | 'completed' | 'blocked' | 'canceled';

export interface SequenceEnrollment {
  id: string;
  sequence_workflow_id: string;
  sequence_name: string;
  segment_id: string;
  segment_name: string;
  status: SequenceStatus;
  enrolled_count: number;
  last_error?: string;
  created_at: string;
}

export interface SequenceInput {
  sequence_workflow_id: string;
  segment_id: string;
}

export interface SequenceProgress {
  total: number;
  active: number;
  completed: number;
  failed: number;
  skipped: number;
  by_status: Record<string, number>;
}

export async function listSequences(): Promise<SequenceEnrollment[]> {
  const res = await apiFetch('/api/marketing/sequences');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load sequences');
  return (json.data ?? []) as SequenceEnrollment[];
}

export async function getSequence(id: string): Promise<SequenceEnrollment> {
  const res = await apiFetch(`/api/marketing/sequences/${id}`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load sequence');
  return json.data as SequenceEnrollment;
}

export async function createSequence(input: SequenceInput): Promise<SequenceEnrollment> {
  const res = await apiFetch('/api/marketing/sequences', { method: 'POST', body: JSON.stringify(input) });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to create sequence enrollment');
  return json.data as SequenceEnrollment;
}

export async function cancelSequence(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/sequences/${id}/cancel`, { method: 'POST' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to cancel sequence');
}

export async function getSequenceProgress(id: string): Promise<SequenceProgress> {
  const res = await apiFetch(`/api/marketing/sequences/${id}/progress`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Failed to load sequence progress');
  return json.data as SequenceProgress;
}

// A sequence enrollment is still feeding (progress should keep refreshing) while active.
export function isSequenceActive(status: SequenceStatus): boolean {
  return status === 'active';
}

export const SEQUENCE_STATUS_LABEL: Record<SequenceStatus, string> = {
  active: 'Active',
  completed: 'Completed',
  blocked: 'Blocked',
  canceled: 'Canceled',
};

export type BadgeVariant = 'success' | 'secondary' | 'warning' | 'destructive' | 'outline';

export function sequenceStatusVariant(status: SequenceStatus): BadgeVariant {
  switch (status) {
    case 'active': return 'success';
    case 'completed': return 'secondary';
    case 'blocked': return 'warning';
    case 'canceled': return 'destructive';
    default: return 'outline';
  }
}
