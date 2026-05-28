import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore, getStepAtPath, getParentPath } from '../store';
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

// ═════════════════════════════════════════════════════════════════════
// updateStep — action param merge
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — action param merge', () => {
  it('merges new params into existing action params', () => {
    const step = mkAction('a1');
    useBuilderStore.getState().addStep(step, null, null);

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { priority: 'high' } },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.action!.params.title).toBe('Task a1'); // preserved
    expect(updated.action!.params.priority).toBe('high'); // added
  });

  it('overwrites existing param value', () => {
    const step = mkAction('a1');
    useBuilderStore.getState().addStep(step, null, null);

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { title: 'Updated Title' } },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.action!.params.title).toBe('Updated Title');
  });

  it('preserves action type and id', () => {
    const step = mkAction('a1');
    useBuilderStore.getState().addStep(step, null, null);

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { due_in_days: 5 } },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.action!.id).toBe('a1');
    expect(updated.action!.type).toBe('create_task');
  });

  it('merges multiple params at once', () => {
    const step: WorkflowStep = {
      id: 'email1',
      type: 'action',
      action: {
        id: 'email1',
        type: 'send_email',
        params: { to: '{{contact.email}}', subject: 'Hello' },
      },
    };
    useBuilderStore.getState().addStep(step, null, null);

    useBuilderStore.getState().updateStep('email1', {
      action: {
        id: 'email1',
        type: 'send_email',
        params: { subject: 'Updated', body_html: '<p>Hi</p>' },
      },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.action!.params.to).toBe('{{contact.email}}'); // preserved
    expect(updated.action!.params.subject).toBe('Updated'); // overwritten
    expect(updated.action!.params.body_html).toBe('<p>Hi</p>'); // added
  });

  it('sets isDirty = true', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.setState({ isDirty: false });

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { title: 'New' } },
    });

    expect(useBuilderStore.getState().isDirty).toBe(true);
  });
});

