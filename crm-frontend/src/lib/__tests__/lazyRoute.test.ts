import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { loadChunk } from '../lazyRoute';

// loadChunk is the recovery policy for a lazy route whose chunk 404s because the
// app was redeployed under an open tab. Its whole value is in the failure paths,
// which never run in a happy-path render test — so they are pinned here.

const RELOAD_MARKER = 'chunk-load:last-reload';

let reload: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.useFakeTimers();
  window.sessionStorage.clear();
  reload = vi.fn();
  // jsdom's location.reload is a non-configurable navigation stub that throws
  // "Not implemented"; replace the whole location object with a spyable one.
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: { ...window.location, reload },
  });
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

/** Advance past the retry delay and let the promise chain settle. */
async function settle() {
  await vi.advanceTimersByTimeAsync(1000);
}

describe('loadChunk', () => {
  it('returns the module without retrying when the import succeeds', async () => {
    const load = vi.fn().mockResolvedValue({ default: 'page' });

    const result = await loadChunk(load);

    expect(result).toEqual({ default: 'page' });
    expect(load).toHaveBeenCalledTimes(1);
    expect(reload).not.toHaveBeenCalled();
  });

  it('retries once and succeeds on a transient failure, without reloading', async () => {
    // The flaky-network case: a reload here would be a needlessly heavy hammer,
    // and would throw away any unsaved state on the page the user is leaving.
    const load = vi
      .fn()
      .mockRejectedValueOnce(new Error('network error'))
      .mockResolvedValueOnce({ default: 'page' });

    const promise = loadChunk(load);
    await settle();

    await expect(promise).resolves.toEqual({ default: 'page' });
    expect(load).toHaveBeenCalledTimes(2);
    expect(reload).not.toHaveBeenCalled();
  });

  it('reloads the page when both attempts fail — the stale-deploy recovery', async () => {
    const load = vi.fn().mockRejectedValue(new Error('Failed to fetch dynamically imported module'));

    void loadChunk(load);
    await settle();

    expect(load).toHaveBeenCalledTimes(2);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('never settles while a reload is in flight', async () => {
    // If this promise resolved or rejected it would race the navigation and
    // paint either a broken page or an error screen the user cannot act on.
    // Suspense must simply keep showing its fallback until the document goes.
    const load = vi.fn().mockRejectedValue(new Error('boom'));
    const onSettled = vi.fn();

    void loadChunk(load).then(onSettled, onSettled);
    await settle();

    expect(reload).toHaveBeenCalledTimes(1);
    expect(onSettled).not.toHaveBeenCalled();
  });

  it('rethrows instead of reloading again inside the cooldown window', async () => {
    // The loop guard. A second failure right after a reload means the reload did
    // not fix it, so reloading again would spin the tab forever. The error must
    // reach the error boundary, which offers a manual retry.
    window.sessionStorage.setItem(RELOAD_MARKER, String(Date.now()));
    const load = vi.fn().mockRejectedValue(new Error('still broken'));

    const promise = loadChunk(load);
    const assertion = expect(promise).rejects.toThrow('still broken');
    await settle();
    await assertion;

    expect(reload).not.toHaveBeenCalled();
  });

  it('reloads again once the cooldown has expired', async () => {
    // A DIFFERENT deploy hours later is a new incident, not the same loop.
    window.sessionStorage.setItem(RELOAD_MARKER, String(Date.now() - 60_000));
    const load = vi.fn().mockRejectedValue(new Error('new deploy'));

    void loadChunk(load);
    await settle();

    expect(reload).toHaveBeenCalledTimes(1);
  });

  it('rethrows rather than reloading when sessionStorage is unavailable', async () => {
    // Safari in private mode throws on sessionStorage access. With no loop guard
    // available the safe direction is the error screen, never an unguarded reload.
    // Spy on Storage.prototype, not on the sessionStorage instance: jsdom's
    // storage object does not accept a property redefinition, so an instance
    // spy silently no-ops and the test would pass for the wrong reason.
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError');
    });
    const load = vi.fn().mockRejectedValue(new Error('boom'));

    const promise = loadChunk(load);
    const assertion = expect(promise).rejects.toThrow('boom');
    await settle();
    await assertion;

    expect(reload).not.toHaveBeenCalled();
  });
});
