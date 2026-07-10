import { create } from 'zustand';
import type { TriggerSpec, ConditionGroup, ActionSpec, WorkflowStep, Workflow, SaveWorkflowPayload } from './types';
import { workflowSchema, validateActionIds, validateConditionDepth } from './schemas';
import { createWorkflow, updateWorkflow, getWorkflow, getWorkflowSchema, getObjectFields, type WorkflowSchema, type FieldItem } from './api';
import { isNoValueOperator } from './useSchema';
import { isValidCron } from './cron';
import { resolvableObjectsForTrigger, objectKeyOfPath, triggerOwnerObject } from './dateField';

/** The AI-draft shape the copilot applies (A7.3). Mirrors the /ai/draft response's
 *  `draft` object; steps are already id-normalized server-side. */
export interface WorkflowDraftInput {
  name?: string;
  description?: string;
  trigger: TriggerSpec;
  conditions?: ConditionGroup | null;
  steps: WorkflowStep[];
}

/** Snapshot of the fields an applied draft overwrites, for Undo. Captures isDirty
 *  too so undoing back to a freshly-loaded (clean) workflow doesn't strand a
 *  spurious dirty flag that re-enables Save for a no-op update. */
interface WorkflowDraftSnapshot {
  name: string;
  description: string;
  trigger: TriggerSpec | null;
  conditions: ConditionGroup | null;
  actions: ActionSpec[];
  steps: WorkflowStep[];
  isDirty: boolean;
}

interface BuilderState {
  workflowId: string | null;
  /** Creator user id of the loaded/created workflow; null for an unsaved draft.
   *  Used to gate the in-builder "Run Now" control (creator allowance). */
  createdBy: string | null;
  name: string;
  description: string;
  isActive: boolean;
  trigger: TriggerSpec | null;
  conditions: ConditionGroup | null;
  actions: ActionSpec[];
  steps: WorkflowStep[];
  selectedNodeId: string | null; // 'trigger' | 'conditions' | action.id | step.id
  isDirty: boolean;
  errors: Record<string, string[]>;
  saving: boolean;

  // AI copilot (A7.3): the pre-draft snapshot, non-null while an applied AI draft
  // is pending Keep/Undo. The canvas shows the draft; Undo restores this.
  draftSnapshot: WorkflowDraftSnapshot | null;

  // Schema (fetched once on builder mount)
  schema: WorkflowSchema | null;
  schemaLoading: boolean;
  schemaError: string | null;

  // Per-object field cache (session-scoped, refetched on Source change)
  fieldCache: Record<string, FieldItem[]>;
  fieldCacheLoading: string | null; // slug currently being fetched
  fieldCacheError: string | null;

  // Actions
  setName: (name: string) => void;
  setDescription: (desc: string) => void;
  setTrigger: (t: TriggerSpec) => void;
  setConditions: (c: ConditionGroup | null) => void;
  insertAction: (action: ActionSpec, index: number) => void;
  updateAction: (id: string, patch: Partial<ActionSpec>) => void;
  removeAction: (id: string) => void;
  reorderActions: (fromIdx: number, toIdx: number) => void;
  addStep: (step: WorkflowStep, parentId: string | null, branch: 'yes' | 'no' | null, index?: number) => void;
  updateStep: (id: string, patch: Partial<WorkflowStep>) => void;
  removeStep: (id: string) => void;
  reorderSteps: (parentId: string | null, branch: 'yes' | 'no' | null, fromIdx: number, toIdx: number) => void;
  selectNode: (id: string | null) => void;
  validate: () => boolean;
  save: () => Promise<void>;
  loadWorkflow: (id: string) => Promise<void>;
  duplicateFrom: (sourceId: string) => Promise<void>;
  /** Hydrate builder state from an already-fetched workflow (no network). Shared by
   *  the store's own loadWorkflow and the React Query load path in the new builder. */
  applyLoadedWorkflow: (wf: Workflow) => void;
  /** Apply an AI-generated draft (A7.3) into the current session: snapshots the
   *  current name/trigger/conditions/steps for Undo, then replaces them. Keeps the
   *  workflowId so a subsequent Save updates the same workflow. */
  applyDraft: (draft: WorkflowDraftInput) => void;
  /** Commit the applied draft (clear the Undo snapshot). */
  keepDraft: () => void;
  /** Discard the applied draft and restore the pre-draft snapshot. */
  undoDraft: () => void;
  /** Build the canonical steps-only save payload from current state. */
  buildSavePayload: () => SaveWorkflowPayload;
  /** Detach current state into a fresh unsaved "Copy of …" draft. */
  detachAsDuplicate: () => void;
  fetchSchema: () => Promise<void>;
  invalidateSchema: () => void;
  fetchObjectFields: (slug: string, forceRefresh?: boolean) => Promise<void>;
  findStep: (id: string) => WorkflowStep | undefined;
  reset: () => void;
}

function arrayMove<T>(arr: T[], from: number, to: number): T[] {
  const result = [...arr];
  const [item] = result.splice(from, 1);
  result.splice(to, 0, item);
  return result;
}

// ── Path-based tree navigation ───────────────────────────────────────

/**
 * A segment in a step path.
 * - The first segment has only `index` (position in root steps array).
 * - Subsequent segments have `branch` ('yes' | 'no') and `index`
 *   (position within that branch's child array).
 */
export interface StepPathSegment {
  branch?: 'yes' | 'no';
  index: number;
}

