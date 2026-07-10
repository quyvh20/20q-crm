import { describe, it, expect, beforeEach } from 'vitest';
import { useBuilderStore, findStepLocation } from '../store';
import { reorderTargetIndex } from '../builder/graph';
import type { WorkflowStep } from '../types';

// Drag-to-reorder (canvas): locating a dragged step + mapping its drop Y to a target
// index + applying it through reorderSteps. The React Flow drag plumbing fires
// onNodeDragStop; this covers the pure logic underneath it.

const action = (id: string): WorkflowStep => ({ id, type: 'action', action: { id, type: 'create_task', params: {} } });
const tree = (): WorkflowStep[] => [
  action('a'),
  action('b'),
  {
    id: 'cond',
    type: 'condition',
    condition: { op: 'AND', rules: [] },
    yes_steps: [action('y0'), action('y1')],
    no_steps: [action('n0')],
  },
  action('c'),
];

describe('findStepLocation', () => {
  it('locates a top-level step with its siblings', () => {
    expect(findStepLocation(tree(), 'b')).toEqual({ parentId: null, branch: null, index: 1, siblingIds: ['a', 'b', 'cond', 'c'] });
  });
  it('locates a step inside a yes-branch', () => {
    expect(findStepLocation(tree(), 'y1')).toEqual({ parentId: 'cond', branch: 'yes', index: 1, siblingIds: ['y0', 'y1'] });
  });
  it('locates a step inside a no-branch', () => {
    expect(findStepLocation(tree(), 'n0')).toEqual({ parentId: 'cond', branch: 'no', index: 0, siblingIds: ['n0'] });
  });
  it('returns null for an unknown id', () => {
    expect(findStepLocation(tree(), 'nope')).toBeNull();
  });
});

describe('reorderTargetIndex', () => {
  // siblings (excl. dragged) laid out top-to-bottom at these Ys.
  const siblingYs = [0, 100, 300]; // e.g. a, cond, c after dragging b out
  it('drop above all siblings → index 0', () => {
    expect(reorderTargetIndex(-10, siblingYs)).toBe(0);
  });
  it('drop between first and second → index 1', () => {
    expect(reorderTargetIndex(150, siblingYs)).toBe(2); // above 0 and 100
  });
  it('drop below all siblings → last index', () => {
    expect(reorderTargetIndex(400, siblingYs)).toBe(3);
  });
});

describe('drag-reorder applied through the store', () => {
  beforeEach(() => useBuilderStore.getState().reset());

  it('moves a top-level step to a new position', () => {
    useBuilderStore.setState({ steps: tree() });
    // Simulate dragging 'a' (index 0) down past 'b' and 'cond' → drops after them.
    const loc = findStepLocation(useBuilderStore.getState().steps, 'a')!;
    // siblings excluding 'a' are b, cond, c at (say) Y 100, 200, 300; drop at 250.
    const toIdx = reorderTargetIndex(250, [100, 200, 300]);
    useBuilderStore.getState().reorderSteps(loc.parentId, loc.branch, loc.index, toIdx);
    expect(useBuilderStore.getState().steps.map((s) => s.id)).toEqual(['b', 'cond', 'a', 'c']);
  });

  it('reorders within a branch only, leaving siblings elsewhere untouched', () => {
    useBuilderStore.setState({ steps: tree() });
    const loc = findStepLocation(useBuilderStore.getState().steps, 'y0')!;
    expect(loc).toMatchObject({ parentId: 'cond', branch: 'yes', index: 0 });
    // Drop y0 below y1 (only one other sibling, at Y 0) → toIdx 1.
    const toIdx = reorderTargetIndex(50, [0]);
    useBuilderStore.getState().reorderSteps(loc.parentId, loc.branch, loc.index, toIdx);
    const cond = useBuilderStore.getState().steps.find((s) => s.id === 'cond')!;
    expect(cond.yes_steps!.map((s) => s.id)).toEqual(['y1', 'y0']);
    // top level untouched
    expect(useBuilderStore.getState().steps.map((s) => s.id)).toEqual(['a', 'b', 'cond', 'c']);
  });
});