// ═════════════════════════════════════════════════════════════════════
// updateStep — delay step update
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — delay step', () => {
  it('updates duration_sec via action patch (shim path)', () => {
    useBuilderStore.getState().addStep(mkDelay('d1', 60), null, null);

    // This is how updateAction calls it: patch.action.params.duration_sec
    useBuilderStore.getState().updateStep('d1', {
      action: { id: 'd1', type: 'delay', params: { duration_sec: 300 } },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.type).toBe('delay');
    expect(updated.delay!.duration_sec).toBe(300);
  });

  it('preserves delay type when updating duration', () => {
    useBuilderStore.getState().addStep(mkDelay('d1', 120), null, null);

    useBuilderStore.getState().updateStep('d1', {
      action: { id: 'd1', type: 'delay', params: { duration_sec: 600 } },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.type).toBe('delay');
    expect(updated.id).toBe('d1');
    // Should not have an action field
    expect(updated.action).toBeUndefined();
  });

  it('updates delay via direct delay patch', () => {
    useBuilderStore.getState().addStep(mkDelay('d1', 60), null, null);

    useBuilderStore.getState().updateStep('d1', {
      delay: { duration_sec: 900 },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.delay!.duration_sec).toBe(900);
  });
});

// ═════════════════════════════════════════════════════════════════════
// updateStep — condition step patch
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — condition step', () => {
  it('updates condition rules via patch', () => {
    const cond = mkCondition('c1');
    useBuilderStore.getState().addStep(cond, null, null);

    const newCondition = {
      op: 'OR' as const,
      rules: [{ field: 'contact.name', operator: 'contains', value: 'John' }],
    };
    useBuilderStore.getState().updateStep('c1', { condition: newCondition });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.condition!.op).toBe('OR');
    expect(updated.condition!.rules[0].field).toBe('contact.name');
  });

  it('preserves yes_steps and no_steps when updating condition', () => {
    const cond = mkCondition('c1', [mkAction('y1')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().updateStep('c1', {
      condition: { op: 'OR', rules: [] },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.yes_steps!.map((s) => s.id)).toEqual(['y1']);
    expect(updated.no_steps!.map((s) => s.id)).toEqual(['n1']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// updateStep — in condition branches (deep)
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — in condition branches', () => {
  it('updates a step in yes_steps', () => {
    const cond = mkCondition('c1', [mkAction('y1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().updateStep('y1', {
      action: { id: 'y1', type: 'create_task', params: { title: 'Updated Y1' } },
    });

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes[0].action!.params.title).toBe('Updated Y1');
  });

  it('updates a step in no_steps', () => {
    const cond = mkCondition('c1', [], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().updateStep('n1', {
      action: { id: 'n1', type: 'create_task', params: { title: 'Updated N1' } },
    });

    const no = useBuilderStore.getState().steps[0].no_steps!;
    expect(no[0].action!.params.title).toBe('Updated N1');
  });

  it('updates a step at depth 2', () => {
    const inner = mkCondition('inner', [mkAction('deep')]);
    const outer = mkCondition('outer', [inner]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().updateStep('deep', {
      action: { id: 'deep', type: 'create_task', params: { title: 'Deep Update' } },
    });

    const deepStep = useBuilderStore.getState().steps[0].yes_steps![0].yes_steps![0];
    expect(deepStep.action!.params.title).toBe('Deep Update');
  });

  it('does not touch siblings when updating in a branch', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().updateStep('y1', {
      action: { id: 'y1', type: 'create_task', params: { title: 'Changed' } },
    });

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps![0].action!.params.title).toBe('Changed');
    expect(root.yes_steps![1].action!.params.title).toBe('Task y2'); // untouched
    expect(root.no_steps![0].action!.params.title).toBe('Task n1'); // untouched
  });
});

// ═════════════════════════════════════════════════════════════════════
// updateStep — immutability guarantees
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — immutability', () => {
  it('returns new root array reference', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    const before = useBuilderStore.getState().steps;

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { title: 'New' } },
    });
    const after = useBuilderStore.getState().steps;

    expect(before).not.toBe(after);
  });

  it('returns new step reference for updated step', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    const beforeStep = useBuilderStore.getState().steps[0];

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { title: 'New' } },
    });
    const afterStep = useBuilderStore.getState().steps[0];

    expect(beforeStep).not.toBe(afterStep);
  });

  it('does not mutate old step params object', () => {
    const step: WorkflowStep = {
      id: 'a1',
      type: 'action',
      action: { id: 'a1', type: 'send_email', params: { to: 'x@y.com', subject: 'Old' } },
    };
    useBuilderStore.getState().addStep(step, null, null);

    const beforeParams = useBuilderStore.getState().steps[0].action!.params;

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'send_email', params: { subject: 'New' } },
    });

    // Old params reference must be unchanged
    expect(beforeParams.subject).toBe('Old');
    // New params have the update
    expect(useBuilderStore.getState().steps[0].action!.params.subject).toBe('New');
  });

  it('does not mutate original condition when updating branch step', () => {
    const cond = mkCondition('c1', [mkAction('y1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    const beforeCond = useBuilderStore.getState().steps[0];

    useBuilderStore.getState().updateStep('y1', {
      action: { id: 'y1', type: 'create_task', params: { title: 'New' } },
    });

    const afterCond = useBuilderStore.getState().steps[0];
    expect(beforeCond).not.toBe(afterCond);
    // Old branch child should still have old title
    expect(beforeCond.yes_steps![0].action!.params.title).toBe('Task y1');
  });
});

// ═════════════════════════════════════════════════════════════════════
// updateStep — actions sync
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — actions sync', () => {
  it('syncs flattened actions after root action update', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().updateStep('a1', {
      action: { id: 'a1', type: 'create_task', params: { title: 'Synced' } },
    });

    const actions = useBuilderStore.getState().actions;
    expect(actions[0].params.title).toBe('Synced');
    expect(actions[1].params.title).toBe('Task a2');
  });

  it('syncs flattened actions after branch action update', () => {
    const cond = mkCondition('c1', [mkAction('y1')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().updateStep('y1', {
      action: { id: 'y1', type: 'create_task', params: { title: 'Branch Updated' } },
    });

    const actions = useBuilderStore.getState().actions;
    const y1Action = actions.find((a) => a.id === 'y1');
    expect(y1Action!.params.title).toBe('Branch Updated');
  });

  it('syncs delay update to actions', () => {
    useBuilderStore.getState().addStep(mkDelay('d1', 60), null, null);

    useBuilderStore.getState().updateStep('d1', {
      action: { id: 'd1', type: 'delay', params: { duration_sec: 999 } },
    });

    const actions = useBuilderStore.getState().actions;
    expect(actions[0].id).toBe('d1');
    expect(actions[0].params.duration_sec).toBe(999);
  });
});