/** A full path from root to a specific step in the tree. */
export type StepPath = StepPathSegment[];

/**
 * Resolve a step from the tree using a path address.
 *
 * Path examples:
 * - `[{ index: 0 }]` → steps[0]
 * - `[{ index: 1 }, { branch: 'yes', index: 2 }]` → steps[1].yes_steps[2]
 * - `[{ index: 0 }, { branch: 'no', index: 0 }, { branch: 'yes', index: 1 }]`
 *   → steps[0].no_steps[0].yes_steps[1]
 *
 * Returns `undefined` if any segment is out of bounds or the branch doesn't exist.
 */
export function getStepAtPath(steps: WorkflowStep[], path: StepPath): WorkflowStep | undefined {
  if (path.length === 0) return undefined;

  const [head, ...rest] = path;

  // First segment: index into root array
  const step = steps[head.index];
  if (!step) return undefined;

  // If no more segments, this is the target
  if (rest.length === 0) return step;

  // Remaining segments navigate into branches
  const next = rest[0];
  if (!next.branch) return undefined;

  const children = next.branch === 'yes' ? step.yes_steps : step.no_steps;
  if (!children) return undefined;

  // Recurse: the remaining path is relative to the branch children array
  // Re-pack: next segment becomes the new "root" segment (strip branch, keep index)
  return getStepAtPath(children, rest.map((seg, i) =>
    i === 0 ? { index: seg.index } : seg
  ));
}

/**
 * Return the parent path by removing the last segment.
 *
 * - `[{ index: 0 }, { branch: 'yes', index: 1 }]` → `[{ index: 0 }]`
 * - `[{ index: 2 }]` → `[]` (root step → parent is the root array)
 * - `[]` → `undefined` (empty path has no parent)
 */
export function getParentPath(path: StepPath): StepPath | undefined {
  if (path.length === 0) return undefined;
  return path.slice(0, -1);
}

/**
 * Parse a backend action-path string (BuildStepPath format `idx(|branch|idx)*`,
 * e.g. "0" or "1|yes|2|no|0") into a StepPath. Returns null for an empty or
 * malformed path. Pairs with getStepAtPath for the A3.6 run-history → canvas
 * deep link (resolve a run's step log to its builder node).
 */
export function parseStepPath(path: string): StepPath | null {
  // Only bare decimal segments are valid indices — mirrors the backend's strconv.Atoi
  // (JS Number() would wrongly accept '', '1e2', '0x1', ' 3 ', so guard with a regex).
  const isIndex = (s: string | undefined): s is string => !!s && /^\d+$/.test(s);
  const parts = path.split('|');
  if (!isIndex(parts[0])) return null;
  const segs: StepPath = [{ index: Number(parts[0]) }];
  for (let i = 1; i < parts.length; i += 2) {
    const branch = parts[i];
    const idx = parts[i + 1];
    if ((branch !== 'yes' && branch !== 'no') || !isIndex(idx)) return null;
    segs.push({ branch, index: Number(idx) });
  }
  return segs;
}

/**
 * Check whether `childPath` is a descendant of `ancestorPath`.
 *
 * Returns `true` if `ancestorPath` is a strict prefix of `childPath`,
 * meaning the step at `childPath` lives somewhere inside the subtree
 * rooted at `ancestorPath`.
 *
 * Useful for **cycle detection** when moving steps: a step cannot be
 * moved into its own subtree.
 *
 * - `isDescendant([], [{ index: 0 }])` → false (empty = root array, not a step)
 * - `isDescendant([{ index: 0 }], [{ index: 0 }])` → false (same path, not a descendant)
 * - `isDescendant([{ index: 0 }], [{ index: 0 }, { branch: 'yes', index: 1 }])` → true
 */
export function isDescendant(ancestorPath: StepPath, childPath: StepPath): boolean {
  // A descendant must be strictly longer
  if (childPath.length <= ancestorPath.length) return false;
  // Empty ancestor path = root array, not a step node
  if (ancestorPath.length === 0) return false;

  for (let i = 0; i < ancestorPath.length; i++) {
    const a = ancestorPath[i];
    const c = childPath[i];
    if (a.index !== c.index) return false;
    if (a.branch !== c.branch) return false;
  }
  return true;
}

/** Maximum nesting depth for condition steps (must match backend MaxStepTreeDepth) */
export const MAX_STEP_TREE_DEPTH = 5;

/** Calculate the nesting depth of a parent step by ID. Root = 0, inside one condition = 1, etc. */
function getStepDepth(steps: WorkflowStep[], parentId: string | null): number {
  if (!parentId) return 0;

  function findDepth(list: WorkflowStep[], depth: number): number {
    for (const step of list) {
      if (step.id === parentId) return depth;
      if (step.type === 'condition') {
        if (step.yes_steps) {
          const d = findDepth(step.yes_steps, depth + 1);
          if (d >= 0) return d;
        }
        if (step.no_steps) {
          const d = findDepth(step.no_steps, depth + 1);
          if (d >= 0) return d;
        }
      }
    }
    return -1;
  }

  return Math.max(0, findDepth(steps, 0));
}

/** Calculate the max depth of a step subtree (condition nesting). */
function getSubtreeDepth(step: WorkflowStep): number {
  if (step.type !== 'condition') return 0;
  let maxChild = 0;
  for (const child of step.yes_steps || []) {
    maxChild = Math.max(maxChild, getSubtreeDepth(child));
  }
  for (const child of step.no_steps || []) {
    maxChild = Math.max(maxChild, getSubtreeDepth(child));
  }
  return 1 + maxChild;
}

