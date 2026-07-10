import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import type { WorkflowStep, ConditionGroup } from '../types';

// A7.3: store apply/keep/undo for an AI-generated draft.

const draftSteps: WorkflowStep[] = [
  { id: 'ai_1', type: 'action', action: { id: 'ai_1', type: 'notify_user', params: { recipient: 'owner_field', title: 'Won' } } },
];

function seedCurrent() {
  useBuilderStore.setState({
    workflowId: 'wf1',
    name: 'Old workflow',
    trigger: { type: 'contact_created' },
    steps: [],
    actions: [],
  });
}

beforeEach(() => useBuilderStore.getState().reset());

describe('copilot draft apply/keep/undo', () => {
  it('applyDraft snapshots the current state and applies the draft (keeping workflowId)', () => {
    seedCurrent();
    useBuilderStore.getState().applyDraft({
      name: 'Drafted workflow',
      trigger: { type: 'deal_stage_changed', params: { to_stage: 's1' } },
      steps: draftSteps,
    });
    const s = useBuilderStore.getState();
    expect(s.name).toBe('Drafted workflow');
    expect(s.trigger?.type).toBe('deal_stage_changed');
    expect(s.steps).toHaveLength(1);
    expect(s.actions).toHaveLength(1); // flattened from steps
    expect(s.isDirty).toBe(true);
    expect(s.draftSnapshot).not.toBeNull();
    expect(s.workflowId).toBe('wf1'); // applies into the current session
  });

  it('undoDraft restores the pre-draft state and clears the snapshot', () => {
    seedCurrent();
    const store = useBuilderStore.getState();
    store.applyDraft({ name: 'Drafted', trigger: { type: 'deal_stage_changed', params: {} }, steps: draftSteps });
    useBuilderStore.getState().undoDraft();
    const s = useBuilderStore.getState();
    expect(s.name).toBe('Old workflow');
    expect(s.trigger?.type).toBe('contact_created');
    expect(s.steps).toHaveLength(0);
    expect(s.draftSnapshot).toBeNull();
  });

  it('keepDraft clears the snapshot but keeps the applied draft', () => {
    seedCurrent();
    const store = useBuilderStore.getState();
    store.applyDraft({ name: 'Drafted', trigger: { type: 'deal_stage_changed', params: {} }, steps: draftSteps });
    useBuilderStore.getState().keepDraft();
    const s = useBuilderStore.getState();
    expect(s.name).toBe('Drafted');
    expect(s.steps).toHaveLength(1);
    expect(s.draftSnapshot).toBeNull();
  });

  it('undoDraft is a no-op when there is no snapshot', () => {
    seedCurrent();
    useBuilderStore.getState().undoDraft();
    expect(useBuilderStore.getState().name).toBe('Old workflow');
  });

  it('a second applyDraft before Keep/Undo preserves the ORIGINAL baseline for Undo', () => {
    seedCurrent(); // 'Old workflow', contact_created, no steps
    useBuilderStore.getState().applyDraft({ name: 'Draft A', trigger: { type: 'deal_stage_changed', params: {} }, steps: draftSteps });
    // Regenerate without keeping/undoing: the snapshot must stay the ORIGINAL, not Draft A.
    useBuilderStore.getState().applyDraft({ name: 'Draft B', trigger: { type: 'deal_updated', params: {} }, steps: [] });
    useBuilderStore.getState().undoDraft();
    const s = useBuilderStore.getState();
    expect(s.name).toBe('Old workflow');
    expect(s.trigger?.type).toBe('contact_created');
    expect(s.steps).toHaveLength(0);
  });

  it('undoDraft restores the pre-draft isDirty flag (a clean workflow stays clean)', () => {
    useBuilderStore.setState({ name: 'Loaded', trigger: { type: 'contact_created' }, steps: [], actions: [], isDirty: false });
    useBuilderStore.getState().applyDraft({ name: 'D', trigger: { type: 'deal_updated', params: {} }, steps: draftSteps });
    expect(useBuilderStore.getState().isDirty).toBe(true);
    useBuilderStore.getState().undoDraft();
    expect(useBuilderStore.getState().isDirty).toBe(false);
  });

  it('applyDraft coerces a malformed conditions object (missing rules) to null', () => {
    seedCurrent();
    // A model can emit a rules-less group; left verbatim it crashes the config panel + save.
    const malformed = { op: 'AND' } as unknown as ConditionGroup;
    useBuilderStore.getState().applyDraft({
      name: 'D',
      trigger: { type: 'deal_stage_changed', params: {} },
      conditions: malformed,
      steps: draftSteps,
    });
    expect(useBuilderStore.getState().conditions).toBeNull();
  });

  it('applyDraft keeps a well-formed conditions group', () => {
    seedCurrent();
    const cg = { op: 'OR', rules: [{ field: 'deal.value', operator: 'gte', value: 1000 }] } as unknown as ConditionGroup;
    useBuilderStore.getState().applyDraft({
      name: 'D',
      trigger: { type: 'deal_stage_changed', params: {} },
      conditions: cg,
      steps: draftSteps,
    });
    expect(useBuilderStore.getState().conditions).toEqual(cg);
  });
});
