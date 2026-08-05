import { describe, it, expect } from 'vitest';
import {
  formatCurrency,
  formatDate,
  formatNumber,
  resolveCurrency,
  resolveLocale,
  FALLBACK_CURRENCY,
} from '../format';
import { workspaceFormatQueryKey } from '../useWorkspaceFormat';
import { settingsKeys } from '../../features/settings/queries';

// Intl output contains U+00A0 (and U+202F) in several locales — "1.000,00 €" and
// "12 k €" both use a non-breaking space. Comparing raw strings makes these
// tests fail for a reason that has nothing to do with the code under test.
const norm = (s: string) => s.replace(/\s/g, ' ');

describe('resolveCurrency — the workspace column is free-form, Intl is not', () => {
  it('passes a real ISO 4217 code straight through', () => {
    expect(resolveCurrency('USD')).toBe('USD');
    expect(resolveCurrency('VND')).toBe('VND');
  });

  it('repairs case and surrounding whitespace', () => {
    expect(resolveCurrency('eur')).toBe('EUR');
    expect(resolveCurrency('  gbp  ')).toBe('GBP');
  });

  it('falls back for the "" that every unconfigured workspace stores', () => {
    // domain/models.go declares currency as `not null;default:''`, so this is
    // the value for every org that never opened Workspace General.
    expect(resolveCurrency('')).toBe(FALLBACK_CURRENCY);
    expect(resolveCurrency(null)).toBe(FALLBACK_CURRENCY);
    expect(resolveCurrency(undefined)).toBe(FALLBACK_CURRENCY);
  });

  it('maps a stored SYMBOL onto its code instead of defaulting', () => {
    // The settings dropdown can't produce these, but the column is free-form
    // varchar(8) with no allowlist, so an import or a hand-written PATCH can.
    expect(resolveCurrency('$')).toBe('USD');
    expect(resolveCurrency('€')).toBe('EUR');
    expect(resolveCurrency('£')).toBe('GBP');
    expect(resolveCurrency('₫')).toBe('VND');
  });

  it('falls back on junk rather than handing Intl something that throws', () => {
    expect(resolveCurrency('dollars')).toBe(FALLBACK_CURRENCY);
    expect(resolveCurrency('12')).toBe(FALLBACK_CURRENCY);
  });

  it('lets a well-formed but unknown code through — visibly wrong beats blank', () => {
    expect(resolveCurrency('ZZZ')).toBe('ZZZ');
    expect(() => formatCurrency(1, { currency: 'ZZZ', locale: 'en-US' })).not.toThrow();
  });
});

describe('resolveLocale', () => {
  it('canonicalises a valid tag', () => {
    expect(resolveLocale('en-GB')).toBe('en-GB');
    expect(resolveLocale('DE-de')).toBe('de-DE');
  });

  it('returns undefined — "use the runtime locale" — for blank or malformed', () => {
    // '' is the column default, and `new Intl.NumberFormat('')` throws.
    expect(resolveLocale('')).toBeUndefined();
    expect(resolveLocale('   ')).toBeUndefined();
    expect(resolveLocale('not a locale')).toBeUndefined();
    expect(resolveLocale(null)).toBeUndefined();
  });
});

describe('formatCurrency', () => {
  it('renders the workspace currency, not a hardcoded dollar sign', () => {
    expect(norm(formatCurrency(1234.56, { locale: 'en-US', currency: 'USD' }))).toBe('$1,234.56');
    expect(norm(formatCurrency(1234.56, { locale: 'de-DE', currency: 'EUR' }))).toBe('1.234,56 €');
    expect(norm(formatCurrency(1234.56, { locale: 'en-GB', currency: 'GBP' }))).toBe('£1,234.56');
  });

  it('uses the currency\'s own minor units — JPY has none', () => {
    // The whole reason a hand-rolled `.toFixed(2)` is wrong for much of the world.
    expect(norm(formatCurrency(1234.56, { locale: 'en-US', currency: 'JPY' }))).toBe('¥1,235');
  });

  it('formats zero as an amount, never as an absence', () => {
    expect(norm(formatCurrency(0, { locale: 'en-US', currency: 'USD' }))).toBe('$0.00');
  });

  it('survives the unconfigured workspace: blank currency AND blank locale', () => {
    // Both are the DB defaults, and both make Intl throw if passed through raw.
    const out = formatCurrency(1000, { locale: '', currency: '' });
    expect(out).toContain('1');
    expect(() => formatCurrency(1000, { locale: '', currency: '' })).not.toThrow();
  });

  it('renders nothing for a missing amount, and passes non-numbers through', () => {
    expect(formatCurrency(null)).toBe('');
    expect(formatCurrency(undefined)).toBe('');
    expect(formatCurrency('')).toBe('');
    expect(formatCurrency('not money')).toBe('not money');
  });

  it('accepts extra Intl options, e.g. compact notation for a chart axis', () => {
    expect(norm(formatCurrency(12000, {
      locale: 'en-US', currency: 'USD', notation: 'compact', maximumFractionDigits: 0,
    }))).toBe('$12K');
  });
});