// ═════════════════════════════════════════════════════════════════════
// updateStep — edge cases
// ═════════════════════════════════════════════════════════════════════
describe('updateStep — edge cases', () => {
  it('nonexistent id: tree unchanged', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);

    useBuilderStore.getState().updateStep('ghost', {
      action: { id: 'ghost', type: 'create_task', params: { title: 'Nope' } },
    });

    const step = useBuilderStore.getState().steps[0];
    expect(step.action!.params.title).toBe('Task a1');
  });

  it('empty patch object: step unchanged except for spread', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);

    useBuilderStore.getState().updateStep('a1', {});

    const step = useBuilderStore.getState().steps[0];
    expect(step.id).toBe('a1');
    expect(step.type).toBe('action');
    expect(step.action!.params.title).toBe('Task a1');
  });

  it('sequential updates accumulate', () => {
    const step: WorkflowStep = {
      id: 'e1',
      type: 'action',
      action: { id: 'e1', type: 'send_email', params: { to: 'a@b.com' } },
    };
    useBuilderStore.getState().addStep(step, null, null);

    useBuilderStore.getState().updateStep('e1', {
      action: { id: 'e1', type: 'send_email', params: { subject: 'First' } },
    });
    useBuilderStore.getState().updateStep('e1', {
      action: { id: 'e1', type: 'send_email', params: { body_html: '<p>Body</p>' } },
    });

    const updated = useBuilderStore.getState().steps[0];
    expect(updated.action!.params.to).toBe('a@b.com');
    expect(updated.action!.params.subject).toBe('First');
    expect(updated.action!.params.body_html).toBe('<p>Body</p>');
  });

  it('update delay in a branch', () => {
    const cond = mkCondition('c1', [mkDelay('d1', 60)]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().updateStep('d1', {
      action: { id: 'd1', type: 'delay', params: { duration_sec: 7200 } },
    });

    const delay = useBuilderStore.getState().steps[0].yes_steps![0];
    expect(delay.delay!.duration_sec).toBe(7200);
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps (moveStep) — root level
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — root level', () => {
  it('moves first to last (0 → 2)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 0, 2);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a2', 'a3', 'a1']);
  });

  it('moves last to first (2 → 0)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 2, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a3', 'a1', 'a2']);
  });

  it('moves middle element forward (1 → 2)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 1, 2);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a3', 'a2']);
  });

  it('moves middle element backward (1 → 0)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 1, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a2', 'a1', 'a3']);
  });

  it('reorders 2 elements (swap)', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 0, 1);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a2', 'a1']);
  });

  it('sets isDirty = true', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.setState({ isDirty: false });

    useBuilderStore.getState().reorderSteps(null, null, 0, 1);

    expect(useBuilderStore.getState().isDirty).toBe(true);
  });

  it('preserves step content after reorder', () => {
    const email: WorkflowStep = {
      id: 'e1',
      type: 'action',
      action: { id: 'e1', type: 'send_email', params: { to: 'x@y.com' } },
    };
    useBuilderStore.getState().addStep(email, null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 0, 1);

    const movedStep = useBuilderStore.getState().steps[1];
    expect(movedStep.id).toBe('e1');
    expect(movedStep.action!.type).toBe('send_email');
    expect(movedStep.action!.params.to).toBe('x@y.com');
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps — within condition branches
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — within condition branches', () => {
  it('reorders within yes_steps', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2'), mkAction('y3')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 2);

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes.map((s) => s.id)).toEqual(['y2', 'y3', 'y1']);
  });

  it('reorders within no_steps', () => {
    const cond = mkCondition('c1', [], [mkAction('n1'), mkAction('n2'), mkAction('n3')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().reorderSteps('c1', 'no', 2, 0);

    const no = useBuilderStore.getState().steps[0].no_steps!;
    expect(no.map((s) => s.id)).toEqual(['n3', 'n1', 'n2']);
  });

  it('does not touch other branch when reordering yes', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')], [mkAction('n1'), mkAction('n2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 1);

    const root = useBuilderStore.getState().steps[0];
    expect(root.yes_steps!.map((s) => s.id)).toEqual(['y2', 'y1']);
    expect(root.no_steps!.map((s) => s.id)).toEqual(['n1', 'n2']); // untouched
  });

  it('does not touch root steps when reordering branch', () => {
    useBuilderStore.getState().addStep(mkAction('root1'), null, null);
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')]);
    useBuilderStore.getState().addStep(cond, null, null);
    useBuilderStore.getState().addStep(mkAction('root2'), null, null);

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 1);

    const rootIds = useBuilderStore.getState().steps.map((s) => s.id);
    expect(rootIds).toEqual(['root1', 'c1', 'root2']); // root unchanged
    expect(useBuilderStore.getState().steps[1].yes_steps!.map((s) => s.id)).toEqual(['y2', 'y1']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps — deeply nested
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — deeply nested', () => {
  it('reorders within a nested condition yes_steps (depth 2)', () => {
    const inner = mkCondition('inner', [mkAction('d1'), mkAction('d2'), mkAction('d3')]);
    const outer = mkCondition('outer', [inner]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().reorderSteps('inner', 'yes', 2, 0);

    const innerYes = useBuilderStore.getState().steps[0].yes_steps![0].yes_steps!;
    expect(innerYes.map((s) => s.id)).toEqual(['d3', 'd1', 'd2']);
  });

  it('reorders within a nested condition no_steps (depth 2)', () => {
    const inner = mkCondition('inner', [], [mkAction('n1'), mkAction('n2')]);
    const outer = mkCondition('outer', [inner]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().reorderSteps('inner', 'no', 0, 1);

    const innerNo = useBuilderStore.getState().steps[0].yes_steps![0].no_steps!;
    expect(innerNo.map((s) => s.id)).toEqual(['n2', 'n1']);
  });

  it('leaves outer branches untouched when reordering inner', () => {
    const inner = mkCondition('inner', [mkAction('i1'), mkAction('i2')]);
    const outer = mkCondition('outer', [inner, mkAction('outerY')], [mkAction('outerN')]);
    useBuilderStore.getState().addStep(outer, null, null);

    useBuilderStore.getState().reorderSteps('inner', 'yes', 0, 1);

    const root = useBuilderStore.getState().steps[0];
    // Inner reordered
    expect(root.yes_steps![0].yes_steps!.map((s) => s.id)).toEqual(['i2', 'i1']);
    // Outer branches untouched
    expect(root.yes_steps![1].id).toBe('outerY');
    expect(root.no_steps![0].id).toBe('outerN');
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps — immutability guarantees
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — immutability', () => {
  it('returns new root array reference', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    const before = useBuilderStore.getState().steps;

    useBuilderStore.getState().reorderSteps(null, null, 0, 1);
    const after = useBuilderStore.getState().steps;

    expect(before).not.toBe(after);
  });

  it('does not mutate original root array', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);
    const before = useBuilderStore.getState().steps;
    const beforeIds = before.map((s) => s.id);

    useBuilderStore.getState().reorderSteps(null, null, 0, 2);

    // Original captured array must still have original order
    expect(before.map((s) => s.id)).toEqual(beforeIds);
  });

  it('does not mutate original branch array', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2'), mkAction('y3')]);
    useBuilderStore.getState().addStep(cond, null, null);

    const beforeYes = useBuilderStore.getState().steps[0].yes_steps!;

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 2);

    // Old yes_steps array is unchanged
    expect(beforeYes.map((s) => s.id)).toEqual(['y1', 'y2', 'y3']);
    // New yes_steps is reordered
    expect(useBuilderStore.getState().steps[0].yes_steps!.map((s) => s.id)).toEqual(['y2', 'y3', 'y1']);
  });

  it('creates new condition step reference when branch is reordered', () => {
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    const beforeCond = useBuilderStore.getState().steps[0];

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 1);

    const afterCond = useBuilderStore.getState().steps[0];
    expect(beforeCond).not.toBe(afterCond);
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps — actions sync
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — actions sync', () => {
  it('syncs flattened actions after root reorder', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 0, 2);

    const actionIds = useBuilderStore.getState().actions.map((a) => a.id);
    expect(actionIds).toEqual(['a2', 'a3', 'a1']);
  });

  it('syncs flattened actions after branch reorder', () => {
    useBuilderStore.getState().addStep(mkAction('root'), null, null);
    const cond = mkCondition('c1', [mkAction('y1'), mkAction('y2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 1);

    const actionIds = useBuilderStore.getState().actions.map((a) => a.id);
    // root stays first, then flattened branch steps in new order
    expect(actionIds[0]).toBe('root');
    expect(actionIds).toContain('y1');
    expect(actionIds).toContain('y2');
    // y2 should come before y1 in the flattened output
    expect(actionIds.indexOf('y2')).toBeLessThan(actionIds.indexOf('y1'));
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderActions shim
// ═════════════════════════════════════════════════════════════════════
describe('reorderActions shim', () => {
  it('delegates to reorderSteps with parentId=null', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);

    useBuilderStore.getState().reorderActions(2, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a3', 'a1', 'a2']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps — mixed step types
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — mixed types', () => {
  it('reorders action and delay steps at root', () => {
    useBuilderStore.getState().addStep(mkDelay('d1', 60), null, null);
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkCondition('c1'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 2, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['c1', 'd1', 'a1']);
  });

  it('reorders condition with children — subtree stays intact', () => {
    const condWithKids = mkCondition('c1', [mkAction('y1')], [mkAction('n1')]);
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(condWithKids, null, null);

    useBuilderStore.getState().reorderSteps(null, null, 1, 0);

    const steps = useBuilderStore.getState().steps;
    expect(steps[0].id).toBe('c1');
    expect(steps[0].yes_steps!.map((s) => s.id)).toEqual(['y1']);
    expect(steps[0].no_steps!.map((s) => s.id)).toEqual(['n1']);
    expect(steps[1].id).toBe('a1');
  });

  it('reorders delays in a branch', () => {
    const cond = mkCondition('c1', [mkDelay('d1'), mkAction('a1'), mkDelay('d2')]);
    useBuilderStore.getState().addStep(cond, null, null);

    useBuilderStore.getState().reorderSteps('c1', 'yes', 0, 2);

    const yes = useBuilderStore.getState().steps[0].yes_steps!;
    expect(yes.map((s) => s.id)).toEqual(['a1', 'd2', 'd1']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// reorderSteps — edge cases
// ═════════════════════════════════════════════════════════════════════
describe('reorderSteps — edge cases', () => {
  it('same from and to index: no change', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 0, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a2']);
  });

  it('single element array reorder: no crash', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);

    useBuilderStore.getState().reorderSteps(null, null, 0, 0);

    expect(useBuilderStore.getState().steps).toHaveLength(1);
    expect(useBuilderStore.getState().steps[0].id).toBe('a1');
  });

  it('sequential reorders produce correct final order', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);
    useBuilderStore.getState().addStep(mkAction('a3'), null, null);
    useBuilderStore.getState().addStep(mkAction('a4'), null, null);

    // a1,a2,a3,a4 → move 0→3 → a2,a3,a4,a1
    useBuilderStore.getState().reorderSteps(null, null, 0, 3);
    // a2,a3,a4,a1 → move 1→0 → a3,a2,a4,a1
    useBuilderStore.getState().reorderSteps(null, null, 1, 0);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a3', 'a2', 'a4', 'a1']);
  });

  it('nonexistent parentId: root unchanged', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().reorderSteps('ghost', 'yes', 0, 1);

    const ids = useBuilderStore.getState().steps.map((s) => s.id);
    expect(ids).toEqual(['a1', 'a2']);
  });
});

// ═════════════════════════════════════════════════════════════════════
// getStepAtPath — root level
// ═════════════════════════════════════════════════════════════════════
describe('getStepAtPath — root level', () => {
  it('resolves step at root index 0', () => {
    const steps = [mkAction('a1'), mkAction('a2'), mkAction('a3')];
    const result = getStepAtPath(steps, [{ index: 0 }]);
    expect(result?.id).toBe('a1');
  });

  it('resolves step at root index 1', () => {
    const steps = [mkAction('a1'), mkAction('a2')];
    const result = getStepAtPath(steps, [{ index: 1 }]);
    expect(result?.id).toBe('a2');
  });

  it('resolves step at last root index', () => {
    const steps = [mkAction('a1'), mkAction('a2'), mkAction('a3')];
    const result = getStepAtPath(steps, [{ index: 2 }]);
    expect(result?.id).toBe('a3');
  });

  it('resolves condition step at root', () => {
    const steps = [mkCondition('c1', [mkAction('y1')])];
    const result = getStepAtPath(steps, [{ index: 0 }]);
    expect(result?.id).toBe('c1');
    expect(result?.type).toBe('condition');
  });

  it('resolves delay step at root', () => {
    const steps = [mkDelay('d1', 120)];
    const result = getStepAtPath(steps, [{ index: 0 }]);
    expect(result?.id).toBe('d1');
    expect(result?.type).toBe('delay');
    expect(result?.delay!.duration_sec).toBe(120);
  });
});

// ═════════════════════════════════════════════════════════════════════
// getStepAtPath — branch navigation
// ═════════════════════════════════════════════════════════════════════
describe('getStepAtPath — branch navigation', () => {
  it('navigates into yes_steps[0]', () => {
    const steps = [mkCondition('c1', [mkAction('y1'), mkAction('y2')])];
    const result = getStepAtPath(steps, [{ index: 0 }, { branch: 'yes', index: 0 }]);
    expect(result?.id).toBe('y1');
  });

  it('navigates into yes_steps[1]', () => {
    const steps = [mkCondition('c1', [mkAction('y1'), mkAction('y2')])];
    const result = getStepAtPath(steps, [{ index: 0 }, { branch: 'yes', index: 1 }]);
    expect(result?.id).toBe('y2');
  });

  it('navigates into no_steps[0]', () => {
    const steps = [mkCondition('c1', [], [mkAction('n1')])];
    const result = getStepAtPath(steps, [{ index: 0 }, { branch: 'no', index: 0 }]);
    expect(result?.id).toBe('n1');
  });

  it('navigates yes and no independently', () => {
    const steps = [mkCondition('c1', [mkAction('y1')], [mkAction('n1')])];
    const yes = getStepAtPath(steps, [{ index: 0 }, { branch: 'yes', index: 0 }]);
    const no = getStepAtPath(steps, [{ index: 0 }, { branch: 'no', index: 0 }]);
    expect(yes?.id).toBe('y1');
    expect(no?.id).toBe('n1');
  });
});

// ═════════════════════════════════════════════════════════════════════
// getStepAtPath — deep nesting (2-3 levels)
// ═════════════════════════════════════════════════════════════════════
describe('getStepAtPath — deep nesting', () => {
  it('navigates depth 2: root → yes → yes', () => {
    const inner = mkCondition('inner', [mkAction('deep')]);
    const outer = mkCondition('outer', [inner]);
    const steps = [outer];

    const result = getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'yes', index: 0 },
    ]);
    expect(result?.id).toBe('deep');
  });

  it('navigates depth 2: root → yes → no', () => {
    const inner = mkCondition('inner', [], [mkAction('deep_no')]);
    const outer = mkCondition('outer', [inner]);
    const steps = [outer];

    const result = getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'no', index: 0 },
    ]);
    expect(result?.id).toBe('deep_no');
  });

  it('navigates depth 3', () => {
    const l3 = mkCondition('L3', [mkAction('bottom')]);
    const l2 = mkCondition('L2', [l3]);
    const l1 = mkCondition('L1', [l2]);
    const steps = [l1];

    const result = getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'yes', index: 0 },
    ]);
    expect(result?.id).toBe('bottom');
  });

  it('navigates mixed branches: yes → no → yes', () => {
    const l3 = mkCondition('L3', [mkAction('target')]);
    const l2 = mkCondition('L2', [], [l3]);
    const l1 = mkCondition('L1', [l2]);
    const steps = [l1];

    const result = getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'no', index: 0 },
      { branch: 'yes', index: 0 },
    ]);
    expect(result?.id).toBe('target');
  });

  it('resolves non-zero indexes at each level', () => {
    const inner = mkCondition('inner', [mkAction('y0'), mkAction('y1'), mkAction('y2')]);
    const outer = mkCondition('outer', [mkAction('skip'), inner]);
    const steps = [mkAction('root0'), outer];

    const result = getStepAtPath(steps, [
      { index: 1 },         // outer (root index 1)
      { branch: 'yes', index: 1 },  // inner (yes index 1)
      { branch: 'yes', index: 2 },  // y2 (yes index 2)
    ]);
    expect(result?.id).toBe('y2');
  });
});

