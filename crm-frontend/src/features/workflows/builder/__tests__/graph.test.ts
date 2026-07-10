import { describe, it, expect } from 'vitest';
import { stepsToGraph } from '../graph';
import type { WorkflowStep, TriggerSpec } from '../../types';

const trigger: TriggerSpec = { type: 'contact_created', params: {} };

function action(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'send_email', params: {} } };
}
function delay(id: string): WorkflowStep {
  return { id, type: 'delay', delay: { duration_sec: 60 } };
}
function condition(id: string, yes: WorkflowStep[], no: WorkflowStep[]): WorkflowStep {
  return { id, type: 'condition', condition: { op: 'AND', rules: [] }, yes_steps: yes, no_steps: no };
}

// Helper: does an edge exist from source to target?
function hasEdge(edges: { source: string; target: string }[], source: string, target: string): boolean {
  return edges.some((e) => e.source === source && e.target === target);
}

describe('stepsToGraph', () => {
  it('always emits a single trigger node', () => {
    const { nodes } = stepsToGraph(trigger, []);
    const triggers = nodes.filter((n) => n.data.kind === 'trigger');
    expect(triggers).toHaveLength(1);
    expect(triggers[0].id).toBe('trigger');
    expect(triggers[0].data.trigger).toEqual(trigger);
  });

  it('chains a linear list top-to-bottom from the trigger', () => {
    const { nodes, edges } = stepsToGraph(trigger, [action('a1'), delay('d1'), action('a2')]);
    const stepIds = nodes.filter((n) => n.data.kind !== 'end').map((n) => n.id).sort();
    expect(stepIds).toEqual(['a1', 'a2', 'd1', 'trigger']);
    expect(hasEdge(edges, 'trigger', 'a1')).toBe(true);
    expect(hasEdge(edges, 'a1', 'd1')).toBe(true);
    expect(hasEdge(edges, 'd1', 'a2')).toBe(true);
    // One open tail becomes an 'end' node; its edge carries the top-level insert.
    const endNodes = nodes.filter((n) => n.data.kind === 'end');
    expect(endNodes).toHaveLength(1);
    const endEdge = edges.find((e) => e.source === 'a2' && e.target === endNodes[0].id);
    expect(endEdge?.data?.insert).toEqual({ parentId: null, branch: null, index: 3 });
  });

  it('lays the Yes branch to the LEFT of the No branch', () => {
    const { nodes } = stepsToGraph(trigger, [condition('c1', [action('y1')], [action('n1')])]);
    const x = (id: string) => nodes.find((n) => n.id === id)!.position.x;
    expect(x('y1')).toBeLessThan(x('n1'));
  });

  it('keeps Yes-left ordering for a nested condition', () => {
    const steps = [condition('c1', [condition('c2', [action('yy')], [action('yn')])], [action('n1')])];
    const { nodes } = stepsToGraph(trigger, steps);
    const x = (id: string) => nodes.find((n) => n.id === id)!.position.x;
    // Outer: c2 subtree (Yes) left of n1 (No).
    expect(x('c2')).toBeLessThan(x('n1'));
    // Inner: yy (Yes) left of yn (No).
    expect(x('yy')).toBeLessThan(x('yn'));
  });

  it('labels both tails of a fresh (empty) If/Else so it shows Yes and No branches', () => {
    // The core no-merge requirement: a just-added If/Else forks into two labeled
    // "+ Add step" pills, one per branch, from the moment it exists.
    const { nodes } = stepsToGraph(trigger, [condition('c1', [], [])]);
    const ends = nodes.filter((n) => n.data.kind === 'end');
    const labels = ends.map((n) => n.data.branchLabel).sort();
    expect(ends).toHaveLength(2);
    expect(labels).toEqual(['No', 'Yes']);
  });

  // Defensive: the transform faithfully renders whatever tree it is given, including
  // a legacy "merge" (a step after a condition). The builder no longer PRODUCES these
  // — inserts absorb into a branch and loads auto-split — but the render stays correct.
  it('defensively renders a legacy merge: both branch tails reach the next sibling', () => {
    const steps = [
      condition('c1', [action('y1')], [action('n1')]),
      action('after'),
    ];
    const { edges } = stepsToGraph(trigger, steps);

    expect(hasEdge(edges, 'trigger', 'c1')).toBe(true);
    // Branch entry edges are labeled.
    const yesEdge = edges.find((e) => e.source === 'c1' && e.target === 'y1');
    const noEdge = edges.find((e) => e.source === 'c1' && e.target === 'n1');
    expect(yesEdge?.label).toBe('Yes');
    expect(noEdge?.label).toBe('No');
    // Both branch tails rejoin the following sibling.
    expect(hasEdge(edges, 'y1', 'after')).toBe(true);
    expect(hasEdge(edges, 'n1', 'after')).toBe(true);
  });

  it('defensively renders a legacy merge with an empty branch (condition → next sibling)', () => {
    const steps = [condition('c1', [], [action('n1')]), action('after')];
    const { edges } = stepsToGraph(trigger, steps);
    // Empty Yes branch: the condition connects straight to the next sibling.
    expect(hasEdge(edges, 'c1', 'after')).toBe(true);
    expect(hasEdge(edges, 'c1', 'n1')).toBe(true);
    expect(hasEdge(edges, 'n1', 'after')).toBe(true);
  });

  it('exposes trailing insert slots for both open branch tails when a condition ends the flow', () => {
    const steps = [condition('c1', [action('y1')], [action('n1')])];
    const { nodes, edges } = stepsToGraph(trigger, steps);
    // Two open branch tails → two 'end' nodes, each with a branch-local insert.
    const endIds = nodes.filter((n) => n.data.kind === 'end').map((n) => n.id);
    expect(endIds).toHaveLength(2);
    const inserts = edges
      .filter((e) => endIds.includes(e.target))
      .map((e) => e.data?.insert);
    expect(inserts).toContainEqual({ parentId: 'c1', branch: 'yes', index: 1 });
    expect(inserts).toContainEqual({ parentId: 'c1', branch: 'no', index: 1 });
  });

  it('carries insert context on every edge', () => {
    const { edges } = stepsToGraph(trigger, [action('a1'), action('a2')]);
    const first = edges.find((e) => e.source === 'trigger' && e.target === 'a1');
    expect(first?.data?.insert).toEqual({ parentId: null, branch: null, index: 0 });
    const second = edges.find((e) => e.source === 'a1' && e.target === 'a2');
    expect(second?.data?.insert).toEqual({ parentId: null, branch: null, index: 1 });
  });

  it('gives every node a computed non-overlapping position', () => {
    const { nodes } = stepsToGraph(trigger, [action('a1'), action('a2')]);
    const y = (id: string) => nodes.find((n) => n.id === id)!.position.y;
    // trigger above a1 above a2 (strictly increasing ranks top-to-bottom).
    expect(y('trigger')).toBeLessThan(y('a1'));
    expect(y('a1')).toBeLessThan(y('a2'));
  });

  it('adds an end node so an empty workflow still has a first insert slot', () => {
    const { nodes, edges } = stepsToGraph(trigger, []);
    const endNodes = nodes.filter((n) => n.data.kind === 'end');
    expect(endNodes).toHaveLength(1);
    const edge = edges.find((e) => e.source === 'trigger' && e.target === endNodes[0].id);
    expect(edge?.data?.insert).toEqual({ parentId: null, branch: null, index: 0 });
  });

  it('handles nested conditions', () => {
    const steps = [
      condition('c1', [condition('c2', [action('yy')], [action('yn')])], [action('n1')]),
    ];
    const { nodes, edges } = stepsToGraph(trigger, steps);
    const stepIds = nodes.filter((n) => n.data.kind !== 'end').map((n) => n.id).sort();
    expect(stepIds).toEqual(['c1', 'c2', 'n1', 'trigger', 'yn', 'yy'].sort());
    expect(hasEdge(edges, 'c1', 'c2')).toBe(true);
    expect(hasEdge(edges, 'c2', 'yy')).toBe(true);
    expect(hasEdge(edges, 'c2', 'yn')).toBe(true);
  });
});
