// ============================================================
// date_field trigger helpers (A4.3)
// ============================================================
//
// The backend `date_field` trigger fires "N days before/after <record>.<date
// field> at <time>" (see automation/datefield_timers.go). Timers are materialized
// event-driven from record writes. This module is the frontend side: the params
// shape, a default, and a human-readable preview. Time/timezone helpers are shared
// with the schedule trigger (cron.ts).

import { formatTime, browserTimeZone } from './cron';

export interface DateFieldParams {
  /** Object slug whose date field is watched, e.g. "deal". */
  object: string;
  /** Dotted field path, e.g. "deal.expected_close_at". */
  field: string;
  /** Negative = before the date, positive = after, 0 = on the date. */
  offset_days: number;
  /** "HH:MM" firing time-of-day; empty → 09:00. */
  at_time: string;
  /** IANA timezone; empty → UTC. */
  timezone: string;
}

export const DEFAULT_AT_TIME = '09:00';

export function defaultDateFieldParams(): DateFieldParams {
  return { object: '', field: '', offset_days: 0, at_time: DEFAULT_AT_TIME, timezone: browserTimeZone() };
}

export type OffsetDirection = 'before' | 'on' | 'after';

/** Split a signed offset into a UI direction + magnitude. */
export function offsetToDirection(offsetDays: number): { direction: OffsetDirection; days: number } {
  if (offsetDays < 0) return { direction: 'before', days: Math.abs(offsetDays) };
  if (offsetDays > 0) return { direction: 'after', days: offsetDays };
  return { direction: 'on', days: 0 };
}

/** Recombine a UI direction + magnitude into a signed offset. */
export function directionToOffset(direction: OffsetDirection, days: number): number {
  const n = Math.max(0, Math.floor(days || 0));
  if (direction === 'before') return -n;
  if (direction === 'after') return n;
  return 0;
}

/** Prettify a field path tail ("deal.expected_close_at" → "Expected Close At") for
 *  labels when the schema label isn't available. */
export function fieldPathLabel(path: string): string {
  const tail = path.includes('.') ? path.slice(path.lastIndexOf('.') + 1) : path;
  if (!tail) return path;
  return tail
    .split('_')
    .map((w) => (w ? w.charAt(0).toUpperCase() + w.slice(1) : w))
    .join(' ');
}

/**
 * Human-readable one-liner for a date_field trigger. `fieldLabel` (from the schema)
 * is used when provided, else the field path tail is prettified. `tz` is appended
 * when the timezone is non-empty.
 */
export function describeDateField(params: Partial<DateFieldParams>, fieldLabel?: string): string {
  const field = params.field || '';
  if (!field) return 'Pick a date field';
  const label = fieldLabel || fieldPathLabel(field);
  const { direction, days } = offsetToDirection(params.offset_days ?? 0);
  const time = formatTime(...parseHHMMOr9(params.at_time));

  let when: string;
  if (direction === 'on') {
    when = `On ${label}`;
  } else {
    const plural = days === 1 ? 'day' : 'days';
    when = `${days} ${plural} ${direction} ${label}`;
  }
  const sentence = `${when} at ${time}`;
  return params.timezone ? `${sentence} (${params.timezone})` : sentence;
}

/**
 * Human-readable phrasing for a wait-until delay (A4.4) — the same date math as a
 * date_field trigger, but phrased as a wait ("Wait until …").
 */
export function describeWaitUntil(params: Partial<DateFieldParams>, fieldLabel?: string): string {
  const field = params.field || '';
  if (!field) return 'Pick a date field';
  const label = fieldLabel || fieldPathLabel(field);
  const { direction, days } = offsetToDirection(params.offset_days ?? 0);
  const time = formatTime(...parseHHMMOr9(params.at_time));
  const when = direction === 'on' ? label : `${days} ${days === 1 ? 'day' : 'days'} ${direction} ${label}`;
  const sentence = `Wait until ${when} at ${time}`;
  return params.timezone ? `${sentence} (${params.timezone})` : sentence;
}

// ============================================================
// Trigger eval-context scope (A4.4 wait-until picker)
// ============================================================

