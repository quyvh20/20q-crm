// The single catalog of everything a user can place on the canvas: triggers,
// actions, and rules (flow control), grouped into palette categories. Both the
// palette sidebar and the edge-"+" InsertMenu render from this module so the two
// insert surfaces can never drift (the A6 lesson: one source of truth per list).
//
// Deliberately ABSENT: webhook_inbound as a palette trigger — the inbound
// endpoint emits contact_created/contact_updated, so a workflow saved with
// trigger=webhook_inbound is never actually started by an inbound POST (it's a
// legacy false affordance; existing workflows still render + configure fine).

import {
  User,
  Building2,
  CircleDollarSign,
  CalendarClock,
  CalendarDays,
  MailOpen,
  Users,
  Boxes,
  Mail,
  Database,
  CheckSquare,
  Webhook,
  Sparkles,
  Workflow,
  Split,
  Percent,
  Clock,
  type LucideIcon,
} from 'lucide-react';
import { isForkStep } from '../types';
import type { TriggerSpec, WorkflowStep } from '../types';
import type { WorkflowSchema } from '../api';
import type { InsertContext } from './graph';
import { DEFAULT_CRON, browserTimeZone } from '../cron';
import { DEFAULT_AT_TIME } from '../dateField';
import { ACTION_TITLES, actionMeta } from './nodeMeta';

/** A step (action or rule) the user can insert into the flow. */
export interface PaletteStepItem {
  type: string;
  label: string;
  /** Palette category section this item lives under. */
  category: string;
  icon: LucideIcon;
}

/** A trigger preset: picking/dropping it replaces the workflow's trigger. */
export interface PaletteTriggerItem {
  /** Stable id used as the drag payload ("contact_created", "custom:ticket", …). */
  id: string;
  label: string;
  category: string;
  icon: LucideIcon;
  build: () => TriggerSpec;
}

const stepItem = (type: string, category: string, label?: string): PaletteStepItem => ({
  type,
  category,
  label: label ?? ACTION_TITLES[type as keyof typeof ACTION_TITLES] ?? type,
  icon: actionMeta(type).icon,
});

export const ACTION_ITEMS: PaletteStepItem[] = [
  stepItem('send_email', 'Messaging'),
  stepItem('notify_user', 'Messaging'),
  stepItem('update_record', 'Records'),
  stepItem('create_record', 'Records'),
  stepItem('find_records', 'Records'),
  stepItem('assign_user', 'Records'),
  stepItem('log_activity', 'Records'),
  stepItem('create_task', 'Tasks'),
  stepItem('send_webhook', 'Webhooks'),
  stepItem('ai_generate', 'AI'),
  stepItem('enroll_records', 'Automations'),
];

export const RULE_ITEMS: PaletteStepItem[] = [
  { type: 'delay', category: 'Rules', label: 'Time delay', icon: Clock },
  { type: 'condition', category: 'Rules', label: 'If / Else split', icon: Split },
  { type: 'split', category: 'Rules', label: 'Percentage split', icon: Percent },
];

/** Section icons for the palette category headers. */
export const CATEGORY_ICONS: Record<string, LucideIcon> = {
  Contacts: Users,
  Companies: Building2,
  Deals: CircleDollarSign,
  Email: Mail,
  Time: CalendarClock,
  'Custom objects': Boxes,
  Messaging: Mail,
  Records: Database,
  Tasks: CheckSquare,
  Webhooks: Webhook,
  AI: Sparkles,
  Automations: Workflow,
  Rules: Split,
};

