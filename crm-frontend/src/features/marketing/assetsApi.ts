// Image library API (builder image picker). Uploads are multipart — apiFetch
// deliberately skips the JSON Content-Type for FormData bodies.

import { apiFetch, parseJsonSafe, apiError, asArray } from '../../lib/api';

export interface MarketingAsset {
  id: string;
  filename: string;
  content_type: string;
  size_bytes: number;
  created_at: string;
  url: string; // public serve URL — what goes into block.src (recipients fetch it)
}

/** displayImageSrc maps a stored asset URL to a SAME-ORIGIN path for in-app
 *  display. The stored URL is minted against the deployment's public base (what
 *  recipients' mail clients must fetch) — which the browser may not reach in
 *  dev. The path form rides the vite proxy locally and the first-party /api
 *  proxy in prod. Non-asset URLs pass through untouched. */
export function displayImageSrc(url?: string): string | undefined {
  if (!url) return url;
  const i = url.indexOf('/api/marketing/asset/');
  return i >= 0 ? url.slice(i) : url;
}

export async function listAssets(): Promise<MarketingAsset[]> {
  const res = await apiFetch('/api/marketing/assets');
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not load the image library');
  return asArray<MarketingAsset>(json.data, 'marketing assets');
}

export async function uploadAsset(file: File): Promise<MarketingAsset> {
  const form = new FormData();
  form.append('file', file);
  const res = await apiFetch('/api/marketing/assets', { method: 'POST', body: form, timeoutMs: 30000 });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Upload failed');
  return json.data as MarketingAsset;
}

export async function removeAsset(id: string): Promise<void> {
  const res = await apiFetch(`/api/marketing/assets/${id}`, { method: 'DELETE' });
  const json = await parseJsonSafe(res);
  if (!res.ok) throw apiError(res, json, 'Could not delete the image');
}