describe('formatNumber', () => {
  it('groups digits per locale', () => {
    expect(norm(formatNumber(1234567, { locale: 'en-US' }))).toBe('1,234,567');
    expect(norm(formatNumber(1234567, { locale: 'de-DE' }))).toBe('1.234.567');
  });

  it('formats zero as "0", not as empty', () => {
    expect(formatNumber(0, { locale: 'en-US' })).toBe('0');
  });

  it('treats null/undefined/"" as no value', () => {
    expect(formatNumber(null)).toBe('');
    expect(formatNumber(undefined)).toBe('');
    expect(formatNumber('')).toBe('');
  });

  it('passes a non-numeric value through instead of blanking the cell', () => {
    expect(formatNumber('N/A', { locale: 'en-US' })).toBe('N/A');
  });

  it('does not turn a boolean into 1/0', () => {
    // Number(true) === 1 would render a checkbox value as "1".
    expect(formatNumber(true)).toBe('true');
    expect(formatNumber(false)).toBe('false');
  });

  it('coerces a numeric string, which is how custom fields arrive', () => {
    expect(norm(formatNumber('42000', { locale: 'en-US' }))).toBe('42,000');
  });

  it('falls back to the runtime locale on a malformed tag', () => {
    expect(() => formatNumber(1000, { locale: 'not a locale' })).not.toThrow();
  });
});

describe('formatDate', () => {
  it('renders per locale', () => {
    expect(formatDate('2026-03-09T12:00:00Z', { locale: 'en-US' })).toBe('3/9/2026');
    expect(formatDate('2026-03-09T12:00:00Z', { locale: 'en-GB' })).toBe('09/03/2026');
  });

  it('keeps a date-ONLY value on the day the user typed', () => {
    // `new Date('2026-01-15')` is UTC midnight by spec, so west of Greenwich the
    // old `new Date(v).toLocaleDateString()` rendered the 14th. Every type="date"
    // input and every `date` custom field stores exactly this shape.
    expect(formatDate('2026-01-15', { locale: 'en-US' })).toBe('1/15/2026');
    expect(formatDate('2026-01-01', { locale: 'en-US' })).toBe('1/1/2026');
  });

  it('accepts a Date and a timestamp', () => {
    expect(formatDate(new Date(2026, 0, 15), { locale: 'en-US' })).toBe('1/15/2026');
    expect(formatDate(Date.UTC(2026, 0, 15, 12), { locale: 'en-US' })).toBe('1/15/2026');
  });

  it('honours Intl options', () => {
    expect(formatDate('2026-03-09', { locale: 'en-US', month: 'short', year: '2-digit' })).toBe('Mar 26');
  });

  it('returns "" for missing or unparseable input — never "Invalid Date"', () => {
    expect(formatDate(null)).toBe('');
    expect(formatDate(undefined)).toBe('');
    expect(formatDate('')).toBe('');
    expect(formatDate('garbage')).toBe('');
    expect(formatDate({})).toBe('');
  });

  it('falls back to the runtime locale on a malformed tag', () => {
    expect(() => formatDate('2026-03-09', { locale: 'not a locale' })).not.toThrow();
  });
});

describe('useWorkspaceFormat query key', () => {
  it('is the SAME key settings uses, so a currency change propagates', () => {
    // useWorkspaceFormat duplicates this literal (lib/ must not import features/).
    // If settingsKeys.workspace() ever moves, the two caches silently diverge and
    // saving a new currency stops re-rendering the deal pages — so pin it here.
    expect([...workspaceFormatQueryKey]).toEqual([...settingsKeys.workspace()]);
  });
});
