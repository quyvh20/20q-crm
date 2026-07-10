// Icon + accent + label metadata for builder nodes. Replaces the old emoji icon
// system with lucide icons and a small themed accent palette.

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
  GitBranch,
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
  /** Tailwind text color class for the icon chip (theme-aware accent). */
  accent: string;
  /** Tailwind background tint for the icon chip. */
  chip: string;
}

const ACTION_META: Record<ActionSpec['type'], NodeMeta> = {
  send_email: { icon: Mail, accent: 'text-sky-600 dark:text-sky-400', chip: 'bg-sky-500/10' },
  create_task: { icon: CheckSquare, accent: 'text-emerald-600 dark:text-emerald-400', chip: 'bg-emerald-500/10' },
  assign_user: { icon: UserPlus, accent: 'text-violet-600 dark:text-violet-400', chip: 'bg-violet-500/10' },
  send_webhook: { icon: Webhook, accent: 'text-amber-600 dark:text-amber-400', chip: 'bg-amber-500/10' },
  delay: { icon: Clock, accent: 'text-amber-600 dark:text-amber-400', chip: 'bg-amber-500/10' },
  update_record: { icon: PencilLine, accent: 'text-blue-600 dark:text-blue-400', chip: 'bg-blue-500/10' },
  log_activity: { icon: Phone, accent: 'text-rose-600 dark:text-rose-400', chip: 'bg-rose-500/10' },
  notify_user: { icon: Bell, accent: 'text-fuchsia-600 dark:text-fuchsia-400', chip: 'bg-fuchsia-500/10' },
  create_record: { icon: FilePlus2, accent: 'text-teal-600 dark:text-teal-400', chip: 'bg-teal-500/10' },
  find_records: { icon: Search, accent: 'text-cyan-600 dark:text-cyan-400', chip: 'bg-cyan-500/10' },
  enroll_records: { icon: Waypoints, accent: 'text-indigo-600 dark:text-indigo-400', chip: 'bg-indigo-500/10' },
  ai_generate: { icon: Sparkles, accent: 'text-purple-600 dark:text-purple-400', chip: 'bg-purple-500/10' },
};

export const ACTION_TITLES: Record<ActionSpec['type'], string> = {
  send_email: 'Send Email',
  create_task: 'Create Task',
  assign_user: 'Assign User',
  send_webhook: 'Send Webhook',
  delay: 'Delay',
  update_record: 'Update Record',
  log_activity: 'Log Activity',
  notify_user: 'Notify User',
  create_record: 'Create Record',
  find_records: 'Find Records',
  enroll_records: 'Enroll Records',
  ai_generate: 'Generate with AI',
};

export function actionMeta(type: string): NodeMeta {
  return ACTION_META[type as ActionSpec['type']] ?? { icon: Zap, accent: 'text-foreground', chip: 'bg-muted' };
}

export const conditionMeta: NodeMeta = {
  icon: GitBranch,
  accent: 'text-indigo-600 dark:text-indigo-400',
  chip: 'bg-indigo-500/10',
};

export const delayMeta: NodeMeta = ACTION_META.delay;

// triggerMeta picks an icon by the trigger's source object (the slug before the
// _created/_updated/... suffix), so contact/deal/company/custom all read clearly.
export function triggerMeta(type: string | undefined): NodeMeta {
  const slug = objectSlugOf(type);
  switch (slug) {
    case 'contact':
      return { icon: User, accent: 'text-sky-600 dark:text-sky-400', chip: 'bg-sky-500/10' };
    case 'deal':
      return { icon: CircleDollarSign, accent: 'text-emerald-600 dark:text-emerald-400', chip: 'bg-emerald-500/10' };
    case 'company':
      return { icon: Building2, accent: 'text-violet-600 dark:text-violet-400', chip: 'bg-violet-500/10' };
    default:
      if (type === 'webhook_inbound') {
        return { icon: Webhook, accent: 'text-amber-600 dark:text-amber-400', chip: 'bg-amber-500/10' };
      }
      if (type === 'schedule') {
        return { icon: CalendarClock, accent: 'text-indigo-600 dark:text-indigo-400', chip: 'bg-indigo-500/10' };
      }
      if (type === 'date_field') {
        return { icon: CalendarDays, accent: 'text-indigo-600 dark:text-indigo-400', chip: 'bg-indigo-500/10' };
      }
      return { icon: Zap, accent: 'text-indigo-600 dark:text-indigo-400', chip: 'bg-indigo-500/10' };
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
  switch (a.type) {
    case 'send_email':
      return typeof a.params.subject === 'string' && a.params.subject ? String(a.params.subject) : 'No subject';
    case 'create_task':
      return typeof a.params.title === 'string' && a.params.title ? String(a.params.title) : 'Untitled task';
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
