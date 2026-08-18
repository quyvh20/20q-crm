import type { WorkflowStep } from '../types';
import { isForkStep } from '../types';
import type { SchemaEntity } from '../api';

/**
 * A wait-for-event step (A9) always completes and publishes its outcome as
 * `{{actions.<step id>.happened}}` / `.timed_out`. This module turns those
 * outcomes into a synthetic FieldPicker entity so an If/Else can branch on
 * "did they open it?" without anyone typing a path by hand.
 *
 * This is deliberately more than a Yes/No fork: the outcome is a *value* on the
 * run, so it can be read by any condition downstream — the next step, or five
 * steps and two branches later.
 */

export const WAIT_OUTCOMES_KEY = '__wait_outcomes';

/**
 * The wait-for-event steps that are guaranteed to have run before `targetId`.
 *
 * Only steps on the target's own path qualify. A step in the sibling branch of a
 * fork, or anywhere below the target, has no log when the condition evaluates —
 * `resolvePath` returns nil and the rule fails closed, which reads to a user as
 * "my condition is broken" rather than "that step hasn't happened yet". Offering
 * only reachable outcomes is what stops that.
 */
export function waitStepsBefore(steps: WorkflowStep[], targetId: string): WorkflowStep[] {
  const acc: WorkflowStep[] = [];
  // The accumulator is only meaningful once the target has been reached. An id
  // that isn't in the tree (the global-conditions panel passes a sentinel) must
  // yield nothing — those conditions run before any step, so no wait has an
  // outcome yet.
  if (!walk(steps, targetId, acc)) return [];
  return acc;
}

function walk(list: WorkflowStep[], targetId: string, acc: WorkflowStep[]): boolean {
  for (const step of list) {
    if (step.id === targetId) return true;
    if (step.type === 'delay' && step.delay?.wait_event) {
      acc.push(step);
    }
    if (isForkStep(step)) {
      // A fork is terminal in its sibling list, so the target is either inside
      // one of its branches or not on this path at all. Unwind what the losing
      // branch contributed before trying the other one.
      const mark = acc.length;
      if (walk(step.yes_steps ?? [], targetId, acc)) return true;
      acc.length = mark;
      if (walk(step.no_steps ?? [], targetId, acc)) return true;
      acc.length = mark;
    }
  }
  return false;
}

/** Human label for a wait step, used to tell several waits apart in the picker. */
function outcomeLabel(step: WorkflowStep, index: number): string {
  const opened = step.delay?.wait_event === 'email_opened';
  const verb = opened ? 'Opened' : 'Clicked';
  return index === 0 ? verb : `${verb} (step ${index + 1})`;
}

/**
 * Build the "Wait outcomes" picker entity for the conditions reachable at
 * `targetId`. Returns null when there is nothing to offer, so callers can spread
 * the result without producing an empty category.
 */
export function waitOutcomeEntity(steps: WorkflowStep[], targetId: string | null): SchemaEntity | null {
  if (!targetId) return null;
  const waits = waitStepsBefore(steps, targetId);
  if (waits.length === 0) return null;
  return {
    key: WAIT_OUTCOMES_KEY,
    label: 'Wait outcomes',
    icon: '⏱️',
    fields: waits.map((step, i) => ({
      path: `actions.${step.id}.happened`,
      label: outcomeLabel(step, i),
      type: 'boolean' as const,
    })),
  };
}
