import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore, normalizeMergesInList } from '../store';
import type { WorkflowStep, Workflow } from '../types';

// The no-merge invariant: an If/Else forks into Yes/No and the branches never rejoin
// — a condition is always the LAST step in its sibling list. These cover the four
// enforcement points: insert-absorb, reorder guard (see dragReorder.test), auto-split
// on load / AI draft, and the save-time backstop.

const mkAction = (id: string, type = 'create_task', params: Record<string, unknown> = {}): WorkflowStep => ({
  id,
  type: 'action',
  action: { id, type: type as never, params },
});
const mkCondition = (id: string, yes: WorkflowStep[] = [], no: WorkflowStep[] = []): WorkflowStep => ({
  id,
  type: 'condition',
  condition: { op: 'AND', rules: [{ field: 'contact.email', operator: 'eq', value: 'x' }] },
  yes_steps: yes,
  no_steps: no,
});

function makeWf(steps: WorkflowStep[]): Workflow {
  return {
    id: 'wf1',
    org_id: 'org1',
    name: 'WF',
    description: '',
    is_active: false,
    trigger: { type: 'contact_created' },
    conditions: null,
    actions: [],
    steps,
    action_count: 0,
    version: 1,
    created_by: 'u1',
    created_at: '',
    updated_at: '',
    last_run_status: null,
    last_run_at: null,
  };
}

beforeEach(() => useBuilderStore.getState().reset());

describe('normalizeMergesInList (auto-split)', () => {
  it('leaves a terminal condition untouched (no change)', () => {
    const steps = [mkAction('a1'), mkCondition('c1', [mkAction('y1')], [mkAction('n1')])];
    const { steps: out, changed } = normalizeMergesInList(steps);
    expect(changed).toBe(false);
    expect(out).toEqual(steps);
  });

  it('returns the same array reference when there is no condition at all', () => {
    const steps = [mkAction('a1'), mkAction('a2')];
    const { steps: out, changed } = normalizeMergesInList(steps);
    expect(changed).toBe(false);
    expect(out).toBe(steps);
  });

  it('copies a post-condition step into BOTH branches and drops the trailing sibling', () => {
    const steps = [mkCondition('c1', [], []), mkAction('after', 'log_activity', { title: 'A' })];
    const { steps: out, changed } = normalizeMergesInList(steps);
    expect(changed).toBe(true);
    expect(out).toHaveLength(1); // only the condition remains at this level
    const c = out[0];
    expect(c.type).toBe('condition');
    expect(c.yes_steps!.map((s) => s.action!.type)).toEqual(['log_activity']);
    expect(c.no_steps!.map((s) => s.action!.type)).toEqual(['log_activity']);
  });

  it('gives every copied step a fresh, unique id (never the original)', () => {
    const steps = [mkCondition('c1'), mkAction('p1'), mkAction('q1')];
    const { steps: out } = normalizeMergesInList(steps);
    const ids = [...out[0].yes_steps!, ...out[0].no_steps!].map((s) => s.id);
    expect(ids).toHaveLength(4);
    expect(new Set(ids).size).toBe(4); // all distinct across both branches
    expect(ids).not.toContain('p1');
    expect(ids).not.toContain('q1');
    // action.id tracks the step id
    for (const s of [...out[0].yes_steps!, ...out[0].no_steps!]) {
      expect(s.action!.id).toBe(s.id);
    }
  });

  it('remaps {{actions.<id>}} references inside the copied subtree to the new ids', () => {
    const steps = [
      mkCondition('c1'),
      mkAction('p1', 'log_activity', { title: 'P' }),
      mkAction('q1', 'send_email', { to: 'x@y.com', body_html: 'Ref {{actions.p1.output}}' }),
    ];
    const { steps: out } = normalizeMergesInList(steps);
    for (const branch of [out[0].yes_steps!, out[0].no_steps!]) {
      const [p, q] = branch;
      // q's reference now points at THIS branch's copy of p, not the original 'p1'.
      expect(q.action!.params.body_html).toBe(`Ref {{actions.${p.id}.output}}`);
      expect(q.action!.params.body_html).not.toContain('p1');
    }
  });

  it('splits a merge nested inside a branch (recurses)', () => {
    const inner = [mkCondition('inner'), mkAction('x')]; // merge inside the yes branch
    const steps = [mkCondition('outer', inner, [])];
    const { steps: out, changed } = normalizeMergesInList(steps);
    expect(changed).toBe(true);
    const outerYes = out[0].yes_steps!;
    expect(outerYes).toHaveLength(1); // inner condition only; 'x' absorbed into its branches
    expect(outerYes[0].id).toBe('inner');
    expect(outerYes[0].yes_steps!).toHaveLength(1);
    expect(outerYes[0].no_steps!).toHaveLength(1);
  });
});

