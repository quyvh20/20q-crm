import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore } from '../store';
import type { WorkflowStep } from '../types';

// ── Helpers ──────────────────────────────────────────────────────────
function mkAction(id: string): WorkflowStep {
  return {
    id,
    type: 'action',
    action: { id, type: 'create_task', params: { title: `Task ${id}` } },
  };
}

function mkCondition(id: string, yes: WorkflowStep[] = [], no: WorkflowStep[] = []): WorkflowStep {
  return {
    id,
    type: 'condition',
    condition: { op: 'AND', rules: [{ field: 'contact.email', operator: 'eq', value: 'x' }] },
    yes_steps: yes,
    no_steps: no,
  };
}

function mkDelay(id: string, sec = 60): WorkflowStep {
  return { id, type: 'delay', delay: { duration_sec: sec } };
}

// ── Setup ────────────────────────────────────────────────────────────
beforeEach(() => {
  useBuilderStore.getState().reset();
});

// ═════════════════════════════════════════════════════════════════════
// 1. Root-level insertions (parentId = null)
// ═════════════════════════════════════════════════════════════════════
describe('insertStep — root level', () => {
  it('appends to empty tree', () => {
    const step = mkAction('a1');
    useBuilderStore.getState().addStep(step, null, null);

    const { steps } = useBuilderStore.getState();
    expect(steps).toHaveLength(1);
    expect(steps[0].id).toBe('a1');
  });

  it('appends to end when index is omitted', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a2', 'a3']);
  });

  it('inserts at specific index=0 (prepend)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('new'), null, null, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['new', 'a1', 'a2']);
  });

  it('inserts at specific index=1 (middle)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('mid'), null, null, 1);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'mid', 'a2']);
  });

  it('sets isDirty = true', () => {
    expect(useBuilderStore.getState().isDirty).toBe(false);
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    expect(useBuilderStore.getState().isDirty).toBe(true);
  });

  it('syncs flattened actions array', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkDelay('d1', 120), null, null);

    const { actions } = useBuilderStore.getState();
    expect(actions).toHaveLength(2);
    expect(actions[0].id).toBe('a1');
    expect(actions[0].type).toBe('create_task');
    expect(actions[1].id).toBe('d1');
    expect(actions[1].type).toBe('delay');
  });
});