// ═════════════════════════════════════════════════════════════════════
// getStepAtPath — out of bounds & missing
// ═════════════════════════════════════════════════════════════════════
describe('getStepAtPath — out of bounds & missing', () => {
  it('returns undefined for empty path', () => {
    const steps = [mkAction('a1')];
    expect(getStepAtPath(steps, [])).toBeUndefined();
  });

  it('returns undefined for empty steps array', () => {
    expect(getStepAtPath([], [{ index: 0 }])).toBeUndefined();
  });

  it('returns undefined for root index out of bounds', () => {
    const steps = [mkAction('a1')];
    expect(getStepAtPath(steps, [{ index: 5 }])).toBeUndefined();
  });

  it('returns undefined for negative root index', () => {
    const steps = [mkAction('a1'), mkAction('a2')];
    expect(getStepAtPath(steps, [{ index: -1 }])).toBeUndefined();
  });

  it('returns undefined when branch does not exist (no yes_steps)', () => {
    const steps = [mkAction('a1')]; // action has no branches
    expect(getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
    ])).toBeUndefined();
  });

  it('returns undefined when branch child index is out of bounds', () => {
    const steps = [mkCondition('c1', [mkAction('y1')])];
    expect(getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 99 },
    ])).toBeUndefined();
  });

  it('returns undefined when navigating into empty branch', () => {
    const steps = [mkCondition('c1')]; // empty yes/no
    expect(getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
    ])).toBeUndefined();
  });

  it('returns undefined when second segment has no branch', () => {
    const steps = [mkCondition('c1', [mkAction('y1')])];
    // Second segment missing branch field — invalid
    expect(getStepAtPath(steps, [
      { index: 0 },
      { index: 0 },
    ])).toBeUndefined();
  });

  it('returns undefined when path overshoots depth', () => {
    const steps = [mkCondition('c1', [mkAction('y1')])];
    // y1 is an action, has no branches, but path tries to go deeper
    expect(getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'yes', index: 0 },
    ])).toBeUndefined();
  });
});

