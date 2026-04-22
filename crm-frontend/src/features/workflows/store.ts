import { create } from 'zustand';
import type { TriggerSpec, ConditionGroup, ActionSpec } from './types';
import { workflowSchema, validateActionIds, validateConditionDepth } from './schemas';
import { createWorkflow, updateWorkflow, getWorkflow } from './api';

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
};

export const useBuilderStore = create<BuilderState>((set, get) => ({
  ...initialState,

  setName: (name) => set({ name, isDirty: true }),
  setDescription: (description) => set({ description, isDirty: true }),

  setTrigger: (trigger) => set({ trigger, isDirty: true }),

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
      errors.trigger = ['Trigger is required'];
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
    }

    set({ errors });
    return Object.keys(errors).length === 0;
  },

  save: async () => {
    const state = get();
    if (!state.validate()) return;

    set({ saving: true });
    try {
      const payload = {
        name: state.name,
        description: state.description,
        trigger: state.trigger!,
        conditions: state.conditions,
        actions: state.actions,
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

  reset: () => set({ ...initialState, errors: {} }),
}));
