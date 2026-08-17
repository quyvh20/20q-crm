// Icon + accent + label metadata for builder nodes. Replaces the old emoji icon
// system with lucide icons and a small themed accent palette.
//
// Color language (Brevo-style, 2026-08): builder chrome is colored by CATEGORY,
// not by individual step type — triggers are rose, actions are teal, rules
// (delay / condition) are violet. Each step keeps its own lucide icon for
// scanability; the hue tells you what KIND of thing you're looking at. The
// palette sidebar, the canvas node headers, and the insert menu all share these
// three accents so the taxonomy can't drift.

import {
  Mail,
  CheckSquare,
  UserPlus,
  Webhook,
  Clock,
  PencilLine,
  Phone,
  Bell,
  FilePlus2,
  Search,
  Waypoints,
  Sparkles,
  Split,
  User,
  CircleDollarSign,
  Building2,
  CalendarClock,
  CalendarDays,
  Zap,
  type LucideIcon,
} from 'lucide-react';
import type { ActionSpec, TriggerSpec, WorkflowStep, DelayParams } from '../types';
import { describeCron } from '../cron';
import { describeDateField, describeWaitUntil, type DateFieldParams } from '../dateField';

export interface NodeMeta {
  icon: LucideIcon;
  /** Tailwind text color class for the icon (theme-aware accent). */
  accent: string;
  /** Tailwind soft background tint for a light icon chip. */
  chip: string;
  /** Tailwind classes for a filled icon disc (Brevo-style solid circle). */
  solid: string;
}

/** The three category accents everything in the builder composes. */
export interface CategoryAccent {
  accent: string;
  chip: string;
  solid: string;
  /** Tinted node-card header strip. */
  header: string;
  /** Small identity dot (palette tabs). */
  dot: string;
}

export const TRIGGER_ACCENT: CategoryAccent = {
  accent: 'text-rose-600 dark:text-rose-400',
  chip: 'bg-rose-500/10',
  solid: 'bg-rose-500 text-white',
  header: 'border-b border-rose-500/15 bg-rose-500/10',
  dot: 'bg-rose-500',
};

export const ACTION_ACCENT: CategoryAccent = {
  accent: 'text-teal-600 dark:text-teal-400',
  chip: 'bg-teal-500/10',
  solid: 'bg-teal-600 text-white',
  header: 'border-b border-teal-500/15 bg-teal-500/10',
  dot: 'bg-teal-500',
};

export const RULE_ACCENT: CategoryAccent = {
  accent: 'text-violet-600 dark:text-violet-400',
  chip: 'bg-violet-500/10',
  solid: 'bg-violet-500 text-white',
  header: 'border-b border-violet-500/15 bg-violet-500/10',
  dot: 'bg-violet-500',
};

const asMeta = (icon: LucideIcon, cat: CategoryAccent): NodeMeta => ({
  icon,
  accent: cat.accent,
  chip: cat.chip,
  solid: cat.solid,
});

const ACTION_META: Record<ActionSpec['type'], NodeMeta> = {
  send_email: asMeta(Mail, ACTION_ACCENT),
  create_task: asMeta(CheckSquare, ACTION_ACCENT),
  assign_user: asMeta(UserPlus, ACTION_ACCENT),
  send_webhook: asMeta(Webhook, ACTION_ACCENT),
  delay: asMeta(Clock, RULE_ACCENT),
  update_record: asMeta(PencilLine, ACTION_ACCENT),
  log_activity: asMeta(Phone, ACTION_ACCENT),
  notify_user: asMeta(Bell, ACTION_ACCENT),
  create_record: asMeta(FilePlus2, ACTION_ACCENT),
  find_records: asMeta(Search, ACTION_ACCENT),
  enroll_records: asMeta(Waypoints, ACTION_ACCENT),
  ai_generate: asMeta(Sparkles, ACTION_ACCENT),
};

export const ACTION_TITLES: Record<ActionSpec['type'], string> = {
  send_email: 'Send an email',
  create_task: 'Create a task',
  assign_user: 'Assign a user',
  send_webhook: 'Call a webhook',
  delay: 'Time delay',
  update_record: 'Update a record',
  log_activity: 'Log an activity',
  notify_user: 'Notify a user',
  create_record: 'Create a record',
  find_records: 'Find records',
  enroll_records: 'Enroll in another automation',
  ai_generate: 'Generate with AI',
};

