import { create } from 'zustand';
import type { TriggerSpec, ConditionGroup, ActionSpec } from './types';
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
  selectedNodeId: string | null; // 'trigger' | 'conditions' | action.id
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
  selectNode: (id: string | null) => void;
  validate: () => boolean;
  save: () => Promise<void>;
  loadWorkflow: (id: string) => Promise<void>;
  fetchSchema: () => Promise<void>;
  invalidateSchema: () => void;
  fetchObjectFields: (slug: string, forceRefresh?: boolean) => Promise<void>;
  reset: () => void;
}

function arrayMove<T>(arr: T[], from: number, to: number): T[] {
  const result = [...arr];
  const [item] = result.splice(from, 1);
  result.splice(to, 0, item);
  return result;
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

  insertAction: (action, index) =>
    set((s) => {
      const actions = [...s.actions];
      actions.splice(index, 0, action);
      return { actions, isDirty: true };
    }),

  updateAction: (id, patch) =>
    set((s) => ({
      actions: s.actions.map((a) =>
        a.id === id ? { ...a, ...patch, params: { ...a.params, ...(patch.params || {}) } } : a
      ),
      isDirty: true,
    })),

  removeAction: (id) =>
    set((s) => ({
      actions: s.actions.filter((a) => a.id !== id),
      selectedNodeId: s.selectedNodeId === id ? null : s.selectedNodeId,
      isDirty: true,
    })),

  reorderActions: (fromIdx, toIdx) =>
    set((s) => ({
      actions: arrayMove(s.actions, fromIdx, toIdx),
      isDirty: true,
    })),

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
    }

    if (state.actions.length === 0) {
      errors.actions = ['At least one action is required'];
    }

    // Validate with zod
    const result = workflowSchema.safeParse({
      name: state.name,
      description: state.description,
      trigger: state.trigger,
      conditions: state.conditions,
      actions: state.actions,
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
      for (let i = 0; i < state.conditions.rules.length; i++) {
        const rule = state.conditions.rules[i];
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

      if (action.type === 'update_contact') {
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
      // Sanitize: strip empty CC strings → omit key entirely; clean update_contact params
      const cleanedActions = state.actions.map((a) => {
        if (a.type === 'send_email') {
          const params = { ...a.params };
          const cc = typeof params.cc === 'string' ? params.cc.trim() : '';
          if (!cc) {
            delete params.cc;
          }
          return { ...a, params };
        }
        if (a.type === 'update_contact') {
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
    set({
      workflowId: wf.id,
      name: wf.name,
      description: wf.description,
      isActive: wf.is_active,
      trigger: wf.trigger,
      conditions: wf.conditions,
      actions: wf.actions,
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

  reset: () => {
    // Preserve schema and fieldCache across resets — they don't change when navigating between workflows
    const { schema, schemaError, fieldCache } = get();
    set({ ...initialState, schema, schemaError, fieldCache, errors: {} });
  },
}));
