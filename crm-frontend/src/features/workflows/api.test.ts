import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { runNowWorkflow } from './api';

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

/** Build a minimal fetch Response-like object for mocking. Includes text() because
 *  the api layer now reads the body as text and parses it defensively (so an HTML
 *  error page can't crash callers with a raw JSON.parse error). */
function mockResponse(body: unknown, ok: boolean): Response {
  const text = JSON.stringify(body);
  return {
    ok,
    status: ok ? 200 : 400,
    json: async () => body,
    text: async () => text,
  } as unknown as Response;
}

describe('runNowWorkflow', () => {
  beforeEach(() => {
    // Each test installs its own fetch mock; ensure a clean slate.
    vi.restoreAllMocks();
    localStorage.clear();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('issues POST /api/workflows/:id/run with the selected contact_id in the body', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(mockResponse({ data: { id: 'run-1', status: 'pending' } }, true));
    vi.stubGlobal('fetch', fetchMock);

    await runNowWorkflow('wf-123', { contact_id: 'contact-abc' });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, options] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_URL}/api/workflows/wf-123/run`);
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ contact_id: 'contact-abc' });
  });

  it('issues POST /api/workflows/:id/run with the selected deal_id in the body', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValue(mockResponse({ data: { id: 'run-2', status: 'pending' } }, true));
    vi.stubGlobal('fetch', fetchMock);

    await runNowWorkflow('wf-456', { deal_id: 'deal-xyz' });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, options] = fetchMock.mock.calls[0];
    expect(url).toBe(`${API_URL}/api/workflows/wf-456/run`);
    expect(options.method).toBe('POST');
    expect(JSON.parse(options.body)).toEqual({ deal_id: 'deal-xyz' });
  });

  it('returns the RunNowResult data on a successful (ok) response', async () => {
    const expected = { id: 'run-789', status: 'pending' };
    const fetchMock = vi.fn().mockResolvedValue(mockResponse({ data: expected }, true));
    vi.stubGlobal('fetch', fetchMock);

    const result = await runNowWorkflow('wf-789', { contact_id: 'contact-1' });

    expect(result).toEqual(expected);
  });

  it('throws an Error with the server-provided error message on a non-ok response', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      mockResponse({ error: { message: 'Workflow not found' } }, false),
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(runNowWorkflow('wf-missing', { contact_id: 'contact-1' })).rejects.toThrow(
      'Workflow not found',
    );
  });

  it('throws a fallback Error when a non-ok response has no error message', async () => {
    const fetchMock = vi.fn().mockResolvedValue(mockResponse({}, false));
    vi.stubGlobal('fetch', fetchMock);

    await expect(runNowWorkflow('wf-err', { deal_id: 'deal-1' })).rejects.toThrow(
      'Failed to run workflow',
    );
  });
});