function findStepInTree(steps: WorkflowStep[], id: string): WorkflowStep | undefined {
  for (const step of steps) {
    if (step.id === id) return step;
    if (step.yes_steps) {
      const found = findStepInTree(step.yes_steps, id);
      if (found) return found;
    }
    if (step.no_steps) {
      const found = findStepInTree(step.no_steps, id);
      if (found) return found;
    }
  }
  return undefined;
}

function findAndModifySteps(
  steps: WorkflowStep[],
  targetId: string,
  modifyFn: (step: WorkflowStep) => WorkflowStep | null
): WorkflowStep[] {
  const result: WorkflowStep[] = [];
  for (const step of steps) {
    if (step.id === targetId) {
      const modified = modifyFn(step);
      if (modified !== null) {
        result.push(modified);
      }
    } else {
      const newStep = { ...step };
      if (step.yes_steps) {
        newStep.yes_steps = findAndModifySteps(step.yes_steps, targetId, modifyFn);
      }
      if (step.no_steps) {
        newStep.no_steps = findAndModifySteps(step.no_steps, targetId, modifyFn);
      }
      result.push(newStep);
    }
  }
  return result;
}

function addStepToTree(
  steps: WorkflowStep[],
  parentId: string | null,
  branch: 'yes' | 'no' | null,
  newStep: WorkflowStep,
  index?: number
): WorkflowStep[] {
  if (parentId === null) {
    const result = [...steps];
    if (index !== undefined) {
      result.splice(index, 0, newStep);
    } else {
      result.push(newStep);
    }
    return result;
  }

  return steps.map((step) => {
    if (step.id === parentId) {
      const updated = { ...step };
      if (branch === 'yes') {
        const yesList = [...(step.yes_steps || [])];
        if (index !== undefined) {
          yesList.splice(index, 0, newStep);
        } else {
          yesList.push(newStep);
        }
        updated.yes_steps = yesList;
      } else if (branch === 'no') {
        const noList = [...(step.no_steps || [])];
        if (index !== undefined) {
          noList.splice(index, 0, newStep);
        } else {
          noList.push(newStep);
        }
        updated.no_steps = noList;
      }
      return updated;
    }

    const nextStep = { ...step };
    if (step.yes_steps) {
      nextStep.yes_steps = addStepToTree(step.yes_steps, parentId, branch, newStep, index);
    }
    if (step.no_steps) {
      nextStep.no_steps = addStepToTree(step.no_steps, parentId, branch, newStep, index);
    }
    return nextStep;
  });
}

function reorderStepsInTree(
  steps: WorkflowStep[],
  parentId: string | null,
  branch: 'yes' | 'no' | null,
  fromIdx: number,
  toIdx: number
): WorkflowStep[] {
  if (parentId === null) {
    return arrayMove(steps, fromIdx, toIdx);
  }

  return steps.map((step) => {
    if (step.id === parentId) {
      const updated = { ...step };
      if (branch === 'yes') {
        updated.yes_steps = arrayMove(step.yes_steps || [], fromIdx, toIdx);
      } else if (branch === 'no') {
        updated.no_steps = arrayMove(step.no_steps || [], fromIdx, toIdx);
      }
      return updated;
    }

    const nextStep = { ...step };
    if (step.yes_steps) {
      nextStep.yes_steps = reorderStepsInTree(step.yes_steps, parentId, branch, fromIdx, toIdx);
    }
    if (step.no_steps) {
      nextStep.no_steps = reorderStepsInTree(step.no_steps, parentId, branch, fromIdx, toIdx);
    }
    return nextStep;
  });
}

function flattenSteps(steps: WorkflowStep[]): ActionSpec[] {
  const result: ActionSpec[] = [];
  for (const step of steps) {
    if (step.type === 'action' && step.action) {
      result.push(step.action);
    } else if (step.type === 'delay') {
      const d = step.delay;
      const params: Record<string, unknown> = { duration_sec: d?.duration_sec ?? 0 };
      if (d?.until_field) {
        // Wait-until mode carries the field + offset/time/timezone (A4.4).
        params.until_field = d.until_field;
        params.offset_days = d.offset_days ?? 0;
        params.at_time = d.at_time ?? '';
        params.timezone = d.timezone ?? '';
      }
      result.push({ id: step.id, type: 'delay', params });
    }
    if (step.yes_steps) {
      result.push(...flattenSteps(step.yes_steps));
    }
    if (step.no_steps) {
      result.push(...flattenSteps(step.no_steps));
    }
  }
  return result;
}

function cleanSteps(steps: WorkflowStep[]): WorkflowStep[] {
  return steps.map((step) => {
    const cleaned = { ...step };
    if (step.type === 'action' && step.action) {
      const a = step.action;
      if (a.type === 'send_email') {
        const params = { ...a.params };
        const cc = typeof params.cc === 'string' ? params.cc.trim() : '';
        if (!cc) {
          delete params.cc;
        }
        cleaned.action = { ...a, params };
      } else if (a.type === 'update_record' || (a.type as string) === 'update_contact') {
        const params: Record<string, unknown> = {};
        if (Array.isArray(a.params.updates)) {
          params.updates = (a.params.updates as Array<Record<string, unknown>>).map((u) => {
            const clean: Record<string, unknown> = { field: u.field, op: u.op };
            if (u.op !== 'clear' && u.value !== undefined && u.value !== null) {
              clean.value = u.value;
            }
            return clean;
          });
        }
        cleaned.action = { ...a, params };
      }
    }
    if (step.yes_steps) {
      cleaned.yes_steps = cleanSteps(step.yes_steps);
    }
    if (step.no_steps) {
      cleaned.no_steps = cleanSteps(step.no_steps);
    }
    return cleaned;
  });
}