export function actionMeta(type: string): NodeMeta {
  return ACTION_META[type as ActionSpec['type']] ?? { icon: Zap, accent: 'text-foreground', chip: 'bg-muted', solid: 'bg-muted text-foreground' };
}

export const conditionMeta: NodeMeta = asMeta(Split, RULE_ACCENT);

export const delayMeta: NodeMeta = ACTION_META.delay;

// triggerMeta picks an icon by the trigger's source object (the slug before the
// _created/_updated/... suffix); the accent is always the trigger category rose.
export function triggerMeta(type: string | undefined): NodeMeta {
  const slug = objectSlugOf(type);
  switch (slug) {
    case 'contact':
      return asMeta(User, TRIGGER_ACCENT);
    case 'deal':
      return asMeta(CircleDollarSign, TRIGGER_ACCENT);
    case 'company':
      return asMeta(Building2, TRIGGER_ACCENT);
    default:
      if (type === 'webhook_inbound') return asMeta(Webhook, TRIGGER_ACCENT);
      if (type === 'schedule') return asMeta(CalendarClock, TRIGGER_ACCENT);
      if (type === 'date_field') return asMeta(CalendarDays, TRIGGER_ACCENT);
      return asMeta(Zap, TRIGGER_ACCENT);
  }
}

const EVENT_SUFFIXES = ['_created', '_updated', '_deleted', '_any'];

export function objectSlugOf(type: string | undefined): string {
  if (!type) return '';
  for (const s of EVENT_SUFFIXES) {
    if (type.endsWith(s)) return type.slice(0, -s.length);
  }
  return '';
}

function titleCase(slug: string): string {
  return slug ? slug.charAt(0).toUpperCase() + slug.slice(1) : slug;
}

// triggerLabel renders a human title for any trigger type, including dynamic
// {slug}_{event} forms the fixed label map doesn't cover.
export function triggerLabel(trigger: TriggerSpec | undefined): string {
  const type = trigger?.type;
  if (!type) return 'Choose a trigger';
  // Schedule shows its human cadence (e.g. "Every Monday at 9:00 AM") right on the node.
  if (type === 'schedule') {
    const cron = (trigger?.params?.cron as string) || '';
    return cron ? describeCron(cron) : 'Schedule';
  }
  // Date-field shows its relative cadence (e.g. "3 days before Expected Close At").
  if (type === 'date_field') {
    const p = (trigger?.params || {}) as Partial<DateFieldParams>;
    return p.field ? describeDateField(p) : 'Date reached';
  }
  const fixed: Record<string, string> = {
    deal_stage_changed: 'Deal Stage Changed',
    no_activity_days: 'No Activity',
    webhook_inbound: 'Webhook Inbound',
  };
  if (fixed[type]) return fixed[type];
  const slug = objectSlugOf(type);
  if (slug) {
    const event = type.slice(slug.length + 1);
    const eventLabel = event.charAt(0).toUpperCase() + event.slice(1);
    return `${titleCase(slug)} ${eventLabel}`;
  }
  return type;
}

// triggerTitle is the short, stable name shown in a trigger node's HEADER strip
// (the config-dependent detail goes in the body via triggerDescription). Unlike
// triggerLabel it never embeds the cron/date description in the title.
export function triggerTitle(trigger: TriggerSpec | undefined): string {
  const type = trigger?.type;
  if (!type) return 'Choose a trigger';
  if (type === 'schedule') return 'On a schedule';
  if (type === 'date_field') return 'Date reached';
  const fixed: Record<string, string> = {
    deal_stage_changed: 'Deal stage updated',
    no_activity_days: 'No activity',
    webhook_inbound: 'Webhook inbound',
  };
  if (fixed[type]) return fixed[type];
  const slug = objectSlugOf(type);
  if (slug) {
    const event = type.slice(slug.length + 1);
    return `${titleCase(slug)} ${event === 'any' ? 'changed' : event}`;
  }
  return type;
}

