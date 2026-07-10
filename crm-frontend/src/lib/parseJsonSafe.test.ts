import { describe, it, expect } from 'vitest';
import { parseJsonSafe } from './api';

// parseJsonSafe is the shared guard that stopped the "Unexpected token '<', <!DOCTYPE
// … not valid JSON" class of crash across every feature api layer. An HTML error page
// from a proxy/gateway/auth-wall must become a clear message, never a raw parse error.

describe('parseJsonSafe', () => {
  it('parses a valid JSON body', async () => {
    const res = new Response(JSON.stringify({ data: { ok: true } }), { status: 200 });
    expect(await parseJsonSafe(res)).toEqual({ data: { ok: true } });
  });

  it('returns {} for an empty body', async () => {
    const res = new Response('', { status: 200 });
    expect(await parseJsonSafe(res)).toEqual({});
  });

  it('turns an HTML 404 page into a clear message, never a raw JSON parse error', async () => {
    await expect(parseJsonSafe(new Response('<!DOCTYPE html><html>404</html>', { status: 404 }))).rejects.toThrow(
      /HTTP 404|not found/i,
    );
    await expect(parseJsonSafe(new Response('<!DOCTYPE html>', { status: 404 }))).rejects.not.toThrow(
      /Unexpected token/i,
    );
  });

  it('maps 5xx / gateway pages to temporarily unavailable', async () => {
    await expect(parseJsonSafe(new Response('<html>502 Bad Gateway</html>', { status: 502 }))).rejects.toThrow(
      /unavailable|HTTP 502/i,
    );
  });

  it('maps 401/403 HTML (auth wall) to a session hint', async () => {
    await expect(parseJsonSafe(new Response('<html>login</html>', { status: 401 }))).rejects.toThrow(
      /sign in again|session/i,
    );
  });
});
