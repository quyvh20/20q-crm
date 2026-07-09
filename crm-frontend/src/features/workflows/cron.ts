// ============================================================
// Cron helpers for the schedule trigger (A4.2)
// ============================================================
//
// The backend arms schedule timers from a standard 5-field cron spec
// (`minute hour day-of-month month day-of-week`) parsed by robfig/cron with the
// @descriptor set (see automation/timers.go). This module is the frontend side:
// it turns a friendly "frequency + time + day" model into that exact cron string
// (and back), plus a human-readable preview. Pure + dependency-free so it is
// trivially unit-testable and safe to import from the store and node metadata.

export type ScheduleFrequency = 'hourly' | 'daily' | 'weekly' | 'monthly' | 'custom';

export interface ScheduleModel {
  frequency: ScheduleFrequency;
  /** 0–59. For every frequency except custom. */
  minute: number;
  /** 0–23. Used by daily/weekly/monthly. */
  hour: number;
  /** 0 (Sunday) – 6 (Saturday). Used by weekly. */
  dayOfWeek: number;
  /** 1–31. Used by monthly (capped at 28 in the builder so every month fires). */
  dayOfMonth: number;
  /** The raw cron expression — canonical for `custom`, derived for the rest. */
  cron: string;
}

export const WEEKDAYS = [
  'Sunday',
  'Monday',
  'Tuesday',
  'Wednesday',
  'Thursday',
  'Friday',
  'Saturday',
] as const;

/** Default: every Monday at 9:00am — the same default the schedule form seeds. */
export const DEFAULT_CRON = '0 9 * * 1';

export function defaultScheduleModel(): ScheduleModel {
  return {
    frequency: 'weekly',
    minute: 0,
    hour: 9,
    dayOfWeek: 1,
    dayOfMonth: 1,
    cron: DEFAULT_CRON,
  };
}

// --- small utilities -------------------------------------------------------

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function clamp(n: number, lo: number, hi: number): number {
  if (Number.isNaN(n)) return lo;
  return Math.min(hi, Math.max(lo, n));
}

/** Parse a cron field that must be a plain integer, else null (any wildcard/list/range). */
function intField(field: string): number | null {
  return /^\d+$/.test(field) ? Number(field) : null;
}

/** robfig accepts 7 as Sunday; normalise to 0–6. */
function normalizeDow(n: number): number {
  return n === 7 ? 0 : clamp(n, 0, 6);
}

function ordinal(n: number): string {
  const s = ['th', 'st', 'nd', 'rd'];
  const v = n % 100;
  return n + (s[(v - 20) % 10] || s[v] || s[0]);
}

/** 24h → "9:00 AM" style. */
export function formatTime(hour: number, minute: number): string {
  const h = clamp(hour, 0, 23);
  const m = clamp(minute, 0, 59);
  const period = h < 12 ? 'AM' : 'PM';
  const h12 = h % 12 === 0 ? 12 : h % 12;
  return `${h12}:${pad2(m)} ${period}`;
}

// --- build / parse ---------------------------------------------------------

/** Structured model → 5-field cron string. `custom` returns the raw expression. */
export function buildCron(model: ScheduleModel): string {
  const mi = clamp(model.minute, 0, 59);
  const ho = clamp(model.hour, 0, 23);
  switch (model.frequency) {
    case 'hourly':
      return `${mi} * * * *`;
    case 'daily':
      return `${mi} ${ho} * * *`;
    case 'weekly':
      return `${mi} ${ho} * * ${normalizeDow(model.dayOfWeek)}`;
    case 'monthly':
      return `${mi} ${ho} ${clamp(model.dayOfMonth, 1, 31)} * *`;
    case 'custom':
    default:
      return model.cron.trim();
  }
}

/**
 * Cron string → structured model. Recognises the four shapes the builder emits;
 * anything else (ranges, lists, steps, @descriptors) falls back to `custom` with
 * sensible defaults for the hidden fields so switching frequency stays usable.
 */