// triggerDescription is the one-line config summary shown in a trigger node's body.
export function triggerDescription(trigger: TriggerSpec | undefined): string {
  const type = trigger?.type;
  const params = (trigger?.params ?? {}) as Record<string, unknown>;
  if (!type) return 'Pick a trigger from the panel to start this automation.';
  if (type === 'schedule') {
    const cron = (params.cron as string) || '';
    return cron ? describeCron(cron) : 'Choose a recurring schedule.';
  }
  if (type === 'date_field') {
    const p = params as Partial<DateFieldParams>;
    return p.field ? describeDateField(p) : 'Choose an object and a date field.';
  }
  if (type === 'deal_stage_changed') {
    const from = (params.from_stage as string) || '';
    const to = (params.to_stage as string) || '';
    if ((from && from !== '*') || (to && to !== '*')) {
      return `Stage ${from && from !== '*' ? from : 'any'} → ${to && to !== '*' ? to : 'any'}`;
    }
    return 'When a deal moves to a new stage.';
  }
  if (type === 'no_activity_days') {
    const days = Number(params.days ?? 0);
    return days > 0 ? `After ${days} day${days === 1 ? '' : 's'} with no activity.` : 'After a period of inactivity.';
  }
  if (type === 'webhook_inbound') return 'Via an inbound webhook call.';
  const slug = objectSlugOf(type);
  if (slug) {
    const watch = (params.watch_field as string) || '';
    if (watch) return `Watching field: ${watch}`;
    const event = type.slice(slug.length + 1);
    const verb =
      event === 'any' ? 'created, updated, or deleted' : event;
    return `When a ${slug} is ${verb}.`;
  }
  return type;
}

// delayLabel renders a delay step's cadence: a wait-until description (A4.4) or a
// fixed duration. The timezone is omitted here to keep the node label short.
export function delayLabel(delay: DelayParams | undefined): string {
  if (delay?.until_field) {
    return describeWaitUntil({ field: delay.until_field, offset_days: delay.offset_days ?? 0, at_time: delay.at_time });
  }
  return humanizeDuration(delay?.duration_sec ?? 0);
}

// stepSubtitle renders a short one-line summary of a step's config for the node.
export function stepSubtitle(step: WorkflowStep): string {
  if (step.type === 'delay') {
    return delayLabel(step.delay);
  }
  if (step.type === 'condition') {
    const n = step.condition?.rules?.length ?? 0;
    return n === 0 ? 'No conditions set' : `${n} condition${n === 1 ? '' : 's'}`;
  }
  const a = step.action;
  if (!a) return '';
  const str = (k: string): string => (typeof a.params[k] === 'string' ? String(a.params[k]) : '');
  switch (a.type) {
    case 'send_email':
      return str('subject') || 'No subject';
    case 'create_task':
      return str('title') || 'Untitled task';
    case 'assign_user':
      return str('strategy') === 'round_robin' ? 'Round robin' : str('strategy') || 'Choose a strategy';
    case 'send_webhook':
      return str('url') || 'No URL set';
    case 'notify_user':
      return str('title') || 'No title set';
    case 'create_record':
      return str('object') ? `Creates a ${str('object')}` : 'Choose an object';
    case 'find_records':
      return str('object') ? `Searches ${str('object')} records` : 'Choose an object';
    case 'enroll_records':
      return str('object') ? `Enrolls ${str('object')} records` : 'Choose records to enroll';
    case 'ai_generate':
      return str('prompt') || 'No prompt set';
    case 'log_activity':
      return str('activity_type') ? `Logs a ${str('activity_type')}` : 'Logs an activity';
    default:
      return ACTION_TITLES[a.type] ?? a.type;
  }
}

export function humanizeDuration(sec: number): string {
  if (sec <= 0) return 'No wait';
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  const parts: string[] = [];
  if (d) parts.push(`${d}d`);
  if (h) parts.push(`${h}h`);
  if (m) parts.push(`${m}m`);
  if (!parts.length) parts.push(`${sec}s`);
  return `Wait ${parts.join(' ')}`;
}