// ═════════════════════════════════════════════════════════════════════
// getStepAtPath — integration with store
// ═════════════════════════════════════════════════════════════════════
describe('getStepAtPath — integration with store', () => {
  it('resolves a step built via addStep', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkCondition('c1'), null, null);
    useBuilderStore.getState().addStep(mkAction('y1'), 'c1', 'yes');

    const { steps } = useBuilderStore.getState();
    const result = getStepAtPath(steps, [
      { index: 1 },
      { branch: 'yes', index: 0 },
    ]);
    expect(result?.id).toBe('y1');
  });

  it('returns the same step object as findStep for the same target', () => {
    const inner = mkCondition('inner', [mkAction('target')]);
    const outer = mkCondition('outer', [inner]);
    useBuilderStore.getState().addStep(outer, null, null);

    const { steps } = useBuilderStore.getState();
    const byPath = getStepAtPath(steps, [
      { index: 0 },
      { branch: 'yes', index: 0 },
      { branch: 'yes', index: 0 },
    ]);
    const byId = useBuilderStore.getState().findStep('target');

    expect(byPath).toBe(byId);
  });
});

// ═════════════════════════════════════════════════════════════════════
// getParentPath — basic cases
// ═════════════════════════════════════════════════════════════════════
describe('getParentPath — basic cases', () => {
  it('parent of root step is empty path', () => {
    const result = getParentPath([{ index: 0 }]);
    expect(result).toEqual([]);
  });

  it('parent of root step at index 2 is empty path', () => {
    const result = getParentPath([{ index: 2 }]);
    expect(result).toEqual([]);
  });

  it('parent of yes branch child is the condition', () => {
    const result = getParentPath([{ index: 0 }, { branch: 'yes', index: 1 }]);
    expect(result).toEqual([{ index: 0 }]);
  });

  it('parent of no branch child is the condition', () => {
    const result = getParentPath([{ index: 1 }, { branch: 'no', index: 0 }]);
    expect(result).toEqual([{ index: 1 }]);
  });

  it('parent of depth-2 step is the depth-1 condition', () => {
    const path = [
      { index: 0 },
      { branch: 'yes' as const, index: 0 },
      { branch: 'yes' as const, index: 2 },
    ];
    const result = getParentPath(path);
    expect(result).toEqual([
      { index: 0 },
      { branch: 'yes', index: 0 },
    ]);
  });

  it('parent of depth-3 step removes last segment', () => {
    const path = [
      { index: 1 },
      { branch: 'no' as const, index: 0 },
      { branch: 'yes' as const, index: 1 },
      { branch: 'no' as const, index: 0 },
    ];
    const result = getParentPath(path);
    expect(result).toEqual([
      { index: 1 },
      { branch: 'no', index: 0 },
      { branch: 'yes', index: 1 },
    ]);
  });
});