let idCounter = 0;
export function generateActionId(): string {
  return `action_${Date.now()}_${++idCounter}`;
}

/** Extract object slug from a trigger type string (e.g. 'contact_created' → 'contact') */
function extractObjectSlug(type: string): string {
  if (type === 'deal_stage_changed') return 'deal';
  if (type === 'no_activity_days') return 'contact';
  if (type === 'webhook_inbound') return 'webhook';
  for (const suffix of ['_created', '_updated', '_deleted', '_any']) {
    if (type.endsWith(suffix)) return type.slice(0, -suffix.length);
  }
  return '';
}

/** Coerce an untrusted conditions value into a well-formed ConditionGroup or null.
 *  An AI draft is applied even when the backend flags it invalid (the canvas + zod
 *  are the final gate), and the backend passes `conditions` through as raw JSON, so
 *  a model can emit a rules-less `{op:'AND'}` (or a non-object). Left verbatim it
 *  would crash the condition config panel and the save-time rule loop, both of which
 *  dereference `.rules`. Null means "no conditions" (the canonical empty state). */
function normalizeConditionGroup(c: unknown): ConditionGroup | null {
  if (!c || typeof c !== 'object') return null;
  const g = c as Partial<ConditionGroup>;
  if (!Array.isArray(g.rules) || g.rules.length === 0) return null;
  return { op: g.op === 'OR' ? 'OR' : 'AND', rules: g.rules };
}

const initialState = {
  workflowId: null as string | null,
  createdBy: null as string | null,
  name: '',
  description: '',
  isActive: false,
  trigger: null as TriggerSpec | null,
  conditions: null as ConditionGroup | null,
  actions: [] as ActionSpec[],
  steps: [] as WorkflowStep[],
  selectedNodeId: null as string | null,
  isDirty: false,
  errors: {} as Record<string, string[]>,
  saving: false,
  draftSnapshot: null as WorkflowDraftSnapshot | null,
  schema: null as WorkflowSchema | null,
  schemaLoading: false,
  schemaError: null as string | null,
  fieldCache: {} as Record<string, FieldItem[]>,
  fieldCacheLoading: null as string | null,
  fieldCacheError: null as string | null,
};

// Singleton promise so concurrent fetchSchema() calls share one request
let schemaFetchPromise: Promise<WorkflowSchema> | null = null;
// Per-slug singleton promises so concurrent fetchObjectFields() calls share one request
let fieldFetchPromises: Record<string, Promise<FieldItem[]>> = {};

