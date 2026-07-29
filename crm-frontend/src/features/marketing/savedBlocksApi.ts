// Saved-block library API (reusable builder blocks). A saved block stores one
// wire-shape Block — inserts are deep copies with fresh ids, never references.

import { apiFetch, parseJsonSafe, apiError, asArray } from '../../lib/api';
import type { Block } from './composer/blocks';

export interface SavedBlockRow {
  id: string;
  name: string;
  block: Block;
  created_at: string;
}

export async function listSavedBlocks(): Promise<SavedBlockRow[]> {
  const res = await apiFetch('/api/marketing/saved-blocks');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not load saved blocks');
  return asArray<SavedBlockRow>(json.data, 'saved blocks');
}

export async function createSavedBlock(name: string, block: Block): Promise<SavedBlockRow> {
  const res = await apiFetch('/api/marketing/saved-blocks', {
    method: 'POST',
    body: JSON.stringify({ name, block }),
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
