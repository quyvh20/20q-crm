// Currency + locale option lists for the settings dropdowns, with human-readable
// labels. Both are derived at runtime and labelled via the platform's own Intl data
// (the same approach ProfileSection already uses for timezones via
// Intl.supportedValuesOf('timeZone')), so the lists stay comprehensive and current
// without a hand-maintained array to drift out of date.
//
// The backend stores currency/locale as free strings (currency ≤8 chars, locale ≤16),
// with no allowlist, so any ISO 4217 code / BCP-47 tag here saves fine.

export interface IntlOption {
  value: string;
  /** e.g. "USD — US Dollar" or "en-US — American English". Falls back to the raw code. */
  label: string;
}

function displayNames(type: 'currency' | 'language'): Intl.DisplayNames | null {
  try {
    return new Intl.DisplayNames(['en'], { type });
  } catch {
    return null;
  }
}

// A handful of currencies people reach for first, floated above the A→Z remainder.
const POPULAR_CURRENCIES = [
  'USD', 'EUR', 'GBP', 'JPY', 'CNY', 'INR', 'CAD', 'AUD',
  'CHF', 'SGD', 'HKD', 'NZD', 'VND', 'BRL', 'MXN', 'KRW',
];

// Fallback if Intl.supportedValuesOf is unavailable — still far more than the old 8.
const FALLBACK_CURRENCIES = [
  ...POPULAR_CURRENCIES,
  'SEK', 'NOK', 'DKK', 'PLN', 'CZK', 'HUF', 'RON', 'TRY', 'RUB', 'UAH',
  'ZAR', 'AED', 'SAR', 'QAR', 'ILS', 'EGP', 'NGN', 'KES', 'GHS',
  'THB', 'IDR', 'MYR', 'PHP', 'TWD', 'PKR', 'BDT', 'LKR',
  'CLP', 'COP', 'ARS', 'PEN', 'UYU',
];

let currencyCache: IntlOption[] | null = null;

/** Comprehensive, labelled currency list — popular codes first, then the rest A→Z. */
export function currencyOptions(): IntlOption[] {
  if (currencyCache) return currencyCache;

  let codes: string[];
  try {
    codes = Intl.supportedValuesOf('currency');
  } catch {
    codes = FALLBACK_CURRENCIES;
  }
  const dn = displayNames('currency');
  const label = (code: string) => {
    const name = dn?.of(code);
    return name && name !== code ? `${code} — ${name}` : code;
  };

  const seen = new Set(codes);
  const popular = POPULAR_CURRENCIES.filter((c) => seen.has(c));
  const popularSet = new Set(popular);
  const rest = codes.filter((c) => !popularSet.has(c)).sort();

  currencyCache = [...popular, ...rest].map((c) => ({ value: c, label: label(c) }));
  return currencyCache;
}

// A generous, curated set of common language–region locales. Every entry is a valid
// BCP-47 tag ≤16 chars (the backend's column width). Labelled via Intl.DisplayNames,
// e.g. "en-GB — British English", "pt-BR — Brazilian Portuguese".
const LOCALE_CODES = [
  'en-US', 'en-GB', 'en-CA', 'en-AU', 'en-IN', 'en-NZ', 'en-IE', 'en-ZA', 'en-SG',
  'es-ES', 'es-MX', 'es-AR', 'es-CO', 'es-CL', 'es-US',
  'fr-FR', 'fr-CA', 'fr-BE', 'fr-CH',
  'de-DE', 'de-AT', 'de-CH',
  'pt-BR', 'pt-PT',
  'it-IT', 'nl-NL', 'nl-BE',
  'sv-SE', 'nb-NO', 'da-DK', 'fi-FI', 'is-IS',
  'pl-PL', 'cs-CZ', 'sk-SK', 'ro-RO', 'hu-HU', 'el-GR', 'bg-BG', 'hr-HR', 'sr-RS',
  'uk-UA', 'ru-RU', 'tr-TR',
  'ar-SA', 'ar-AE', 'ar-EG', 'he-IL', 'fa-IR',
  'hi-IN', 'bn-IN', 'ta-IN', 'te-IN', 'mr-IN', 'gu-IN', 'ur-PK',
  'zh-CN', 'zh-TW', 'zh-HK', 'ja-JP', 'ko-KR',
  'th-TH', 'vi-VN', 'id-ID', 'ms-MY', 'fil-PH', 'km-KH',
];

let localeCache: IntlOption[] | null = null;

/** Comprehensive, labelled locale list (BCP-47 tags). */
export function localeOptions(): IntlOption[] {
  if (localeCache) return localeCache;
  const dn = displayNames('language');
  localeCache = LOCALE_CODES.map((code) => {
    const name = dn?.of(code);
    return { value: code, label: name && name !== code ? `${code} — ${name}` : code };
  });
  return localeCache;
}