export function parseCron(expr: string): ScheduleModel {
  const base = defaultScheduleModel();
  const s = (expr || '').trim();
  const fields = s.split(/\s+/);

  if (fields.length === 5) {
    const [mi, ho, dom, mon, dow] = fields;
    const miN = intField(mi);
    const hoN = intField(ho);
    const domN = intField(dom);
    const dowN = intField(dow);
    const star = (f: string) => f === '*';

    // hourly: `M * * * *`
    if (miN !== null && star(ho) && star(dom) && star(mon) && star(dow)) {
      return { ...base, frequency: 'hourly', minute: miN, cron: s };
    }
    // daily: `M H * * *`
    if (miN !== null && hoN !== null && star(dom) && star(mon) && star(dow)) {
      return { ...base, frequency: 'daily', minute: miN, hour: hoN, cron: s };
    }
    // weekly: `M H * * D`
    if (miN !== null && hoN !== null && star(dom) && star(mon) && dowN !== null) {
      return { ...base, frequency: 'weekly', minute: miN, hour: hoN, dayOfWeek: normalizeDow(dowN), cron: s };
    }
    // monthly: `M H DOM * *`
    if (miN !== null && hoN !== null && domN !== null && star(mon) && star(dow)) {
      return { ...base, frequency: 'monthly', minute: miN, hour: hoN, dayOfMonth: clamp(domN, 1, 31), cron: s };
    }
  }

  return { ...base, frequency: 'custom', cron: s };
}

// --- validation ------------------------------------------------------------

/**
 * Lightweight sanity check so the form can flag an obviously bad expression before
 * save. The backend (robfig/cron) is the authoritative parser — this is permissive
 * on purpose (it must never reject an expression the backend would accept).
 */
export function isValidCron(expr: string): boolean {
  const s = (expr || '').trim();
  if (!s) return false;
  if (s.startsWith('@')) {
    return /^@(hourly|daily|midnight|weekly|monthly|yearly|annually|reboot|every\s+\S.*)$/i.test(s);
  }
  const fields = s.split(/\s+/);
  if (fields.length !== 5) return false;
  // Each field: digits, wildcards, names, and the range/list/step separators.
  return fields.every((f) => /^[0-9A-Za-z*,/-]+$/.test(f));
}

// --- human preview ---------------------------------------------------------

/** Best-effort description of a non-structured cron / @descriptor. */
function describeGenericCron(expr: string): string {
  const s = expr.trim();
  const lower = s.toLowerCase();
  const descriptors: Record<string, string> = {
    '@hourly': 'Every hour',
    '@daily': 'Every day at midnight',
    '@midnight': 'Every day at midnight',
    '@weekly': 'Every week (Sunday at midnight)',
    '@monthly': 'On the 1st of every month at midnight',
    '@yearly': 'Once a year (Jan 1 at midnight)',
    '@annually': 'Once a year (Jan 1 at midnight)',
  };
  if (descriptors[lower]) return descriptors[lower];
  const every = /^@every\s+(.+)$/i.exec(s);
  if (every) return `Every ${every[1].trim()}`;
  return `Custom schedule (${s})`;
}

/**
 * Human-readable one-liner for a cron expression (the builder's preview + the
 * canvas trigger-node subtitle). `tz` is appended when provided.
 */
export function describeCron(expr: string, tz?: string): string {
  const model = parseCron(expr);
  let sentence: string;
  switch (model.frequency) {
    case 'hourly':
      sentence = model.minute === 0 ? 'Every hour, on the hour' : `Every hour at :${pad2(model.minute)}`;
      break;
    case 'daily':
      sentence = `Every day at ${formatTime(model.hour, model.minute)}`;
      break;
    case 'weekly':
      sentence = `Every ${WEEKDAYS[model.dayOfWeek]} at ${formatTime(model.hour, model.minute)}`;
      break;
    case 'monthly':
      sentence = `On the ${ordinal(model.dayOfMonth)} of every month at ${formatTime(model.hour, model.minute)}`;
      break;
    case 'custom':
    default:
      sentence = describeGenericCron(model.cron);
      break;
  }
  return tz ? `${sentence} (${tz})` : sentence;
}

// --- timezones -------------------------------------------------------------

/** The viewer's IANA zone, or 'UTC' if it can't be resolved. */
export function browserTimeZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

// Curated fallback for environments without Intl.supportedValuesOf.
const COMMON_TIMEZONES = [
  'UTC',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Sao_Paulo',
  'Europe/London',
  'Europe/Paris',
  'Europe/Berlin',
  'Europe/Moscow',
  'Asia/Dubai',
  'Asia/Kolkata',
  'Asia/Singapore',
  'Asia/Shanghai',
  'Asia/Tokyo',
  'Australia/Sydney',
];

/**
 * All IANA zones the runtime knows (via Intl.supportedValuesOf), falling back to a
 * curated common list. `Intl.supportedValuesOf` isn't in this project's TS lib, so it
 * is feature-detected through a cast rather than a direct call.
 */
export function allTimeZones(): string[] {
  try {
    const sv = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
    if (typeof sv === 'function') {
      const zones = sv('timeZone');
      if (Array.isArray(zones) && zones.length > 0) return zones;
    }
  } catch {
    /* fall through to the curated list */
  }
  return COMMON_TIMEZONES;
}