describe('insert & absorb (addStep of a condition)', () => {
  beforeEach(() => useBuilderStore.setState({ trigger: { type: 'contact_created' }, steps: [] }));

  it('inserting a condition before existing steps moves them into its Yes branch', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    // Insert an If/Else at the top (index 0) — a1, a2 should be absorbed into Yes.
    useBuilderStore.getState().addStep(mkCondition('c1'), null, null, 0);

    const steps = useBuilderStore.getState().steps;
    expect(steps.map((s) => s.id)).toEqual(['c1']);
    expect(steps[0].yes_steps!.map((s) => s.id)).toEqual(['a1', 'a2']);
    expect(steps[0].no_steps).toEqual([]);
  });

  it('appending a condition at the tail just forks (nothing to absorb)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkCondition('c1'), null, null); // append

    const steps = useBuilderStore.getState().steps;
    expect(steps.map((s) => s.id)).toEqual(['a1', 'c1']);
    expect(steps[1].yes_steps).toEqual([]);
    expect(steps[1].no_steps).toEqual([]);
  });

  it('absorbs trailing steps within a branch too', () => {
    useBuilderStore.getState().addStep(mkCondition('outer'), null, null);
    useBuilderStore.getState().addStep(mkAction('a1'), 'outer', 'yes');
    useBuilderStore.getState().addStep(mkAction('a2'), 'outer', 'yes');
    // Insert a nested If/Else at the front of outer.yes → a1, a2 absorbed into it.
    useBuilderStore.getState().addStep(mkCondition('inner'), 'outer', 'yes', 0);

    const outerYes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(outerYes.map((s) => s.id)).toEqual(['inner']);
    expect(outerYes[0].yes_steps!.map((s) => s.id)).toEqual(['a1', 'a2']);
  });
});

describe('auto-split on open (applyLoadedWorkflow)', () => {
  it('splits a saved merge into both branches and marks dirty + notice', () => {
    useBuilderStore.getState().applyLoadedWorkflow(makeWf([mkCondition('c1'), mkAction('after')]));
    const s = useBuilderStore.getState();
    expect(s.steps).toHaveLength(1);
    expect(s.steps[0].yes_steps).toHaveLength(1);
    expect(s.steps[0].no_steps).toHaveLength(1);
    expect(s.isDirty).toBe(true);
    expect(s.autoSplitNotice).toBe(true);
    // actions view re-derived from the rewritten tree (both branch copies present)
    expect(s.actions).toHaveLength(2);
  });

  it('leaves a clean (terminal) workflow untouched — no dirty, no notice', () => {
    useBuilderStore.getState().applyLoadedWorkflow(
      makeWf([mkAction('a1'), mkCondition('c1', [mkAction('y1')], [mkAction('n1')])]),
    );
    const s = useBuilderStore.getState();
    expect(s.steps.map((x) => x.id)).toEqual(['a1', 'c1']);
    expect(s.isDirty).toBe(false);
    expect(s.autoSplitNotice).toBe(false);
  });

  it('dismissAutoSplitNotice clears the flag', () => {
    useBuilderStore.getState().applyLoadedWorkflow(makeWf([mkCondition('c1'), mkAction('after')]));
    expect(useBuilderStore.getState().autoSplitNotice).toBe(true);
    useBuilderStore.getState().dismissAutoSplitNotice();
    expect(useBuilderStore.getState().autoSplitNotice).toBe(false);
  });
});

describe('applyDraft normalization', () => {
  it('auto-splits a merge the AI emitted', () => {
    useBuilderStore.getState().applyDraft({
      trigger: { type: 'contact_created' },
      steps: [mkCondition('c1'), mkAction('after')],
    });
    const s = useBuilderStore.getState();
    expect(s.steps).toHaveLength(1);
    expect(s.steps[0].yes_steps).toHaveLength(1);
    expect(s.steps[0].no_steps).toHaveLength(1);
    expect(s.autoSplitNotice).toBe(true);
  });
});

describe('validate() no-merge backstop', () => {
  it('rejects a tree where a step follows a condition', () => {
    // Set a merge directly (bypassing the insert/reorder guards) to prove the backstop.
    const after = mkAction('after', 'create_task', { title: 'T' });
    useBuilderStore.setState({
      name: 'WF',
      trigger: { type: 'contact_created' },
      steps: [mkCondition('c1'), after],
      actions: [{ id: 'after', type: 'create_task', params: { title: 'T' } }],
    });
    const ok = useBuilderStore.getState().validate();
    expect(ok).toBe(false);
    expect(useBuilderStore.getState().errors['step.c1']?.[0]).toMatch(/must be the last step/i);
  });
});
