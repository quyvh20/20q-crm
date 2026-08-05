// Locale- and currency-aware display formatting.
//
// This module is deliberately PURE: it imports nothing, holds no state and
// touches no React. Every helper takes the locale (and, for money, the currency)
// as an explicit argument, which makes it trivially unit-testable and free at
// lint time — react-refresh/only-export-components never fires on a file that
// exports no component, so the helpers cost zero of the warning budget. The hook
// that actually reads the workspace lives next door in `useWorkspaceFormat.ts`
// for the same reason; keep React out of this file.
//
// ── Where currency and locale come from, and why they are not trusted ─────────
// The workspace stores both as FREE-FORM strings: `currency varchar(8)` and
// `locale varchar(16)`, each `NOT NULL DEFAULT ''`, with no allowlist anywhere
// (crm-backend internal/domain/models.go; the PATCH in workspace_usecase.go only
// TrimSpace()s the value). The settings dropdown (lib/intlOptions.ts) offers ISO
// 4217 codes and BCP-47 tags, so a workspace whose admin has visited Workspace
// General really does hold e.g. "USD" / "en-GB" — a CODE, not a symbol. But:
//
//   * every workspace that has never opened that page still holds "" for both, and
//   * nothing server-side stops a direct API call from storing "$", "usd" or junk.
//
// That matters because Intl is strict about exactly those two cases. Measured:
//     new Intl.NumberFormat('en-US', { currency: '' })   → RangeError
//     new Intl.NumberFormat('en-US', { currency: '$' })  → RangeError
//     new Intl.NumberFormat('', {})                      → RangeError
// while a well-formed but unknown code does NOT throw:
//     new Intl.NumberFormat('en-US', { currency: 'ZZZ' }).format(1234.5) → "ZZZ 1,234.50"
//
// So the stored value is normalised (resolveCurrency / resolveLocale) before it
// ever reaches Intl, and the Intl call is still wrapped defensively. An unknown
// three-letter code is deliberately allowed through: printing "ZZZ 1,234.50" is
// visibly wrong in a way an admin can fix, whereas an uncaught RangeError blanks
// the page.
//
// ── Money precision: read this before doing arithmetic on an amount ───────────
// Amounts arrive as Go `float64` (domain.Deal.Value) and land in JS as IEEE-754
// doubles — bit-for-bit the same representation, so nothing is lost in transit.
// What IS lossy is arithmetic performed here: `value * probability / 100`
// accumulates binary-fraction error, and integers past 2^53 are not exactly
// representable at all. Intl then rounds to the currency's minor units for
// display, which HIDES that error rather than fixing it — a drifted total simply
// prints as a clean-looking number.
//
// Therefore: format at the last possible moment, never parse a formatted string
// back into a stored amount, and compute any total that gets persisted on the
// server. Note also that the minor-unit count is currency-dependent (USD prints
// 2 decimals, JPY prints 0), so a hand-rolled `.toFixed(2)` is wrong for a third
// of the world; that is exactly what these helpers exist to stop.

/** ISO 4217 shape: exactly three ASCII letters. Intl accepts nothing else. */
const ISO_4217 = /^[A-Z]{3}$/;

/**
 * Last-ditch rescue for a workspace whose `currency` column holds a SYMBOL
 * rather than a code. The settings dropdown cannot produce these, but the
 * column is free-form and 8 chars wide, so a hand-written PATCH or an import
 * can. Mapping the handful that are unambiguous beats falling all the way back
 * to USD and silently relabelling someone's euros.
 */
const SYMBOL_TO_CODE: Record<string, string> = {
  $: 'USD',
  '€': 'EUR',
  '£': 'GBP',
  '¥': 'JPY',
  '₹': 'INR',
  '₫': 'VND',
  '₩': 'KRW',
  '₽': 'RUB',
  '₺': 'TRY',
  R$: 'BRL',
};

/** Used when the workspace has no usable currency — the unconfigured default. */
export const FALLBACK_CURRENCY = 'USD';

/** Shared "which locale" knob. Undefined/blank means the runtime's own locale. */
interface LocaleOption {
  /** BCP-47 tag, e.g. "en-GB". Blank or malformed falls back to the runtime locale. */
  locale?: string | null;
}

export type NumberFormatOptions = Intl.NumberFormatOptions & LocaleOption;
export type CurrencyFormatOptions = Intl.NumberFormatOptions & LocaleOption & {
  /** ISO 4217 code, e.g. "USD". Blank/malformed falls back to FALLBACK_CURRENCY. */
  currency?: string | null;
};
export type DateFormatOptions = Intl.DateTimeFormatOptions & LocaleOption;

/**
 * Normalise a stored locale into something Intl will accept, or `undefined` to
 * mean "use the runtime's locale". Blank (the column default) and malformed
 * tags both become undefined rather than throwing.
 */
