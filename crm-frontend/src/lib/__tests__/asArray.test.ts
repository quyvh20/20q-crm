import { describe, it, expect, vi, afterEach } from 'vitest';
import { asArray } from '../api';

// asArray is the load-crash stopgap for the members page (and any list endpoint):
// a response whose `data` is not an array must degrade to [] instead of reaching
// a `.filter`/`.map`/`.length` consumer and white-screening the page.
describe('asArray', () => {
  afterEach(() => vi.restoreAllMocks());

  it('passes an array through unchanged', () => {
    const arr = [{ id: 1 }, { id: 2 }];
    expect(asArray(arr, 'x')).toBe(arr);
  });

  it('coerces null/undefined to [] silently', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    expect(asArray(null, 'x')).toEqual([]);
    expect(asArray(undefined, 'x')).toEqual([]);
    expect(warn).not.toHaveBeenCalled(); // absent data is normal, not an anomaly
  });

  it('coerces an unexpected shape (object/string) to [] and warns', () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    expect(asArray({ oops: true }, 'GET /x')).toEqual([]);
    expect(asArray('nope', 'GET /x')).toEqual([]);
    expect(warn).toHaveBeenCalledTimes(2);
  });
});
