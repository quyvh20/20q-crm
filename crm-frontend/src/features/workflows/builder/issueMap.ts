// Maps the store's validate() error record onto canvas coordinates: which STEP
// each issue belongs to, which belong to the trigger, and which are global
// (name, empty workflow, …). This is what lets the canvas badge the exact nodes
// that block a save — competitor builders (Brevo et al.) validate per-form and
// leave you hunting; we point at the node.
//
// Error-key conventions produced by store.validate():
//   step.<id>            — already id-keyed (condition rules, no-merge backstop)
//   actions.<i>[...]     — flat action index; actions[i].id === the step's id
//   steps.<i>(.yes_steps|no_steps.<i>)*[...] — zod path into the steps tree
//   trigger / trigger.*  — the trigger node
//   anything else        — global (name, steps-empty, conditions, dupe ids)

import type { WorkflowStep, ActionSpec } from '../types';

export interface CanvasIssues {
  byStep: Record<string, string[]>;
  trigger: string[];
  global: string[];
  /** Total number of distinct messages across all buckets. */
  count: number;
}

const EMPTY: CanvasIssues = { byStep: {}, trigger: [], global: [], count: 0 };

export function mapIssuesToCanvas(
  errors: Record<string, string[]>,
  steps: WorkflowStep[],
  actions: ActionSpec[],
): CanvasIssues {
  const keys = Object.keys(errors);
  if (keys.length === 0) return EMPTY;

  const byStep: Record<string, Set<string>> = {};
  const trigger = new Set<string>();
  const global = new Set<string>();
  const addStepMsgs = (id: string, msgs: string[]) => {
    const set = (byStep[id] ??= new Set());
    for (const m of msgs) set.add(m);
  };

  for (const key of keys) {
    const msgs = errors[key];
    if (!msgs?.length) continue;
    if (key === 'trigger' || key.startsWith('trigger.')) {
      for (const m of msgs) trigger.add(m);
      continue;
    }
    const stepKey = key.match(/^step\.(.+)$/);
    if (stepKey) {
      addStepMsgs(stepKey[1], msgs);
      continue;
    }
    const actionKey = key.match(/^actions\.(\d+)(?:\.|$)/);
    if (actionKey) {
      const action = actions[Number(actionKey[1])];
      if (action?.id) {
        addStepMsgs(action.id, msgs);
        continue;
      }
    }
    if (/^steps\.\d+/.test(key)) {
      const id = resolveStepPathKey(steps, key);
      if (id) {
        addStepMsgs(id, msgs);
        continue;
      }
    }
    for (const m of msgs) global.add(m);
  }

  const byStepOut: Record<string, string[]> = {};
  let count = trigger.size + global.size;
  for (const [id, set] of Object.entries(byStep)) {
    byStepOut[id] = [...set];
    count += set.size;
  }
  return { byStep: byStepOut, trigger: [...trigger], global: [...global], count };
}

/** Resolve a zod-style `steps.0.yes_steps.1....` error key to the step it points into. */
function resolveStepPathKey(steps: WorkflowStep[], key: string): string | null {
  const parts = key.split('.');
  let list = steps;
  let step: WorkflowStep | undefined;
  let i = 1; // parts[0] === 'steps'
  while (i < parts.length) {
    const idx = Number(parts[i]);
    if (!Number.isInteger(idx) || idx < 0) break;
    step = list[idx];
    if (!step) return null;
    i++;
    const branch = parts[i];
    if (branch === 'yes_steps' || branch === 'no_steps') {
      list = (branch === 'yes_steps' ? step.yes_steps : step.no_steps) ?? [];
      i++;
      continue;
    }
    break;
  }
  return step?.id ?? null;
}