// ═════════════════════════════════════════════════════════════════════
// 2. Branch-level insertions (parentId + branch)
// ═════════════════════════════════════════════════════════════════════
describe('insertStep — into condition branches', () => {
  it('inserts into yes_steps of a condition', () => {
    const cond = mkCondition('c1');
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('yes1'), 'c1', 'yes');

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps).toHaveLength(1);
    expect(root.yes_steps![0].id).toBe('yes1');
    expect(root.no_steps).toHaveLength(0);
  });

  it('inserts into no_steps of a condition', () => {
    const cond = mkCondition('c1');
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('no1'), 'c1', 'no');

    const root = useBuilderStore.getState().steps[0];
    expect(root.no_steps).toHaveLength(1);
    expect(root.no_steps![0].id).toBe('no1');
    expect(root.yes_steps).toHaveLength(0);
  });

  it('appends to existing yes_steps', () => {
    const cond = mkCondition('c1', [mkAction('existing')]);
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('appended'), 'c1', 'yes');

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes).toHaveLength(2);
    expect(yes[0].id).toBe('existing');
    expect(yes[1].id).toBe('appended');
  });

  it('inserts at index=0 in yes_steps (prepend)', () => {
    const cond = mkCondition('c1', [mkAction('existing')]);
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('first'), 'c1', 'yes', 0);

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes).toHaveLength(2);
    expect(yes[0].id).toBe('first');
    expect(yes[1].id).toBe('existing');
  });

  it('inserts at middle index in no_steps', () => {
    const cond = mkCondition('c1', [], [mkAction('n1'), mkAction('n3')]);
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('n2'), 'c1', 'no', 1);

    const no = useBuilderStore.getState().steps[0].no_steps!;
    expect(no.map((s) => s.id)).toEqual(['n1', 'n2', 'n3']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// 3. Deep nested insertions (2+ levels deep)
// ═════════════════════════════════════════════════════════════════════
describe('insertStep — deeply nested', () => {
  it('inserts into a nested condition (depth 2)', () => {
    // Root: [cond_outer] → yes_steps: [cond_inner] → yes_steps: []
    const inner = mkCondition('inner');
    const outer = mkCondition('outer', [inner]);
    useBuilderStore.getState().addStep(outer, null, null);

    // Insert action into inner.yes_steps
    useBuilderStore.getState().addStep(mkAction('deep_action'), 'inner', 'yes');

    const root = useBuilderStore.getState().steps[0];
    const innerStep = root.yes_steps![0];
    expect(innerStep.id).toBe('inner');
    expect(innerStep.yes_steps).toHaveLength(1);
    expect(innerStep.yes_steps![0].id).toBe('deep_action');
  });

  it('inserts into a nested condition (depth 3)', () => {
    const level3 = mkCondition('L3');
    const level2 = mkCondition('L2', [level3]);
    const level1 = mkCondition('L1', [level2]);
    useBuilderStore.getState().addStep(level1, null, null);

    useBuilderStore.getState().addStep(mkAction('bottom'), 'L3', 'no');

    const steps = useBuilderStore.getState().steps;
    const bottom = steps[0].yes_steps![0].yes_steps![0].no_steps!;
    expect(bottom).toHaveLength(1);
    expect(bottom[0].id).toBe('bottom');
  });

  it('leaves sibling branches untouched', () => {
    const cond = mkCondition('c1', [mkAction('y1')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('y2'), 'c1', 'yes');

    const root = useBuilderStore.getState().steps[0];
    // yes got the new step
    expect(root.yes_steps!.map((s) => s.id)).toEqual(['y1', 'y2']);
    // no branch is unchanged
    expect(root.no_steps!.map((s) => s.id)).toEqual(['n1']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// 4. Immutability guarantees
// ═════════════════════════════════════════════════════════════════════
describe('insertStep — immutability', () => {
  it('returns new root array reference', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    const before = useBuilderStore.getState().steps;

    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    const after = useBuilderStore.getState().steps;

    expect(before).not.toBe(after);
    // Original reference must NOT have been mutated
    expect(before).toHaveLength(1);
    expect(after).toHaveLength(2);
  });

  it('does not mutate original step objects in the tree', () => {
    const cond = mkCondition('c1', [mkAction('y1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    // Grab a reference to the condition step before mutation
    const beforeCond = useBuilderStore.getState().steps[0];
    const beforeYes = beforeCond.yes_steps!;

    // Insert into the yes branch
    useBuilderStore.getState().addStep(mkAction('y2'), 'c1', 'yes');

    const afterCond = useBuilderStore.getState().steps[0];

    // The condition step reference should be new
    expect(afterCond).not.toBe(beforeCond);
    // The old yes_steps array should still be length 1 (not mutated)
    expect(beforeYes).toHaveLength(1);
    // The new yes_steps array should be length 2
    expect(afterCond.yes_steps).toHaveLength(2);
  });

  it('does not mutate sibling steps during deep insert', () => {
    useBuilderStore.getState().addStep(mkAction('sibling'), null, null);
    const innerCond = mkCondition('c1');
    useBuilderStore.getState().addStep(innerCond, null, null);

    // Insert deeply into the condition
    useBuilderStore.getState().addStep(mkAction('deep'), 'c1', 'yes');

    // The sibling step at index 0 should have the same content (id, type)
    const siblingAfter = useBuilderStore.getState().steps[0];
    expect(siblingAfter.id).toBe('sibling');
    expect(siblingAfter.type).toBe('action');
  });
});

// ═════════════════════════════════════════════════════════════════════
// 5. Mixed step types (action, condition, delay)
// ═════════════════════════════════════════════════════════════════════
describe('insertStep — mixed types', () => {
  it('inserts delay step into a branch', () => {
    const cond = mkCondition('c1');
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkDelay('wait', 300), 'c1', 'yes');

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes).toHaveLength(1);
    expect(yes[0].type).toBe('delay');
    expect(yes[0].delay!.duration_sec).toBe(300);
  });

  it('inserts condition into a condition branch (nesting)', () => {
    const outer = mkCondition('outer');
    useBuilderStore.getState().addStep(outer, null, null);
    useBuilderStore.getState().addStep(mkCondition('nested'), 'outer', 'yes');

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes).toHaveLength(1);
    expect(yes[0].type).toBe('condition');
    expect(yes[0].id).toBe('nested');
  });

  it('builds a realistic tree: action → condition(yes:[action, delay], no:[action])', () => {
    useBuilderStore.getState().addStep(mkAction('initial'), null, null);
    useBuilderStore.getState().addStep(mkCondition('check'), null, null);
    useBuilderStore.getState().addStep(mkAction('yes_task'), 'check', 'yes');
    useBuilderStore.getState().addStep(mkDelay('yes_wait', 600), 'check', 'yes');
    useBuilderStore.getState().addStep(mkAction('no_task'), 'check', 'no');

    const { steps, actions } = useBuilderStore.getState();
    expect(steps).toHaveLength(2);
    expect(steps[0].id).toBe('initial');
    expect(steps[1].id).toBe('check');
    expect(steps[1].yes_steps!.map((s) => s.id)).toEqual(['yes_task', 'yes_wait']);
    expect(steps[1].no_steps!.map((s) => s.id)).toEqual(['no_task']);

    // Actions should contain the flattened list of action/delay steps
    const actionIds = actions.map((a) => a.id);
    expect(actionIds).toContain('initial');
    expect(actionIds).toContain('yes_task');
    expect(actionIds).toContain('yes_wait');
    expect(actionIds).toContain('no_task');
  });
});

// ═════════════════════════════════════════════════════════════════════
// 6. Edge cases
// ═════════════════════════════════════════════════════════════════════
describe('insertStep — edge cases', () => {
  it('parentId not found: tree unchanged', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('orphan'), 'nonexistent', 'yes');

    // Orphan should not appear anywhere
    const { steps } = useBuilderStore.getState();
    expect(steps).toHaveLength(1);
    expect(steps[0].id).toBe('a1');
  });

  it('branch=null on condition parent: no change to either branch', () => {
    const cond = mkCondition('c1');
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('noop'), 'c1', null);

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps).toHaveLength(0);
    expect(root.no_steps).toHaveLength(0);
  });

  it('multiple inserts into same branch maintain correct order', () => {
    const cond = mkCondition('c1');
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('step1'), 'c1', 'yes');
    useBuilderStore.getState().addStep(mkAction('step2'), 'c1', 'yes');
    useBuilderStore.getState().addStep(mkAction('step3'), 'c1', 'yes');

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes.map((s) => s.id)).toEqual(['step1', 'step2', 'step3']);
  });

  it('insert at index beyond length appends', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('far'), null, null, 999);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'far']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — root level
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — root level', () => {
  it('removes the only root step → empty tree', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().removeStep('a1');

    expect(useBuilderStore.getState().steps).toHaveLength(0);
  });

  it('removes first of three root steps', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().removeStep('a1');

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a2', 'a3']);
  });

  it('removes middle root step', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().removeStep('a2');

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a3']);
  });

  it('removes last root step', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().removeStep('a2');

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1']);
  });

  it('sets isDirty = true', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.setState({ isDirty: false });

    useBuilderStore.getState().removeStep('a1');

    expect(useBuilderStore.getState().isDirty).toBe(true);
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — from condition branches
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — from condition branches', () => {
  it('removes from yes_steps', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().removeStep('y1');

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps!.map((s) => s.id)).toEqual(['y2']);
    expect(root.no_steps!.map((s) => s.id)).toEqual(['n1']);
  });

  it('removes from no_steps', () => {
    const cond = mkCondition('c1', [mkAction('y1')], [mkAction('n1'), mkAction('n2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().removeStep('n1');

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps!.map((s) => s.id)).toEqual(['y1']);
    expect(root.no_steps!.map((s) => s.id)).toEqual(['n2']);
  });

  it('removes last step from a branch → empty branch', () => {
    const cond = mkCondition('c1', [mkAction('only')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().removeStep('only');

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps).toHaveLength(0);
  });

  it('does not touch sibling branch', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().removeStep('y2');

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps!.map((s) => s.id)).toEqual(['y1']);
    // no_steps untouched
    expect(root.no_steps!.map((s) => s.id)).toEqual(['n1']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — deeply nested
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — deeply nested', () => {
  it('removes from depth 2', () => {
    const inner = mkCondition('inner', [mkAction('deep1'), mkAction('deep2')]);
    const outer = mkCondition('outer', [inner]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().removeStep('deep1');

    const innerAfter = useBuilderStore.getState().steps[0].yes_steps![0];
    expect(innerAfter.yes_steps!.map((s) => s.id)).toEqual(['deep2']);
  });

  it('removes from depth 3', () => {
    const l3 = mkCondition('L3', [], [mkAction('target'), mkAction('keep')]);
    const l2 = mkCondition('L2', [l3]);
    const l1 = mkCondition('L1', [l2]);
    useBuilderStore.getState().addStep(l1, null, null);

    useBuilderStore.getState().removeStep('target');

    const noSteps = useBuilderStore.getState().steps[0].yes_steps![0].yes_steps![0].no_steps!;
    expect(noSteps.map((s) => s.id)).toEqual(['keep']);
  });

  it('removes a nested condition node entirely', () => {
    const inner = mkCondition('inner', [mkAction('y1')], [mkAction('n1')]);
    const outer = mkCondition('outer', [inner, mkAction('sibling')]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().removeStep('inner');

    const outerAfter = useBuilderStore.getState().steps[0];
    expect(outerAfter.yes_steps!.map((s) => s.id)).toEqual(['sibling']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — condition subtree cascade (removing parent drops children)
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — condition subtree cascade', () => {
  it('removing a condition removes all its branch children from actions', () => {
    useBuilderStore.getState().addStep(mkAction('root_action'), null, null);
    const cond = mkCondition('cond', [mkAction('y1'), mkAction('y2')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    // Before: actions should include root_action, y1, y2, n1
    const actionsBefore = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionsBefore).toContain('root_action');
    expect(actionsBefore).toContain('y1');
    expect(actionsBefore).toContain('y2');
    expect(actionsBefore).toContain('n1');

    // Remove the condition
    useBuilderStore.getState().removeStep('cond');

    // After: only root_action should remain
    const actionsAfter = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionsAfter).toEqual(['root_action']);
    expect(useBuilderStore.getState().steps).toHaveLength(1);
  });

  it('removing a deeply nested condition cascades properly', () => {
    const inner = mkCondition('inner', [mkAction('deep1')], [mkAction('deep2')]);
    const outer = mkCondition('outer', [inner, mkAction('kept')]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().removeStep('inner');

    const actionsAfter = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionsAfter).toEqual(['kept']);
    expect(actionsAfter).not.toContain('deep1');
    expect(actionsAfter).not.toContain('deep2');
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — immutability guarantees
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — immutability', () => {
  it('returns new root array reference', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    const before = useBuilderStore.getState().steps;

    useBuilderStore.getState().removeStep('a1');
    const after = useBuilderStore.getState().steps;

    expect(before).not.toBe(after);
    expect(before).toHaveLength(2);
    expect(after).toHaveLength(1);
  });

  it('does not mutate original condition step when removing from branch', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    const beforeCond = useBuilderStore.getState().steps[0];
    const beforeYes = beforeCond.yes_steps!;

    useBuilderStore.getState().removeStep('y1');

    // Old references unchanged
    expect(beforeYes).toHaveLength(2);
    expect(beforeYes[0].id).toBe('y1');

    // New references updated
    const afterCond = useBuilderStore.getState().steps[0];
    expect(afterCond).not.toBe(beforeCond);
    expect(afterCond.yes_steps).toHaveLength(1);
    expect(afterCond.yes_steps![0].id).toBe('y2');
  });

  it('does not mutate sibling steps', () => {
    useBuilderStore.getState().addStep(mkAction('sibling'), null, null);
    useBuilderStore.getState().addStep(mkAction('target'), null, null);

    const siblingBefore = useBuilderStore.getState().steps[0];

    useBuilderStore.getState().removeStep('target');

    const siblingAfter = useBuilderStore.getState().steps[0];
    expect(siblingAfter.id).toBe('sibling');
    // Content identical even though reference may differ (findAndModifySteps shallow-copies)
    expect(siblingBefore.id).toBe(siblingAfter.id);
    expect(siblingBefore.type).toBe(siblingAfter.type);
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — selectedNodeId clearing
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — selectedNodeId', () => {
  it('clears selectedNodeId when the removed step was selected', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.setState({ selectedNodeId: 'a1' });

    useBuilderStore.getState().removeStep('a1');

    expect(useBuilderStore.getState().selectedNodeId).toBeNull();
  });

  it('preserves selectedNodeId when a different step is removed', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.setState({ selectedNodeId: 'a1' });

    useBuilderStore.getState().removeStep('a2');

    expect(useBuilderStore.getState().selectedNodeId).toBe('a1');
  });

  it('clears selectedNodeId when removing a branch step that was selected', () => {
    const cond = mkCondition('c1', [mkAction('y1')]);
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.setState({ selectedNodeId: 'y1' });

    useBuilderStore.getState().removeStep('y1');

    expect(useBuilderStore.getState().selectedNodeId).toBeNull();
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — actions sync
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — actions sync', () => {
  it('syncs flattened actions after root removal', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkDelay('d1', 300), null, null);

    useBuilderStore.getState().removeStep('a1');

    const actionIds = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionIds).toEqual(['a2', 'd1']);
  });

  it('syncs flattened actions after branch removal', () => {
    useBuilderStore.getState().addStep(mkAction('root'), null, null);
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().removeStep('y1');

    const actionIds = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionIds).toEqual(['root', 'y2']);
  });

  it('actions becomes empty after removing all steps', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);

    useBuilderStore.getState().removeStep('a1');

    expect(useBuilderStore.getState().actions).toHaveLength(0);
  });
});

// ═════════════════════════════════════════════════════════════════════
// removeStep — edge cases
// ═════════════════════════════════════════════════════════════════════
describe('removeStep — edge cases', () => {
  it('removing nonexistent id: tree unchanged', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().removeStep('ghost');

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a2']);
  });

  it('removing from empty tree: no crash', () => {
    useBuilderStore.getState().removeStep('anything');

    expect(useBuilderStore.getState().steps).toHaveLength(0);
    expect(useBuilderStore.getState().actions).toHaveLength(0);
  });

  it('removing a delay step works', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkDelay('d1', 120), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().removeStep('d1');

    const stepIds = useBuilderStore.getState().steps.map((s) => s.id);
    expect(stepIds).toEqual(['a1', 'a2']);
    const actionIds = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionIds).toEqual(['a1', 'a2']);
  });

  it('multiple sequential removes', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);
    useBuilderStore.getState().addStep(mkAction('a4'), null, null);

    useBuilderStore.getState().removeStep('a2');
    useBuilderStore.getState().removeStep('a4');

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a3']);
  });

  it('remove then add: tree is consistent', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().removeStep('a1');
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a2', 'a3']);
  });
});