/** A trigger reference — just the bits needed to reason about eval-context scope. */
export interface TriggerLike {
  type?: string;
  params?: Record<string, unknown> | null;
}

/**
 * The set of object keys whose fields are resolvable in a run's eval context for
 * the given trigger. Mirrors the backend's buildEvalContext hydration
 * (engine.go): a contact-triggered run carries contact + company; a deal-triggered
 * run carries deal + contact + company; a custom-object trigger carries just that
 * object; a schedule (no record) carries none.
 *
 * Used to scope the wait-until date-field picker so a user can't wait on a field
 * the backend will silently fail to resolve — which would skip the wait entirely
 * (resolveDelayWakeAt → ok=false → proceed immediately) rather than error.
 */
export function resolvableObjectsForTrigger(trigger?: TriggerLike | null): Set<string> {
  const set = new Set<string>();
  const type = trigger?.type;
  if (!type) {
    set.add('contact'); // safe default before a trigger is chosen
    return set;
  }

  let primary: string | null = null;
  if (type === 'schedule') {
    return set; // fires on a clock — no record in the eval context
  } else if (type === 'webhook_inbound') {
    primary = 'contact'; // inbound webhook upserts a contact
  } else if (type === 'no_activity_days') {
    const entity = trigger?.params?.entity;
    primary = (typeof entity === 'string' && entity) || 'contact';
  } else if (type === 'date_field') {
    const object = trigger?.params?.object;
    primary = (typeof object === 'string' && object) || null;
  } else if (type === 'deal_stage_changed') {
    primary = 'deal';
  } else {
    for (const suffix of ['_created', '_updated', '_deleted', '_any']) {
      if (type.endsWith(suffix)) {
        primary = type.slice(0, -suffix.length);
        break;
      }
    }
  }

  if (!primary) return set;
  set.add(primary);
  // One-hop relation hydration mirrors the backend (deal → contact → company).
  if (primary === 'deal') {
    set.add('contact');
    set.add('company');
  } else if (primary === 'contact') {
    set.add('company');
  }
  return set;
}

/**
 * The trigger's PRIMARY record object slug (the object the run fires on), or null
 * for record-less triggers (schedule). Distinct from resolvableObjectsForTrigger,
 * which also includes hydrated relations — here we want only the object the run is
 * actually about.
 */
export function triggerPrimaryObject(trigger?: TriggerLike | null): string | null {
  const type = trigger?.type;
  if (!type) return 'contact'; // safe default before a trigger is chosen
  if (type === 'schedule') return null;
  if (type === 'webhook_inbound') return 'contact';
  if (type === 'no_activity_days') {
    const entity = trigger?.params?.entity;
    return (typeof entity === 'string' && entity) || 'contact';
  }
  if (type === 'date_field') {
    const object = trigger?.params?.object;
    return (typeof object === 'string' && object) || null;
  }
  if (type === 'deal_stage_changed') return 'deal';
  for (const suffix of ['_created', '_updated', '_deleted', '_any']) {
    if (type.endsWith(suffix)) return type.slice(0, -suffix.length);
  }
  return null;
}

/**
 * The trigger record's owner object for notify_user "record owner" mode — only
 * contact/deal carry owner_user_id, so company/custom/schedule triggers return
 * null (their runs can't resolve a record owner, and the form falls back to
 * choosing a specific user).
 */
export function triggerOwnerObject(trigger?: TriggerLike | null): 'contact' | 'deal' | null {
  const primary = triggerPrimaryObject(trigger);
  return primary === 'deal' || primary === 'contact' ? primary : null;
}

/** Extract the object key from a field path ("deal.expected_close_at" → "deal"). */
export function objectKeyOfPath(path: string): string {
  const i = path.indexOf('.');
  return i > 0 ? path.slice(0, i) : path;
}

/** Parse "HH:MM" into [hour, minute], defaulting to 9:00. */
function parseHHMMOr9(at?: string): [number, number] {
  if (at) {
    const parts = at.split(':');
    if (parts.length === 2) {
      const h = Number(parts[0]);
      const m = Number(parts[1]);
      if (!Number.isNaN(h) && !Number.isNaN(m)) return [h, m];
    }
  }
  return [9, 0];
}
