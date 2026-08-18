// Local, offline fallback for the AI copilot. When the /ai/draft call fails (the
// service is down, a proxy returned HTML, the request timed out), we still want the
// copilot to produce SOMETHING useful rather than a dead end — so we parse the
// prompt with a lightweight heuristic into a reasonable starting workflow. It's a
// best-effort draft: the canvas + validation are the review gate, same as an AI draft.

import type { WorkflowSchema } from '../api';
import type { WorkflowDraftInput } from '../store';
import type { WorkflowStep, TriggerSpec, ActionSpec } from '../types';

let counter = 0;
const nid = (): string => `local_${Date.now()}_${++counter}`;

function actionStep(type: ActionSpec['type'], params: Record<string, unknown>): WorkflowStep {
  const id = nid();
  return { id, type: 'action', action: { id, type, params } };
}

function delayStep(durationSec: number): WorkflowStep {
  return { id: nid(), type: 'delay', delay: { duration_sec: durationSec } };
}

/** A schema stage, plus the board it belongs to (R9.3). Declared optional here
 *  rather than assumed: the backend omits pipeline_id for a stage that predates
 *  the pipeline backfill. */
type BoardStage = WorkflowSchema['stages'][number] & { pipeline_id?: string };

/**
 * Find a stage by name, confined to ONE board.
 *
 * Every pipeline has its own "Closed Won", so a match over the flat stage list
 * keys the drafted trigger to whichever board the payload happened to order
 * first — a draft that reads correctly and then watches a pipeline the user never
 * opens. The schema carries no is_default flag, so the first stage that names a
 * board stands in for it: the schema handler orders stages by pipeline position,
 * making that the org's leftmost board. Stages from before the backfill name no
 * board at all, and there is then nothing to confine to, so the old flat match
 * stands.
 *
 * No cross-board fallback when the chosen board has no match: that is the
 * arbitrary pick this exists to remove, and an unset stage is a blank the builder
 * already asks the user to fill.
 */
function findStage(schema: WorkflowSchema | null | undefined, re: RegExp): { id: string } | undefined {
  // ?? [] rather than ?., because the backend marshals a nil stage slice as JSON
  // null — the guard is against null, not just a missing key.
  const stages: BoardStage[] = schema?.stages ?? [];
  const board = stages.find((s) => s.pipeline_id)?.pipeline_id;
  const scoped = board ? stages.filter((s) => s.pipeline_id === board) : stages;
  return scoped.find((s) => re.test(s.name.toLowerCase()));
}

function detectTrigger(p: string, schema?: WorkflowSchema | null): TriggerSpec {
  if (/\bdeal\b/.test(p) && /(won|closed[- ]?won|move[sd]?\s+to|reaches|stage|pipeline)/.test(p)) {
    const stage = findStage(schema, /won|closed/);
    return { type: 'deal_stage_changed', params: stage ? { to_stage: stage.id } : {} };
  }
  if (/\bdeal\b/.test(p) && /(created|new deal)/.test(p)) return { type: 'deal_created', params: {} };
  if (/(new contact|new lead|contact.*(created|added|signs? up)|created.*contact|signs? up)/.test(p)) {
    return { type: 'contact_created', params: {} };
  }
  if (/contact.*updated|updated.*contact|profile.*updated/.test(p)) return { type: 'contact_updated', params: {} };
  // Clicks first: "clicked the link in the email" also matches the open rule's
  // \bemail\b, so the more specific intent has to win.
  if (/\bclick(s|ed|ing)?\b[^.]{0,30}\b(link|cta|button)\b|\b(link|cta|button)\b[^.]{0,30}\bclick(s|ed|ing)?\b/.test(p)) {
    return { type: 'email_clicked', params: {} };
  }
  if (/\bopen(s|ed|ing)?\b[^.]*\bemail\b|\bemail\b[^.]*\bopen(s|ed|ing)?\b/.test(p)) {
    return { type: 'email_opened', params: {} };
  }
  if (/(every day|daily|each morning|weekly|on a schedule|\bschedule\b|\bcron\b)/.test(p)) {
    return { type: 'schedule', params: { cron: '0 9 * * *', timezone: '' } };
  }
  // Sensible default: the most common entry point.
  return { type: 'contact_created', params: {} };
}