export const useBuilderStore = create<BuilderState>((set, get) => ({
  ...initialState,

  setName: (name) => set({ name, isDirty: true }),
  setDescription: (description) => set({ description, isDirty: true }),

  setTrigger: (trigger) => {
    const prev = get().trigger;
    const updates: Partial<BuilderState> = { trigger, isDirty: true };

    // If the source object changed, clear stale condition rules (field paths no longer valid).
    // A non-object trigger (schedule) has an empty slug, so leaving/entering it also drops
    // object-scoped conditions that could no longer resolve.
    const prevSlug = prev ? extractObjectSlug(prev.type) : '';
    const newSlug = extractObjectSlug(trigger.type);

    if (prev && prevSlug && prevSlug !== newSlug) {
      updates.conditions = null;
    }

    set(updates);

    // Auto-fetch fields for the new object (uses cache if available)
    if (newSlug && newSlug !== 'webhook') {
      get().fetchObjectFields(newSlug);
    }
  },

  setConditions: (conditions) => set({ conditions, isDirty: true }),

  insertAction: (action, index) => {
    const step: WorkflowStep = {
      id: action.id,
      type: action.type === 'delay' ? 'delay' : 'action',
      action: action.type === 'delay' ? undefined : action,
      delay: action.type === 'delay' ? { duration_sec: Number(action.params?.duration_sec) || 60 } : undefined,
    };
    get().addStep(step, null, null, index);
  },

  updateAction: (id, patch) => {
    get().updateStep(id, { action: patch as ActionSpec });
  },

  removeAction: (id) => {
    get().removeStep(id);
  },

  reorderActions: (fromIdx, toIdx) => {
    get().reorderSteps(null, null, fromIdx, toIdx);
  },

  addStep: (step, parentId, branch, index) =>
    set((s) => {
      // Depth guard: check if adding this step would exceed max depth
      if (parentId && branch) {
        const parentDepth = getStepDepth(s.steps || [], parentId);
        const subtreeDepth = getSubtreeDepth(step);
        // parentDepth = how deep the parent is. Adding into a branch adds 1.
        // If the step itself is a condition tree, add its subtree depth.
        if (parentDepth + 1 + subtreeDepth > MAX_STEP_TREE_DEPTH) {
          return {
            errors: {
              ...s.errors,
              depth: [`Cannot add step: nesting would exceed maximum depth of ${MAX_STEP_TREE_DEPTH} levels`],
            },
          };
        }
      }
      const steps = addStepToTree(s.steps || [], parentId, branch, step, index);
      const actions = flattenSteps(steps);
      return { steps, actions, isDirty: true };
    }),

  updateStep: (id, patch) =>
    set((s) => {
      const steps = findAndModifySteps(s.steps || [], id, (step) => {
        if (step.type === 'delay' && patch.action) {
          const dp = patch.action.params as Record<string, unknown> | undefined;
          const nextDelay: import('./types').DelayParams = { ...(step.delay || { duration_sec: 60 }) };
          if (dp?.duration_sec !== undefined) nextDelay.duration_sec = Number(dp.duration_sec);
          // Wait-until fields (A4.4). Empty until_field clears wait-until mode back to a fixed delay.
          if (dp?.until_field !== undefined) nextDelay.until_field = (dp.until_field as string) || undefined;
          if (dp?.offset_days !== undefined) nextDelay.offset_days = Number(dp.offset_days);
          if (dp?.at_time !== undefined) nextDelay.at_time = dp.at_time as string;
          if (dp?.timezone !== undefined) nextDelay.timezone = dp.timezone as string;
          return { ...step, delay: nextDelay };
        }
        if (step.type === 'action' && step.action && patch.action) {
          return {
            ...step,
            ...patch,
            action: {
              ...step.action,
              ...patch.action,
              params: { ...step.action.params, ...(patch.action.params || {}) },
            },
          };
        }
        return { ...step, ...patch };
      });
      const actions = flattenSteps(steps);
      return { steps, actions, isDirty: true };
    }),

  removeStep: (id) =>
    set((s) => {
      const steps = findAndModifySteps(s.steps || [], id, () => null);
      const actions = flattenSteps(steps);
      // Clear selection if the removed step (or any cascade-removed child) was selected
      const selId = s.selectedNodeId;
      const selStillExists = selId ? !!findStepInTree(steps, selId) : false;
      return {
        steps,
        actions,
        selectedNodeId: selStillExists ? selId : null,
        isDirty: true,
      };
    }),

  reorderSteps: (parentId, branch, fromIdx, toIdx) =>
    set((s) => {
      const steps = reorderStepsInTree(s.steps || [], parentId, branch, fromIdx, toIdx);
      const actions = flattenSteps(steps);
      return { steps, actions, isDirty: true };
    }),

  selectNode: (id) => set({ selectedNodeId: id }),

  validate: () => {
    const state = get();
    const errors: Record<string, string[]> = {};

    if (!state.name.trim()) {
      errors.name = ['Name is required'];
    }

    if (!state.trigger) {
      errors.trigger = ['Source is required'];
      errors['trigger.object'] = ['Select a source object'];
    } else if (state.trigger.type === 'schedule') {
      // Schedule is not object-based: validate the cron (+ optional timezone) instead
      // of an object/fires-on. The backend (robfig/cron) is the authoritative parser.
      const cron = (state.trigger.params?.cron as string) || '';
      if (!cron.trim()) {
        errors['trigger.params.cron'] = ['Enter a schedule'];
        errors.trigger = ['Schedule is required'];
      } else if (!isValidCron(cron)) {
        errors['trigger.params.cron'] = ['Invalid cron expression'];
        errors.trigger = ['Schedule cron is invalid'];
      }
      const tz = state.trigger.params?.timezone;
      if (tz !== undefined && typeof tz !== 'string') {
        errors['trigger.params.timezone'] = ['Invalid timezone'];
      }
    } else if (state.trigger.type === 'date_field') {
      // date_field is object-based but not an event trigger: require an object + a
      // date field. The backend re-validates offset/time/timezone.
      const object = (state.trigger.params?.object as string) || '';
      const field = (state.trigger.params?.field as string) || '';
      if (!object) {
        errors['trigger.params.object'] = ['Select an object'];
        errors.trigger = ['Source object is required'];
      }
      if (!field) {
        errors['trigger.params.field'] = ['Select a date field'];
        if (!errors.trigger) errors.trigger = [];
        errors.trigger.push('Date field is required');
      }
    } else {
      const slug = extractObjectSlug(state.trigger.type);
      if (!slug) {
        errors['trigger.object'] = ['Select a source object'];
        if (!errors.trigger) errors.trigger = [];
        errors.trigger.push('Source object is missing');
      }
      // Fires-on is always set when object is set (default = created),
      // but validate the trigger type is well-formed
      const t = state.trigger.type;
      const hasValidEvent = t === 'webhook_inbound' || t === 'deal_stage_changed' || t === 'no_activity_days'
        || t.endsWith('_created') || t.endsWith('_updated') || t.endsWith('_deleted') || t.endsWith('_any');
      if (slug && !hasValidEvent) {
        errors['trigger.firesOn'] = ['Select a fires-on event'];
        if (!errors.trigger) errors.trigger = [];
        errors.trigger.push('Fires-on event is missing');
      }

      // deal_stage_changed requires to_stage param
      if (t === 'deal_stage_changed') {
        const toStage = state.trigger.params?.to_stage;
        if (!toStage || toStage === '') {
          errors['trigger.params.to_stage'] = ['Select a target stage'];
          if (!errors.trigger) errors.trigger = [];
          errors.trigger.push('Target stage is required for deal stage change trigger');
        }
      }
    }

    if (state.steps.length === 0) {
      errors.steps = ['At least one action or condition is required'];
    }

    // Validate with zod
    const result = workflowSchema.safeParse({
      name: state.name,
      description: state.description,
      trigger: state.trigger,
      conditions: state.conditions,
      actions: state.actions,
      steps: state.steps,
    });

    if (!result.success) {
      for (const issue of result.error.issues) {
        const path = issue.path.join('.');
        if (!errors[path]) errors[path] = [];
        errors[path].push(issue.message);
      }
    }

    // Check duplicate action IDs
    const dupeErrors = validateActionIds(state.actions);
    if (dupeErrors.length) {
      errors.actions = [...(errors.actions || []), ...dupeErrors];
    }

    // Check condition depth. Guard on rules being an array so a malformed group
    // (e.g. a rules-less object from a legacy row) can't crash the rule loop below.
    if (state.conditions && Array.isArray(state.conditions.rules)) {
      const depthErrors = validateConditionDepth(state.conditions);
      if (depthErrors.length) {
        errors.conditions = [...(errors.conditions || []), ...depthErrors];
      }

      // Validate condition rules: block save if field+operator set but value missing
      // Also flag orphaned fields (edge case 3: permission downgrade)
      const slug = state.trigger ? extractObjectSlug(state.trigger.type) : '';
      const allEntities = state.schema
        ? [...state.schema.entities, ...(state.schema.custom_objects || [])]
        : [];
      const entity = allEntities.find((e) => e.key === slug);
      const validFieldPaths = new Set(entity?.fields.map((f) => f.path) || []);

      for (let i = 0; i < state.conditions.rules.length; i++) {
        const rule = state.conditions.rules[i];

        // Orphaned field check
        if (rule.field && validFieldPaths.size > 0 && !validFieldPaths.has(rule.field)) {
          errors[`conditions.rules.${i}.field`] = [`Field "${rule.field}" is no longer accessible`];
          if (!errors.conditions) errors.conditions = [];
          errors.conditions.push(`Rule ${i + 1}: field no longer accessible`);
        }

        // Missing value check
        if (rule.field && rule.operator && !isNoValueOperator(rule.operator)) {
          const isEmpty = rule.value === null || rule.value === undefined || rule.value === '';
          if (isEmpty) {
            errors[`conditions.rules.${i}.value`] = ['Value is required for this operator'];
            if (!errors.conditions) errors.conditions = [];
            errors.conditions.push(`Rule ${i + 1}: value is required`);
          }
        }
      }
    }

    // Edge case 3: trigger object permission downgrade
    if (state.trigger && state.schema) {
      const slug = extractObjectSlug(state.trigger.type);
      if (slug && slug !== 'webhook') {
        const allEntities = [...state.schema.entities, ...(state.schema.custom_objects || [])];
        const objectExists = allEntities.some((e) => e.key === slug);
        if (!objectExists) {
          errors['trigger.object'] = [`Object "${slug}" is no longer accessible`];
          if (!errors.trigger) errors.trigger = [];
          errors.trigger.push('Source object is no longer accessible');
        }
      }
    }

    // Validate condition steps inside the step tree — flag empty conditions
    function validateStepConditions(steps: WorkflowStep[], pathPrefix: string) {
      for (let i = 0; i < steps.length; i++) {
        const s = steps[i];
        if (s.type === 'condition') {
          const rules = s.condition?.rules ?? [];
          const configured = rules.filter((r) => r.field && r.field !== '');
          if (configured.length === 0) {
            const key = `step.${s.id}`;
            errors[key] = ['Configure at least one condition rule with a field'];
          }
          // Recurse into branches
          if (s.yes_steps) validateStepConditions(s.yes_steps, `${pathPrefix}${i}.yes_steps.`);
          if (s.no_steps) validateStepConditions(s.no_steps, `${pathPrefix}${i}.no_steps.`);
        }
      }
    }
    if (state.steps && state.steps.length > 0) {
      validateStepConditions(state.steps, 'steps.');
    }

    // Validate email addresses in send_email actions
    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    const templateRegex = /\{\{.+?\}\}/;
    for (let i = 0; i < state.actions.length; i++) {
      const action = state.actions[i];
      const key = `actions.${i}`;

      if (action.type === 'send_email') {
        // Validate "to"
        const to = String(action.params.to || '').trim();
        if (to && !emailRegex.test(to) && !templateRegex.test(to)) {
          errors[`${key}.params.to`] = ['Must be a valid email address or {{template}}'];
        }

        // Validate "cc" — comma-separated, each part must be email or template
        const cc = String(action.params.cc || '').trim();
        if (cc) {
          const parts = cc.split(',').map((p: string) => p.trim()).filter(Boolean);
          const invalid = parts.filter((p: string) => !emailRegex.test(p) && !templateRegex.test(p));
          if (invalid.length > 0) {
            errors[`${key}.params.cc`] = [`Invalid CC address: ${invalid.join(', ')}`];
          }
        }
      }

      if (action.type === 'assign_user') {
        const strategy = String(action.params.strategy || '');
        if (strategy === 'specific' && !action.params.user_id) {
          errors[`${key}.params.user_id`] = ['Select a user to assign'];
        }
        if (strategy === 'round_robin') {
          const pool = Array.isArray(action.params.pool) ? action.params.pool : [];
          if (pool.length === 0) {
            errors[`${key}.params.pool`] = ['Select at least one user for round robin pool'];
          }
        }
      }

      if (action.type === 'delay') {
        const untilField = typeof action.params.until_field === 'string' ? action.params.until_field : '';
        if (untilField) {
          // Wait-until mode (A4.4): a field is required; offset/time/timezone default.
          // No 30-day cap — a field-based wait can be months out.
          // Guard against a field the run's eval context can't resolve (a deal field
          // on a contact-triggered workflow, or a field left stale after the trigger
          // changed): the backend would silently skip the wait instead of erroring.
          const resolvable = resolvableObjectsForTrigger(state.trigger);
          if (!resolvable.has(objectKeyOfPath(untilField))) {
            errors[`${key}.params.until_field`] = [
              "This date field isn't available for the current trigger — pick a field from the trigger's record",
            ];
          }
        } else {
          const sec = Number(action.params.duration_sec) || 0;
          if (sec <= 0) {
            errors[`${key}.params.duration_sec`] = ['Duration must be a positive number'];
          } else if (sec > 2592000) {
            errors[`${key}.params.duration_sec`] = ['Duration exceeds maximum of 30 days (2,592,000 seconds)'];
          }
        }
      }

      if (action.type === 'update_record' || (action.type as string) === 'update_contact') {
        const updates = Array.isArray(action.params.updates)
          ? (action.params.updates as Array<{ field?: string; op?: string; value?: unknown }>)
          : [];
        if (updates.length === 0) {
          errors[`${key}.params.updates`] = ['Add at least one field update'];
        } else {
          updates.forEach((upd, idx) => {
            const uKey = `${key}.params.updates[${idx}]`;
            if (!upd.field) {
              errors[`${uKey}.field`] = ['Select a contact field'];
            }
            if (!upd.op) {
              errors[`${uKey}.op`] = ['Select an operation'];
            }
            if (upd.op && upd.op !== 'clear' && (upd.value === undefined || upd.value === null || upd.value === '')) {
              errors[`${uKey}.value`] = ['Provide a value for this operation'];
            }
          });
        }
      }

      if (action.type === 'notify_user') {
        const title = String(action.params.title || '').trim();
        if (!title) {
          errors[`${key}.params.title`] = ['Title is required'];
        }
        const recipient = String(action.params.recipient || 'owner_field');
        if (recipient === 'specific') {
          if (!action.params.user_id) {
            errors[`${key}.params.user_id`] = ['Select a user to notify'];
          }
        } else if (!triggerOwnerObject(state.trigger)) {
          // owner_field mode but the trigger's record has no owner (schedule /
          // company / custom) → the run can't resolve a recipient. Mirror the
          // wait-until guard: reject at save so it isn't a silent runtime failure.
          errors[`${key}.params.recipient`] = ["This trigger has no record owner — choose a specific user"];
        }
      }

      if (action.type === 'create_record') {
        if (!String(action.params.object || '').trim()) {
          errors[`${key}.params.object`] = ['Choose an object to create'];
        }
        const rows = Array.isArray(action.params.fields)
          ? (action.params.fields as Array<{ field?: string; value?: unknown }>)
          : [];
        const named = rows.filter((r) => String(r.field || '').trim());
        if (named.length === 0) {
          errors[`${key}.params.fields`] = ['Add at least one field'];
        }
      }

      if (action.type === 'find_records') {
        if (!String(action.params.object || '').trim()) {
          errors[`${key}.params.object`] = ['Choose an object to find'];
        }
      }

      if (action.type === 'enroll_records') {
        if (!String(action.params.object || '').trim()) {
          errors[`${key}.params.object`] = ['Choose an object to enroll'];
        }
        if (!String(action.params.workflow_id || '').trim()) {
          errors[`${key}.params.workflow_id`] = ['Choose a workflow to enroll into'];
        }
      }

      if (action.type === 'ai_generate') {
        if (!String(action.params.prompt || '').trim()) {
          errors[`${key}.params.prompt`] = ['Write a prompt for the AI'];
        }
        if (action.params.max_tokens !== undefined) {
          const n = Number(action.params.max_tokens);
          if (!Number.isFinite(n) || n < 1 || n > 1024) {
            errors[`${key}.params.max_tokens`] = ['Max length must be between 1 and 1024'];
          }
        }
      }
    }

    set({ errors });
    return Object.keys(errors).length === 0;
  },

  // Sanitize trigger (strip UI-only _fieldMeta) + emit steps-only payload (A1:
  // the server derives the deprecated flat actions; cleanSteps applies the
  // send_email/update_record param sanitization the flat list used to get).
  buildSavePayload: () => {
    const state = get();
    const cleanedTrigger = { ...state.trigger! };
    if (cleanedTrigger.params) {
      const { _fieldMeta, ...triggerParams } = cleanedTrigger.params as Record<string, unknown>;
      cleanedTrigger.params = Object.keys(triggerParams).length > 0 ? triggerParams : undefined;
    }
    return {
      name: state.name,
      description: state.description,
      trigger: cleanedTrigger,
      conditions: state.conditions,
      steps: cleanSteps(state.steps || []),
    };
  },

  save: async () => {
    const state = get();
    if (!state.validate()) return;

    set({ saving: true });
    try {
      const payload = get().buildSavePayload();
      if (state.workflowId) {
        await updateWorkflow(state.workflowId, payload);
      } else {
        const wf = await createWorkflow(payload);
        set({ workflowId: wf.id, createdBy: wf.created_by ?? null });
      }
      set({ isDirty: false });
    } finally {
      set({ saving: false });
    }
  },

  applyLoadedWorkflow: (wf) => {
    const loadedSteps = wf.steps && wf.steps.length > 0
      ? wf.steps
      : (wf.actions || []).map((a) => ({
          id: a.id,
          type: a.type === 'delay' ? 'delay' : 'action',
          action: a.type === 'delay' ? undefined : a,
          params: a.type === 'delay' ? a.params : undefined,
        } as WorkflowStep));
    set({
      workflowId: wf.id,
      createdBy: wf.created_by ?? null,
      name: wf.name,
      description: wf.description,
      isActive: wf.is_active,
      trigger: wf.trigger,
      conditions: wf.conditions,
      actions: wf.actions || [],
      steps: loadedSteps,
      isDirty: false,
      errors: {},
      selectedNodeId: null,
      draftSnapshot: null,
    });
  },

  applyDraft: (draft) => {
    const state = get();
    // Preserve the true pre-draft baseline across successive regenerations: only
    // capture a snapshot when none is pending. A second Generate before Keep/Undo
    // must still Undo back to the user's original state, not the first AI draft.
    const snapshot: WorkflowDraftSnapshot = state.draftSnapshot ?? {
      name: state.name,
      description: state.description,
      trigger: state.trigger,
      conditions: state.conditions,
      actions: state.actions,
      steps: state.steps,
      isDirty: state.isDirty,
    };
    const steps = (draft.steps || []) as WorkflowStep[];
    set({
      draftSnapshot: snapshot,
      name: draft.name || state.name,
      description: draft.description ?? state.description,
      trigger: draft.trigger ?? null,
      // The draft is applied even when validation flagged it, and the backend
      // relays conditions as raw JSON — coerce to a safe shape (see normalizeConditionGroup).
      conditions: normalizeConditionGroup(draft.conditions),
      steps,
      actions: flattenSteps(steps),
      isDirty: true,
      errors: {},
      selectedNodeId: null,
    });
  },

  keepDraft: () => set({ draftSnapshot: null }),

  undoDraft: () => {
    const snap = get().draftSnapshot;
    if (!snap) return;
    set({
      name: snap.name,
      description: snap.description,
      trigger: snap.trigger,
      conditions: snap.conditions,
      actions: snap.actions,
      steps: snap.steps,
      draftSnapshot: null,
      isDirty: snap.isDirty,
      errors: {},
      selectedNodeId: null,
    });
  },

  loadWorkflow: async (id) => {
    const wf = await getWorkflow(id);
    get().applyLoadedWorkflow(wf);
  },

  // Detach current state into a fresh, unsaved draft (P23). Nulling
  // workflowId/createdBy makes the next save() create a NEW workflow instead of
  // overwriting the original; the copy starts inactive so a clone never auto-fires,
  // and is marked dirty so the builder shows unsaved state.
  detachAsDuplicate: () =>
    set((s) => ({
      workflowId: null,
      createdBy: null,
      name: `Copy of ${s.name}`,
      isActive: false,
      isDirty: true,
    })),

  duplicateFrom: async (sourceId) => {
    await get().loadWorkflow(sourceId);
    get().detachAsDuplicate();
  },

  fetchSchema: async () => {
    // Already loaded → skip
    if (get().schema) return;

    set({ schemaLoading: true, schemaError: null });
    try {
      // Deduplicate concurrent fetches with a singleton promise
      if (!schemaFetchPromise) {
        schemaFetchPromise = getWorkflowSchema();
      }
      const data = await schemaFetchPromise;
      set({ schema: data });
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to load schema';
      console.error('Failed to load workflow schema:', err);
      set({ schemaError: message });
    } finally {
      set({ schemaLoading: false });
      schemaFetchPromise = null;
    }
  },

  invalidateSchema: () => {
    // Clear cached schema AND field cache, then re-fetch from server.
    // Call this after settings mutations (tags, stages, custom fields, custom objects).
    schemaFetchPromise = null;
    fieldFetchPromises = {};
    set({ schema: null, schemaError: null, fieldCache: {}, fieldCacheLoading: null, fieldCacheError: null });
    get().fetchSchema();
  },

  fetchObjectFields: async (slug, forceRefresh = false) => {
    // Return cached fields if available (session cache)
    const cache = get().fieldCache;
    if (!forceRefresh && cache[slug]) return;

    set({ fieldCacheLoading: slug, fieldCacheError: null });
    try {
      // Deduplicate concurrent fetches per slug
      if (!fieldFetchPromises[slug]) {
        fieldFetchPromises[slug] = getObjectFields(slug);
      }
      const fields = await fieldFetchPromises[slug];
      set({
        fieldCache: { ...get().fieldCache, [slug]: fields },
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to load fields';
      console.error(`Failed to load fields for ${slug}:`, err);
      set({ fieldCacheError: message });
    } finally {
      set({ fieldCacheLoading: null });
      delete fieldFetchPromises[slug];
    }
  },

  findStep: (id) => findStepInTree(get().steps || [], id),

  reset: () => {
    // Preserve schema and fieldCache across resets — they don't change when navigating between workflows
    const { schema, schemaError, fieldCache } = get();
    set({ ...initialState, schema, schemaError, fieldCache, errors: {} });
  },
}));

// The steps↔actions subscribe back-sync was removed in overhaul A1: steps are
// the canonical format (the server derives the deprecated flat list itself).
// The in-memory `actions` view is still maintained by the tree mutations via
// flattenSteps because the builder's ActionConfig resolves the selected action
// from it; code that seeds state directly must set `steps` (loadWorkflow maps
// legacy actions-only workflows explicitly).


