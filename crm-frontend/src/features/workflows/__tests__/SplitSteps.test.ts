import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore, normalizeMergesInList } from '../store';
import { firstOpenSlot, findStepById } from '../builder/catalog';
import { stepsToGraph } from '../builder/graph';
import type { WorkflowStep } from '../types';

// ── Helpers ──────────────────────────────────────────────────────────
function mkAction(id: string): WorkflowStep {
  return {
    id,
    type: 'action',
    action: { id, type: 'create_task', params: { title: `Task ${id}` } },
  };
}

function mkSplit(id: string, percent = 50, yes: WorkflowStep[] = [], no: WorkflowStep[] = []): WorkflowStep {
  return { id, type: 'split', split: { percent_a: percent }, yes_steps: yes, no_steps: no };
}

beforeEach(() => {
  useBuilderStore.getState().reset();
});

// ═════════════════════════════════════════════════════════════════════
// Insert & absorb — a split is a fork: trailing siblings move into A
// ═════════════════════════════════════════════════════════════════════
describe('split — insert & absorb', () => {
  it('absorbs trailing siblings into the A branch on insert', () => {
    useBuilderStore.getState().addStep(mkAction('a1'), null, null);
    useBuilderStore.getState().addStep(mkAction('a2'), null, null);

    useBuilderStore.getState().addStep(mkSplit('sp'), null, null, 0);

    const s = useBuilderStore.getState();
    expect(s.steps.map((x) => x.id)).toEqual(['sp']);
    expect(s.steps[0].yes_steps!.map((x) => x.id)).toEqual(['a1', 'a2']);
    expect(s.steps[0].no_steps).toEqual([]);
  });

  it('refuses a split insert whose absorbed tail would exceed max depth', () => {
    // Chain of 5 nested splits — subtree depth 5, valid.
    let deep = mkSplit('s5');
    for (let i = 4; i >= 1; i--) deep = mkSplit(`s${i}`, 50, [deep]);
    useBuilderStore.getState().addStep(deep, null, null);

    useBuilderStore.getState().addStep(mkSplit('newSp'), null, null, 0);

    const s = useBuilderStore.getState();
    expect(s.steps.map((x) => x.id)).toEqual(['s1']); // unchanged
    expect(s.errors.depth).toBeDefined();
  });
});

// ═════════════════════════════════════════════════════════════════════
// Fork invariants — duplicate refusal, terminal backstop, percent bounds
// ═════════════════════════════════════════════════════════════════════
describe('split — fork invariants', () => {
  it('refuses to duplicate a split', () => {
    useBuilderStore.getState().addStep(mkSplit('sp'), null, null);
    useBuilderStore.getState().duplicateStep('sp');
    expect(useBuilderStore.getState().steps).toHaveLength(1);
  });

  it('validate() flags a non-terminal split', () => {
    useBuilderStore.setState({
      name: 'wf',
      trigger: { type: 'contact_created', params: {} },
      steps: [mkSplit('sp'), mkAction('after')],
    });
    useBuilderStore.getState().validate();
    expect(useBuilderStore.getState().errors['step.sp']?.[0]).toMatch(/last step in its path/);
  });

  it('validate() flags an out-of-range split percentage', () => {
    useBuilderStore.setState({
      name: 'wf',
      trigger: { type: 'contact_created', params: {} },
      steps: [mkSplit('sp', 0, [mkAction('a1')])],
    });
    useBuilderStore.getState().validate();
    expect(useBuilderStore.getState().errors['step.sp']?.[0]).toMatch(/between 1 and 99/);
  });

  it('validate() accepts a well-formed split workflow', () => {
    useBuilderStore.setState({
      name: 'wf',
      trigger: { type: 'contact_created', params: {} },
    });
    // Insert via addStep so the flat actions projection is derived like real flows.
    useBuilderStore.getState().addStep(mkSplit('sp', 30, [mkAction('a1')], [mkAction('b1')]), null, null);
    const ok = useBuilderStore.getState().validate();
    expect(ok).toBe(true);
  });
});

// ═════════════════════════════════════════════════════════════════════
// Normalize on load — steps after a split are copied into both branches
// ═════════════════════════════════════════════════════════════════════
describe('split — merge normalization', () => {
  it('copies post-split siblings into both branches', () => {
    const { steps, changed } = normalizeMergesInList([mkSplit('sp'), mkAction('after')]);
    expect(changed).toBe(true);
    expect(steps.map((s) => s.id)).toEqual(['sp']);
    const sp = steps[0];
    expect(sp.yes_steps).toHaveLength(1);
    expect(sp.no_steps).toHaveLength(1);
    // Fresh ids on the copies (backend rejects duplicates).
    expect(sp.yes_steps![0].id).not.toBe('after');
    expect(sp.no_steps![0].id).not.toBe('after');
    expect(sp.yes_steps![0].id).not.toBe(sp.no_steps![0].id);
  });
});

// ═════════════════════════════════════════════════════════════════════
// Palette helpers
// ═════════════════════════════════════════════════════════════════════
describe('split — palette helpers', () => {
  it('firstOpenSlot descends into a terminal split A branch', () => {
    const steps = [mkSplit('sp', 50, [mkAction('a1')])];
    expect(firstOpenSlot(steps)).toEqual({ parentId: 'sp', branch: 'yes', index: 1 });
  });

  it('findStepById sees into split branches', () => {
    const steps = [mkSplit('sp', 50, [mkAction('a1')], [mkAction('b1')])];
    expect(findStepById(steps, 'b1')?.id).toBe('b1');
  });
});

// ═════════════════════════════════════════════════════════════════════
// Graph — fan-out labels + A-left layout
// ═════════════════════════════════════════════════════════════════════
describe('split — graph', () => {
  it('fans out with A/B percentage labels and split node kind', () => {
    const { nodes, edges } = stepsToGraph(
      { type: 'contact_created', params: {} },
      [mkSplit('sp', 60, [mkAction('a1')], [mkAction('b1')])],
    );
    const splitNode = nodes.find((n) => n.id === 'sp');
    expect(splitNode?.type).toBe('split');
    expect(splitNode?.data.kind).toBe('split');

    const labels = edges.filter((e) => e.source === 'sp').map((e) => e.label).sort();
    expect(labels).toEqual(['A · 60%', 'B · 40%']);
  });

  it('lays the A branch left of the B branch', () => {
    const { nodes } = stepsToGraph(
      { type: 'contact_created', params: {} },
      [mkSplit('sp', 50, [mkAction('a1')], [mkAction('b1')])],
    );
    const ax = nodes.find((n) => n.id === 'a1')!.position.x;
    const bx = nodes.find((n) => n.id === 'b1')!.position.x;
    expect(ax).toBeLessThan(bx);
  });

  it('caps empty split branches with labeled end pills', () => {
    const { nodes } = stepsToGraph({ type: 'contact_created', params: {} }, [mkSplit('sp', 25)]);
    const ends = nodes.filter((n) => n.type === 'end');
    expect(ends).toHaveLength(2);
    const labels = ends.map((n) => n.data.branchLabel).sort();
    expect(labels).toEqual(['A · 25%', 'B · 75%']);
  });
});