const CORE_TRIGGER_ITEMS: PaletteTriggerItem[] = [
  { id: 'contact_created', label: 'Contact created', category: 'Contacts', icon: User, build: () => ({ type: 'contact_created', params: {} }) },
  { id: 'contact_updated', label: 'Contact updated', category: 'Contacts', icon: User, build: () => ({ type: 'contact_updated', params: {} }) },
  { id: 'contact_deleted', label: 'Contact deleted', category: 'Contacts', icon: User, build: () => ({ type: 'contact_deleted', params: {} }) },
  { id: 'company_created', label: 'Company created', category: 'Companies', icon: Building2, build: () => ({ type: 'company_created', params: {} }) },
  { id: 'company_updated', label: 'Company updated', category: 'Companies', icon: Building2, build: () => ({ type: 'company_updated', params: {} }) },
  { id: 'deal_created', label: 'Deal created', category: 'Deals', icon: CircleDollarSign, build: () => ({ type: 'deal_created', params: {} }) },
  { id: 'deal_stage_changed', label: 'Deal stage updated', category: 'Deals', icon: CircleDollarSign, build: () => ({ type: 'deal_stage_changed', params: {} }) },
  {
    id: 'email_opened',
    label: 'Email opened',
    category: 'Email',
    icon: MailOpen,
    build: () => ({ type: 'email_opened', params: {} }),
  },
  {
    id: 'schedule',
    label: 'On a schedule',
    category: 'Time',
    icon: CalendarClock,
    build: () => ({ type: 'schedule', params: { cron: DEFAULT_CRON, timezone: browserTimeZone() } }),
  },
  {
    id: 'date_field',
    label: 'Date reached',
    category: 'Time',
    icon: CalendarDays,
    build: () => ({
      type: 'date_field',
      params: { object: '', field: '', offset_days: 0, at_time: DEFAULT_AT_TIME, timezone: browserTimeZone() },
    }),
  },
];

// Objects that already have first-class trigger items (or aren't triggerable).
const CORE_OBJECT_KEYS = new Set(['contact', 'company', 'deal', 'trigger', 'schedule', 'date_field', 'webhook']);

/**
 * The full trigger list for the palette: core presets plus one "record created"
 * item per custom object in the org's schema (the fires-on event is refined in
 * the trigger config panel after picking).
 */
export function buildTriggerItems(schema: WorkflowSchema | null): PaletteTriggerItem[] {
  const items = [...CORE_TRIGGER_ITEMS];
  if (schema) {
    const extras = [
      ...(schema.entities ?? []),
      ...(schema.custom_objects ?? []),
    ].filter((o) => !CORE_OBJECT_KEYS.has(o.key));
    const seen = new Set<string>();
    for (const obj of extras) {
      if (seen.has(obj.key)) continue;
      seen.add(obj.key);
      items.push({
        id: `custom:${obj.key}`,
        label: `${obj.label} created`,
        category: 'Custom objects',
        icon: Boxes,
        build: () => ({ type: `${obj.key}_created`, params: {} }),
      });
    }
  }
  return items;
}

export function findTriggerItem(id: string, schema: WorkflowSchema | null): PaletteTriggerItem | undefined {
  return buildTriggerItems(schema).find((it) => it.id === id);
}

/** Depth-first lookup of a step anywhere in the tree (used to confirm an insert landed). */
export function findStepById(steps: WorkflowStep[], id: string): WorkflowStep | undefined {
  for (const s of steps) {
    if (s.id === id) return s;
    if (isForkStep(s)) {
      const hit = findStepById(s.yes_steps ?? [], id) ?? findStepById(s.no_steps ?? [], id);
      if (hit) return hit;
    }
  }
  return undefined;
}

/**
 * The slot a palette CLICK inserts into: the tail of the main path. Forks
 * (condition/split) are terminal in their sibling list (no-merge invariant), so
 * when the list ends in one we descend into its Yes/A branch — appending at the
 * root would create a forbidden step-after-fork.
 */
export function firstOpenSlot(steps: WorkflowStep[]): InsertContext {
  let list = steps;
  let parentId: string | null = null;
  let branch: 'yes' | 'no' | null = null;
  for (;;) {
    const last = list.length ? list[list.length - 1] : undefined;
    if (!last || !isForkStep(last)) {
      return { parentId, branch, index: list.length };
    }
    parentId = last.id;
    branch = 'yes';
    list = last.yes_steps ?? [];
  }
}
