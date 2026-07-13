// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { apiFetch, setAccessToken, getAccessToken } from './api';

// The one shared authenticated fetch wrapper: attaches the bearer token and, on a
// 401, transparently refreshes the session (single-flight) and retries the original
// request with the new token — or clears the token and bounces to /login if the
// session is truly gone. Every feature api layer relies on this behavior.

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
}

describe('apiFetch — 401 refresh', () => {
  beforeEach(() => {
    localStorage.clear();
    setAccessToken('old-token');
  });
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    setAccessToken(null);
  });

  it('refreshes on 401 and retries the original request with the NEW token', async () => {
    let targetCalls = 0;
    const authHeaders: (string | undefined)[] = [];
    const fetchMock = vi.fn((url: string, init: RequestInit) => {
      if (url.includes('/api/auth/refresh')) {
        return Promise.resolve(jsonResponse({ data: { access_token: 'new-token' } }));
      }
      targetCalls++;
      authHeaders.push((init.headers as Record<string, string>).Authorization);
      return Promise.resolve(targetCalls === 1 ? jsonResponse({ error: 'unauthorized' }, 401) : jsonResponse({ data: { ok: true } }));
    });
    vi.stubGlobal('fetch', fetchMock);

    const res = await apiFetch('/api/thing');

    expect(res.status).toBe(200);
    // original (401) → refresh → retry
    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/api/auth/refresh'))).toBe(true);
    // first attempt carried the old token; the retry carried the refreshed one
    expect(authHeaders[0]).toBe('Bearer old-token');
    expect(authHeaders[1]).toBe('Bearer new-token');
    expect(getAccessToken()).toBe('new-token');
  });

  it('does not refresh or retry when the first attempt succeeds', async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse({ data: { ok: true } })));
    vi.stubGlobal('fetch', fetchMock);

    const res = await apiFetch('/api/thing');

    expect(res.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(getAccessToken()).toBe('old-token');
  });

  it('clears the token and redirects to /login with expired + return-to params when the refresh fails', async () => {
    // U2: the bounce carries ?expired=1 (LoginPage shows a notice) and the
    // current path as ?next= so a re-login lands back where the session died.
    const loc = { href: '', pathname: '/deals/abc', search: '?tab=1' } as Location;
    const original = window.location;
    Object.defineProperty(window, 'location', { configurable: true, value: loc });
    try {
      const fetchMock = vi.fn((url: string) =>
        Promise.resolve(
          url.includes('/api/auth/refresh') ? jsonResponse({ error: 'expired' }, 401) : jsonResponse({ error: 'unauthorized' }, 401),
        ),
      );
      vi.stubGlobal('fetch', fetchMock);

      await apiFetch('/api/thing');

      expect(getAccessToken()).toBeNull();
      expect(loc.href).toBe('/login?expired=1&next=%2Fdeals%2Fabc%3Ftab%3D1');
      // original (401) + refresh (failed) — no retry
      expect(fetchMock).toHaveBeenCalledTimes(2);
    } finally {
      Object.defineProperty(window, 'location', { configurable: true, value: original });
    }
  });

  it('shares a single refresh across concurrent 401s (single-flight)', async () => {
    let refreshCalls = 0;
    let resolveRefresh!: (r: Response) => void;
    const refreshPromise = new Promise<Response>((r) => {
      resolveRefresh = r;
    });
    const fetchMock = vi.fn((url: string) => {
      if (url.includes('/api/auth/refresh')) {
        refreshCalls++;
        return refreshPromise; // stays pending until we resolve it below
      }
      // 401 until the shared refresh has installed the new token
      return Promise.resolve(
        getAccessToken() === 'new-token' ? jsonResponse({ data: { ok: true } }) : jsonResponse({ error: 'unauthorized' }, 401),
      );
    });
    vi.stubGlobal('fetch', fetchMock);

    const both = Promise.all([apiFetch('/api/a'), apiFetch('/api/b')]);
    // Let both requests hit their 401 and await the SAME in-flight refresh.
    await new Promise((r) => setTimeout(r, 0));
    resolveRefresh(jsonResponse({ data: { access_token: 'new-token' } }));
    const [r1, r2] = await both;

    expect(refreshCalls).toBe(1);
    expect(r1.status).toBe(200);
    expect(r2.status).toBe(200);
    expect(getAccessToken()).toBe('new-token');
  });
});
