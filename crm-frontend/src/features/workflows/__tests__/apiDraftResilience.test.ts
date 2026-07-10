import { describe, it, expect, afterEach, vi } from 'vitest';
import { draftWorkflow } from '../api';

// The bug that started this: POST /ai/draft came back as an HTML page (a 404/redirect/
// timeout page from a proxy) and the old code called res.json() on it, surfacing the
// cryptic "Unexpected token '<', <!DOCTYPE ...". These lock in the resilient behavior.

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
  vi.restoreAllMocks();
});

describe('draftWorkflow resilience', () => {
  it('turns an HTML (non-JSON) response into a clear message, never a JSON parse error', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response('<!DOCTYPE html><html><body>404 Not Found</body></html>', {
        status: 404,
        headers: { 'content-type': 'text/html' },
      }),
    ) as unknown as typeof fetch;

    await expect(draftWorkflow('do X')).rejects.toThrow(/HTTP 404|not found/i);
    await expect(draftWorkflow('do X')).rejects.not.toThrow(/Unexpected token/i);
  });

  it('reports a gateway/5xx HTML error as temporarily unavailable', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response('<!DOCTYPE html><html>502 Bad Gateway</html>', { status: 502 }),
    ) as unknown as typeof fetch;
    await expect(draftWorkflow('do X')).rejects.toThrow(/unavailable|HTTP 502/i);
  });

  it('reports a network failure clearly (not a raw TypeError)', async () => {
    globalThis.fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch')) as unknown as typeof fetch;
    await expect(draftWorkflow('do X')).rejects.toThrow(/could not reach|connection/i);
  });

  it('still returns the draft on a valid JSON success response', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ data: { draft: { name: 'WF' }, validation: { valid: true } } }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    ) as unknown as typeof fetch;
    const res = await draftWorkflow('do X');
    expect(res.draft.name).toBe('WF');
  });

  it('surfaces a structured JSON error body message on a non-OK response', async () => {
    globalThis.fetch = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { message: 'prompt is required' } }), {
        status: 400,
        headers: { 'content-type': 'application/json' },
      }),
    ) as unknown as typeof fetch;
    await expect(draftWorkflow('do X')).rejects.toThrow(/prompt is required/i);
  });
});