export function resolveLocale(locale?: string | null): string | undefined {
  const tag = (locale ?? '').trim();
  if (!tag) return undefined;
  try {
    return Intl.getCanonicalLocales(tag)[0] ?? undefined;
  } catch {
    return undefined;
  }
}

/**
 * Normalise a stored currency into a code Intl will accept. Case is repaired
 * ("usd" → "USD"), a known symbol is mapped to its code, and anything else —
 * including the "" default — falls back to FALLBACK_CURRENCY.
 */
export function resolveCurrency(currency?: string | null): string {
  const raw = (currency ?? '').trim();
  if (!raw) return FALLBACK_CURRENCY;
  const upper = raw.toUpperCase();
  if (ISO_4217.test(upper)) return upper;
  return SYMBOL_TO_CODE[raw] ?? SYMBOL_TO_CODE[upper] ?? FALLBACK_CURRENCY;
}

/**
 * Coerce a record value to a finite number, or null when it isn't one.
 * Booleans are rejected on purpose: `Number(true) === 1` would render a checkbox
 * value as "1". Empty string is "no value", not zero.
 */
function toFiniteNumber(value: unknown): number | null {
  if (value === null || value === undefined || value === '') return null;
  if (typeof value === 'boolean') return null;
  const n = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(n) ? n : null;
}

/** Nothing at all ⇒ empty string; something unformattable ⇒ show it verbatim. */
function passthrough(value: unknown): string {
  return value === null || value === undefined ? '' : String(value);
}

/**
 * Group-separated number for display, e.g. 1234567 → "1,234,567" (en-US) or
 * "1.234.567" (de-DE).
 *
 * Zero formats as "0" — it is a value, not an absence. Null/undefined/'' give
 * "". A non-numeric value is passed through unchanged so a mistyped field shows
 * its content rather than blanking.
 */
export function formatNumber(value: unknown, opts: NumberFormatOptions = {}): string {
  const n = toFiniteNumber(value);
  if (n === null) return passthrough(value);
  const { locale, ...intl } = opts;
  try {
    return new Intl.NumberFormat(resolveLocale(locale), intl).format(n);
  } catch {
    return String(n);
  }
}

/**
 * Money for display in the workspace's currency, e.g. "$1,234.56" (en-US/USD),
 * "1.234,56 €" (de-DE/EUR), "¥1,235" (JPY — zero minor units).
 *
 * The currency is a CODE, never a symbol: Intl derives the symbol, the placement
 * and the number of decimals from the code plus the locale. See the module
 * header for the precision hazards before doing arithmetic on the input.
 */
export function formatCurrency(value: unknown, opts: CurrencyFormatOptions = {}): string {
  const n = toFiniteNumber(value);
  if (n === null) return passthrough(value);
  const { locale, currency, ...intl } = opts;
  const code = resolveCurrency(currency);
  try {
    return new Intl.NumberFormat(resolveLocale(locale), {
      style: 'currency',
      currency: code,
      ...intl,
    }).format(n);
  } catch {
    // Belt and braces: the inputs are already normalised, so reaching here means
    // an engine that rejects an option combination. Still show the amount.
    return `${code} ${formatNumber(n, { locale })}`;
  }
}

/** A date-only value: no time, therefore no timezone. */
const DATE_ONLY = /^\d{4}-\d{2}-\d{2}$/;

/**
 * Date for display in the viewer's locale. Defaults to the short numeric form,
 * matching what `toLocaleDateString()` produced before; pass Intl options for
 * anything else.
 *
 * A date-ONLY string ("2026-01-15" — what every `type="date"` input and every
 * `date` custom field stores) is parsed as LOCAL midnight rather than UTC
 * midnight. `new Date('2026-01-15')` is specified as UTC, so west of Greenwich
 * it renders as the 14th — the day before the one the user typed. A full
 * timestamp keeps its instant semantics and is unaffected.
 *
 * Null/undefined/'' and unparseable input both give "" so the caller can pick
 * its own placeholder; nothing ever renders the string "Invalid Date".
 */
export function formatDate(value: unknown, opts: DateFormatOptions = {}): string {
  if (value === null || value === undefined || value === '') return '';
  let d: Date;
  if (value instanceof Date) {
    d = value;
  } else if (typeof value === 'string' && DATE_ONLY.test(value.trim())) {
    const [y, m, day] = value.trim().split('-').map(Number);
    d = new Date(y, m - 1, day);
  } else if (typeof value === 'string' || typeof value === 'number') {
    d = new Date(value);
  } else {
    return '';
  }
  if (Number.isNaN(d.getTime())) return '';
  const { locale, ...intl } = opts;
  try {
    return new Intl.DateTimeFormat(resolveLocale(locale), intl).format(d);
  } catch {
    return d.toISOString().slice(0, 10);
  }
}
