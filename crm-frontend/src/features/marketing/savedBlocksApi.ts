// Saved-block library API (reusable builder blocks). A saved block stores one
// wire-shape Block. A plain row is inserted as a deep COPY; a `synced` row is
// inserted as a LINKED instance (Block.ref = the row id) whose content the
// library can update everywhere at once.

import { apiFetch, parseJsonSafe, apiError, asArray } from '../../lib/api';
import type { Block } from './composer/blocks';

export interface SavedBlockRow {
  id: string;
  name: string;
  block: Block;
  /** Synced rows insert as linked instances and can be updated everywhere. */
  synced: boolean;
  created_at: string;
}

/** Where a synced block is used — the "is it safe to change this?" answer shown
 *  before an update propagates. */
export interface SavedBlockUsage {
  emails: { id: string; name: string; instances: number }[];
  email_count: number;
  instance_count: number;
}

export async function listSavedBlocks(): Promise<SavedBlockRow[]> {
  const res = await apiFetch('/api/marketing/saved-blocks');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not load saved blocks');
  return asArray<SavedBlockRow>(json.data, 'saved blocks');
}

export async function createSavedBlock(name: string, block: Block, synced = false): Promise<SavedBlockRow> {
  const res = await apiFetch('/api/marketing/saved-blocks', {
    method: 'POST',
    body: JSON.stringify({ name, block, synced }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not save the block');
  return json.data as SavedBlockRow;
}

export async function renameSavedBlock(id: string, name: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/saved-blocks/${id}`, {
    method: 'PUT',
    body: JSON.stringify({ name }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not rename the saved block');
}

export async function removeSavedBlock(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/saved-blocks/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not delete the saved block');
}

/** GET a synced block's usage, so the count can be shown BEFORE propagating. */
export async function savedBlockUsage(id: string): Promise<SavedBlockUsage> {
  const res = await apiFetch(`/api/marketing/saved-blocks/${id}/usage`);
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not check where this block is used');
  return (json.data ?? { emails: [], email_count: 0, instance_count: 0 }) as SavedBlockUsage;
}

/** PUT new content for a synced block. The server rewrites AND RECOMPILES every
 *  email using it — body_html_compiled is the send source, so a rewrite without
 *  a recompile would leave recipients receiving the old block. Emails the new
 *  content would make invalid are skipped and named. */
export async function updateSavedBlockContent(
  id: string,
  block: Block,
): Promise<{ updated: string[]; skipped: string[] }> {
  const res = await apiFetch(`/api/marketing/saved-blocks/${id}/content`, {
    method: 'PUT',
    body: JSON.stringify({ block }),
  });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not update the synced block');
  return (json.data ?? { updated: [], skipped: [] }) as { updated: string[]; skipped: string[] };
}