function detectSteps(p: string, triggerType?: string): WorkflowStep[] {
  const found: { pos: number; step: WorkflowStep }[] = [];
  const push = (re: RegExp, make: () => WorkflowStep) => {
    const m = p.match(re);
    if (m) found.push({ pos: m.index ?? Number.MAX_SAFE_INTEGER, step: make() });
  };

  // "wait 2 days" / "after an hour" → delay
  const waitM = p.match(/(?:wait|after|delay|in)\s+(\d+)\s*(minute|hour|day|week)s?/);
  if (waitM) {
    const n = parseInt(waitM[1], 10);
    const unit = waitM[2];
    const sec = n * (unit === 'week' ? 604800 : unit === 'day' ? 86400 : unit === 'hour' ? 3600 : 60);
    found.push({ pos: waitM.index ?? 0, step: delayStep(sec) });
  }

  push(/\b(notify|alert)\b/, () =>
    actionStep('notify_user', {
      recipient: /manager|specific user|to a user/.test(p) ? 'specific' : 'owner_field',
      title: 'Heads up',
      body: '',
    }),
  );
  // For an engagement trigger, "email" in the prompt is usually the trigger
  // clause ("when someone opens/clicks a link in an email…"), not a send
  // instruction — require a sending verb so the draft doesn't grow a spurious
  // send_email step.
  const emailStepRe = triggerType === 'email_opened' || triggerType === 'email_clicked'
    ? /\b(send|write|shoot|reply with)\b[^.]*\bemail\b/
    : /\bemail\b/;
  push(emailStepRe, () => actionStep('send_email', { to: '{{contact.email}}', subject: '', body_html: '' }));
  push(/\btask\b|\bfollow[- ]?up\b|\bto-?do\b/, () =>
    actionStep('create_task', { title: 'Follow up', priority: 'medium', due_in_days: 3 }),
  );
  push(/\bassign\b/, () => actionStep('assign_user', { entity: 'contact', strategy: 'round_robin' }));
  push(/\blog\b|\bnote\b/, () => actionStep('log_activity', { activity_type: 'note', title: '', body: '' }));
  push(/\bwebhook\b/, () => actionStep('send_webhook', { url: '', method: 'POST' }));

  // Keep the order the user described them in; drop duplicates of the same kind.
  found.sort((a, b) => a.pos - b.pos);
  const seen = new Set<string>();
  const steps: WorkflowStep[] = [];
  for (const f of found) {
    const key = f.step.type === 'action' ? f.step.action!.type : `delay:${f.step.delay?.duration_sec}`;
    if (seen.has(key)) continue;
    seen.add(key);
    steps.push(f.step);
  }
  return steps;
}

function draftName(trigger: TriggerSpec, prompt: string): string {
  const words = prompt.trim().split(/\s+/).slice(0, 6).join(' ');
  if (words) return words.charAt(0).toUpperCase() + words.slice(1);
  return trigger.type === 'deal_stage_changed' ? 'Deal stage automation' : 'New automation';
}

/**
 * Build a best-effort workflow draft from a plain-language prompt, entirely on the
 * client. Used as the copilot's fallback when the AI service is unreachable so the
 * user always gets an editable starting point instead of an error.
 */
export function localDraftFromPrompt(prompt: string, schema?: WorkflowSchema | null): WorkflowDraftInput {
  const p = prompt.toLowerCase();
  const trigger = detectTrigger(p, schema);
  const steps = detectSteps(p, trigger.type);
  if (steps.length === 0) {
    // Nothing recognized — give them a task to start from rather than an empty flow.
    steps.push(actionStep('create_task', { title: 'Follow up', priority: 'medium', due_in_days: 3 }));
  }
  return {
    name: draftName(trigger, prompt),
    description: `Draft from: "${prompt.trim().slice(0, 140)}"`,
    trigger,
    steps,
  };
}
