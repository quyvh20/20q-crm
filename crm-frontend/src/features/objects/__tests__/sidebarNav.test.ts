import { describe, it, expect } from 'vitest';
import { orderNavObjects, SYSTEM_NAV_FALLBACK, SYSTEM_NAV_ORDER } from '../sidebarNav';
import { listPath } from '../recordRoutes';

// The regression these guard: the sidebar used to read the legacy custom-object
// endpoint, so `company` — a system object — could never appear in the nav.

describe('orderNavObjects', () => {
  it('pins the system objects to Contacts, Companies, Deals', () => {
    // Deliberately scrambled: the backend's ordering must not be load-bearing.
    const input = [
      { slug: 'deal' },
      { slug: 'subscription' },
      { slug: 'company' },
      { slug: 'contact' },
    ];
    expect(orderNavObjects(input).map(o => o.slug)).toEqual([
      'contact', 'company', 'deal', 'subscription',
    ]);
  });

  it('keeps custom objects in registry order after the system ones', () => {
    const input = [
      { slug: 'zebra' },
      { slug: 'deal' },
      { slug: 'apple' },
      { slug: 'contact' },
    ];
    // 'zebra' before 'apple' — stable sort preserves what the backend chose
    // rather than imposing an alphabetical order the user never asked for.
    expect(orderNavObjects(input).map(o => o.slug)).toEqual([
      'contact', 'deal', 'zebra', 'apple',
    ]);
  });

  it('does not mutate its input', () => {
    const input = [{ slug: 'deal' }, { slug: 'contact' }];
    orderNavObjects(input);
    expect(input.map(o => o.slug)).toEqual(['deal', 'contact']);
  });

  it('handles an empty registry', () => {
    expect(orderNavObjects([])).toEqual([]);
  });
});

describe('SYSTEM_NAV_FALLBACK', () => {
  // A failed registry fetch resolves to this list. If it ever went empty or lost
  // an entry, the Records section would silently lose nav items the hardcoded
  // links used to guarantee.
  it('covers every pinned system object', () => {
    expect(SYSTEM_NAV_FALLBACK.map(o => o.slug)).toEqual(SYSTEM_NAV_ORDER);
  });

  it('includes Companies', () => {
    expect(SYSTEM_NAV_FALLBACK.map(o => o.slug)).toContain('company');
  });

  it('is already in display order', () => {
    expect(orderNavObjects(SYSTEM_NAV_FALLBACK)).toEqual(SYSTEM_NAV_FALLBACK);
  });

  it('gives every entry a human label', () => {
    for (const obj of SYSTEM_NAV_FALLBACK) {
      expect(obj.label_plural).toBeTruthy();
    }
  });
});

describe('nav destinations', () => {
  // Contacts and Deals keep their bespoke pages; everything else, Companies
  // included, resolves to the schema-driven unified record page.
  it('routes each system object somewhere real', () => {
    expect(listPath('contact')).toBe('/contacts');
    expect(listPath('deal')).toBe('/deals');
    expect(listPath('company')).toBe('/objects/company');
  });
});
