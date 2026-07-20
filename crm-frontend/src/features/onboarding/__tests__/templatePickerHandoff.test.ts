import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest';
import { markTemplatePickerPending, consumeTemplatePickerPending } from '../templatePickerHandoff';

describe('templatePickerHandoff', () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it('is not pending by default', () => {
    expect(consumeTemplatePickerPending()).toBe(false);
  });

  it('hands the flag across a load', () => {
    markTemplatePickerPending();
    expect(consumeTemplatePickerPending()).toBe(true);
  });

  // The whole point: a re-render, a manual refresh, or navigating back to the
  // dashboard must not reopen a picker the user already dismissed.
  it('consumes on read so the picker opens exactly once', () => {
    markTemplatePickerPending();
    expect(consumeTemplatePickerPending()).toBe(true);
    expect(consumeTemplatePickerPending()).toBe(false);
    expect(consumeTemplatePickerPending()).toBe(false);
  });

  it('leaves no key behind once consumed', () => {
    markTemplatePickerPending();
    consumeTemplatePickerPending();
    expect(sessionStorage.getItem('template_picker_pending')).toBeNull();
  });

  it('ignores a foreign value in the slot', () => {
    sessionStorage.setItem('template_picker_pending', 'yes');
    expect(consumeTemplatePickerPending()).toBe(false);
  });

  // Private browsing / storage disabled must degrade softly: the picker simply
  // doesn't auto-open, and stays reachable from the setup checklist.
  it('survives sessionStorage throwing on write', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError');
    });
    expect(() => markTemplatePickerPending()).not.toThrow();
  });

  it('survives sessionStorage throwing on read', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('SecurityError');
    });
    expect(consumeTemplatePickerPending()).toBe(false);
  });

  afterEach(() => {
    vi.restoreAllMocks();
    sessionStorage.clear();
  });
});
