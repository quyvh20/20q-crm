import { create } from 'zustand';
import type { TriggerSpec, ConditionGroup, ActionSpec, WorkflowStep } from './types';
import { workflowSchema, validateActionIds, validateConditionDepth } from './schemas';
import { createWorkflow, updateWorkflow, getWorkflow, getWorkflowSchema, getObjectFields, type WorkflowSchema, type FieldItem } from './api';
import { isNoValueOperator } from './useSchema';

interface BuilderState {
  workflowId: string | null;
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
      result.push({
        id: step.id,
        type: 'delay',
        params: step.delay ? { duration_sec: step.delay.duration_sec } : {},
      });
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

const initialState = {
  workflowId: null as string | null,
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

    // If the source object changed, clear stale condition rules (field paths no longer valid)
    const prevSlug = prev ? extractObjectSlug(prev.type) : '';
    const newSlug = extractObjectSlug(trigger.type);

    if (prev && prevSlug && newSlug && prevSlug !== newSlug) {
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
      const steps = addStepToTree(s.steps || [], parentId, branch, step, index);
      const actions = flattenSteps(steps);
      return { steps, actions, isDirty: true };
    }),

  updateStep: (id, patch) =>
    set((s) => {
      const steps = findAndModifySteps(s.steps || [], id, (step) => {
        if (step.type === 'delay' && patch.action) {
          const delayParams = patch.action.params as any;
          return {
            ...step,
            delay: {
              ...(step.delay || { duration_sec: 60 }),
              ...(delayParams?.duration_sec !== undefined ? { duration_sec: Number(delayParams.duration_sec) } : {}),
            },
          };
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

    // Check condition depth
    if (state.conditions) {
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
        const sec = Number(action.params.duration_sec) || 0;
        if (sec <= 0) {
          errors[`${key}.params.duration_sec`] = ['Duration must be a positive number'];
        } else if (sec > 2592000) {
          errors[`${key}.params.duration_sec`] = ['Duration exceeds maximum of 30 days (2,592,000 seconds)'];
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
    }

    set({ errors });
    return Object.keys(errors).length === 0;
  },

  save: async () => {
    const state = get();
    if (!state.validate()) return;

    set({ saving: true });
    try {
      // Sanitize: strip empty CC strings → omit key entirely; clean update_record params
      const cleanedActions = state.actions.map((a) => {
        if (a.type === 'send_email') {
          const params = { ...a.params };
          const cc = typeof params.cc === 'string' ? params.cc.trim() : '';
          if (!cc) {
            delete params.cc;
          }
          return { ...a, params };
        }
        if (a.type === 'update_record' || (a.type as string) === 'update_contact') {
          // Ensure only 'updates' key is emitted, strip legacy flat keys
          const params: Record<string, unknown> = {};
          if (Array.isArray(a.params.updates)) {
            // Strip undefined values from each entry
            params.updates = (a.params.updates as Array<Record<string, unknown>>).map((u) => {
              const clean: Record<string, unknown> = { field: u.field, op: u.op };
              if (u.op !== 'clear' && u.value !== undefined && u.value !== null) {
                clean.value = u.value;
              }
              return clean;
            });
          }
          return { ...a, params };
        }
        return a;
      });

      // Sanitize trigger: strip UI-only _fieldMeta cache from params
      const cleanedTrigger = { ...state.trigger! };
      if (cleanedTrigger.params) {
        const { _fieldMeta, ...triggerParams } = cleanedTrigger.params as Record<string, unknown>;
        cleanedTrigger.params = Object.keys(triggerParams).length > 0 ? triggerParams : undefined;
      }

      const payload = {
        name: state.name,
        description: state.description,
        trigger: cleanedTrigger,
        conditions: state.conditions,
        actions: cleanedActions,
        steps: cleanSteps(state.steps || []),
      };

      if (state.workflowId) {
        await updateWorkflow(state.workflowId, payload);
      } else {
        const wf = await createWorkflow(payload);
        set({ workflowId: wf.id });
      }
      set({ isDirty: false });
    } finally {
      set({ saving: false });
    }
  },

  loadWorkflow: async (id) => {
    const wf = await getWorkflow(id);
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
    });
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

// Sync steps and actions automatically if either is updated directly (e.g. in tests via setState)
let _syncing = false;
useBuilderStore.subscribe((state, prevState) => {
  if (_syncing) return;
  if (state.actions === prevState?.actions && state.steps === prevState?.steps) return;

  _syncing = true;
  try {
    // 1. If actions was populated directly but steps is empty:
    if (state.actions && state.actions.length > 0 && (!state.steps || state.steps.length === 0)) {
      const steps = state.actions.map(
        (a) =>
          ({
            id: a.id,
            type: a.type === 'delay' ? 'delay' : 'action',
            action: a.type === 'delay' ? undefined : a,
            delay: a.type === 'delay' ? { duration_sec: Number(a.params?.duration_sec) || 60 } : undefined,
          } as WorkflowStep)
      );
      useBuilderStore.setState({ steps });
    }

    // 2. If steps was populated directly but actions is empty:
    if (state.steps && state.steps.length > 0 && (!state.actions || state.actions.length === 0)) {
      const actions = flattenSteps(state.steps);
      useBuilderStore.setState({ actions });
    }
  } finally {
    _syncing = false;
  }
});