// ═════════════════════════════════════════════════════════════════════
// getParentPath — edge cases
// ═════════════════════════════════════════════════════════════════════
describe('getParentPath — edge cases', () => {
  it('returns undefined for empty path', () => {
    expect(getParentPath([])).toBeUndefined();
  });

  it('does not mutate the original path array', () => {
    const path = [
      { index: 0 },
      { branch: 'yes' as const, index: 1 },
      { branch: 'no' as const, index: 0 },
    ];
    const pathCopy = [...path];
    getParentPath(path);
    expect(path).toEqual(pathCopy);
    expect(path).toHaveLength(3);
  });

  it('returned path is a new array (not same reference)', () => {
    const path = [{ index: 0 }, { branch: 'yes' as const, index: 0 }];
    const parent = getParentPath(path);
    expect(parent).not.toBe(path);
  });
});

// ═════════════════════════════════════════════════════════════════════
// getParentPath — composition with getStepAtPath
// ═════════════════════════════════════════════════════════════════════
describe('getParentPath — composition with getStepAtPath', () => {
  it('getStepAtPath(getParentPath(path)) returns the parent step', () => {
    const inner = mkCondition('inner', [mkAction('target')]);
    const outer = mkCondition('outer', [inner]);
    const steps = [outer];

    const targetPath = [
      { index: 0 },
      { branch: 'yes' as const, index: 0 },
      { branch: 'yes' as const, index: 0 },
    ];

    const parentPath = getParentPath(targetPath)!;
    const parent = getStepAtPath(steps, parentPath);
    expect(parent?.id).toBe('inner');
  });

  it('grandparent via double getParentPath', () => {
    const inner = mkCondition('inner', [mkAction('target')]);
    const outer = mkCondition('outer', [inner]);
    const steps = [outer];

    const targetPath = [
      { index: 0 },
      { branch: 'yes' as const, index: 0 },
      { branch: 'yes' as const, index: 0 },
    ];

    const grandparentPath = getParentPath(getParentPath(targetPath)!)!;
    const grandparent = getStepAtPath(steps, grandparentPath);
    expect(grandparent?.id).toBe('outer');
  });

  it('root parent path resolves to undefined with getStepAtPath (empty path)', () => {
    const steps = [mkAction('a1')];
    const rootPath = [{ index: 0 }];
    const parentPath = getParentPath(rootPath)!;

    expect(parentPath).toEqual([]);
    expect(getStepAtPath(steps, parentPath)).toBeUndefined();
  });

  it('works with store-built tree', () => {
    useBuilderStore.getState().addStep(mkCondition('c1'), null, null);
    useBuilderStore.getState().addStep(mkAction('y1'), 'c1', 'yes');
    useBuilderStore.getState().addStep(mkAction('y2'), 'c1', 'yes');

    const { steps } = useBuilderStore.getState();

    const childPath = [{ index: 0 }, { branch: 'yes' as const, index: 1 }];
    const parentPath = getParentPath(childPath)!;

    const child = getStepAtPath(steps, childPath);
    const parent = getStepAtPath(steps, parentPath);

    expect(child?.id).toBe('y2');
    expect(parent?.id).toBe('c1');
  });
});
