// Pure transform: a workflow (trigger + steps tree) → React Flow nodes/edges,
// laid out top-down with dagre. Kept free of React and store imports so it can be
// unit-tested in isolation — the transform is the load-bearing core of the new
// builder, so it's tested hard.

import Dagre from '@dagrejs/dagre';
import type { Node, Edge } from '@xyflow/react';
import type { TriggerSpec, WorkflowStep } from '../types';

/**
 * Target index for a step dropped at `droppedY` among its siblings' Y positions
 * (siblingYs must EXCLUDE the dragged step). Returns how many siblings sit above the
 * drop point, which is exactly the arrayMove(from, to) target used by reorderSteps.
 */
export function reorderTargetIndex(droppedY: number, siblingYs: number[]): number {
  return siblingYs.reduce((count, y) => count + (y < droppedY ? 1 : 0), 0);
}

export const NODE_WIDTH = 280;
export const NODE_HEIGHTS: Record<BuilderNodeKind, number> = {
  trigger: 84,
  action: 76,
  delay: 64,
  condition: 64,
  end: 36,
};

// 'end' is a ghost terminal node rendered as a "+ Add step" pill at every open
// path tail, so adding a step (including the first step of an empty workflow) is
// the same edge-"+" gesture everywhere.
export type BuilderNodeKind = 'trigger' | 'action' | 'condition' | 'delay' | 'end';

export interface BuilderNodeData {
  kind: BuilderNodeKind;
  /** The step this node renders (absent for the trigger/end nodes). */
  step?: WorkflowStep;
  /** The trigger spec (only on the trigger node). */
  trigger?: TriggerSpec;
  /** Insert slot (only on 'end' nodes — the trailing "+ Add step"). */
  insert?: InsertContext;
  /** Yes/No badge on an 'end' node that caps an open branch. */
  branchLabel?: string;
  [key: string]: unknown;
}

export type BuilderNode = Node<BuilderNodeData>;

/** An insert slot on an edge: where a new step would go if the user clicks "+". */
export interface InsertContext {
  /** Parent condition step id, or null for the top-level list. */
  parentId: string | null;
  /** Which branch of the parent condition, or null at the top level. */
  branch: 'yes' | 'no' | null;
  /** Index within that list to insert at. */
  index: number;
}

export interface BuilderEdgeData {
  insert: InsertContext;
  [key: string]: unknown;
}

export type BuilderEdge = Edge<BuilderEdgeData>;

// A pending connection point whose downstream edge hasn't been drawn yet.
interface Pending {
  source: string;
  label?: string; // "Yes" / "No" on the first edge out of a condition branch
  insert: InsertContext;
}

/**
 * stepsToGraph converts a workflow into a laid-out graph. Sequential steps chain
 * top-to-bottom; a condition fans out to Yes/No branches whose tails rejoin the
 * next sibling (the engine executes siblings after a condition's branches — this
 * is what the old vertical builder couldn't render).
 */
export function stepsToGraph(trigger: TriggerSpec | null, steps: WorkflowStep[]): {
  nodes: BuilderNode[];
  edges: BuilderEdge[];
} {
  const nodes: BuilderNode[] = [];
  const edges: BuilderEdge[] = [];

  const triggerId = 'trigger';
  nodes.push({
    id: triggerId,
    type: 'trigger',
    position: { x: 0, y: 0 },
    data: { kind: 'trigger', trigger: trigger ?? undefined },
  });

  const addEdge = (
    source: string,
    target: string,
    insert: InsertContext,
    label?: string,
    type: 'insert' | 'plain' = 'insert',
  ) => {
    edges.push({
      id: `${source}=>${target}`,
      source,
      target,
      type,
      label,
      data: { insert },
    });
  };

  // Processes one ordered list of steps, wiring `incoming` pending points into the
  // first node and returning the pending points that exit the list.
  const processList = (
    list: WorkflowStep[],
    incoming: Pending[],
    parentId: string | null,
    branch: 'yes' | 'no' | null,
  ): Pending[] => {
    let pending = incoming;

    list.forEach((step, i) => {
      const kind = step.type as BuilderNodeKind;
      nodes.push({
        id: step.id,
        type: step.type,
        position: { x: 0, y: 0 },
        data: { kind, step },
      });

      // Connect everything pending into this step.
      for (const p of pending) {
        addEdge(p.source, step.id, { parentId, branch, index: i }, p.label);
      }

      if (step.type === 'condition') {
        const yesOut = processList(
          step.yes_steps ?? [],
          [{ source: step.id, label: 'Yes', insert: { parentId: step.id, branch: 'yes', index: 0 } }],
          step.id,
          'yes',
        );
        const noOut = processList(
          step.no_steps ?? [],
          [{ source: step.id, label: 'No', insert: { parentId: step.id, branch: 'no', index: 0 } }],
          step.id,
          'no',
        );
        // A condition forks into Yes/No and the branches never rejoin: the builder
        // forbids steps after a condition (store insert-absorb + normalize-on-load),
        // so each branch tail flows on independently and becomes its own trailing
        // "+ Add step" pill. Keep the Yes/No label on the tail so an EMPTY branch's
        // pill is badged — that's what makes a fresh If/Else show both branches.
        // (A defensively-rendered legacy merge, which should not survive
        // normalization, just yields labeled branch edges — harmless.)
        pending = [...yesOut, ...noOut];
      } else {
        pending = [{ source: step.id, insert: { parentId, branch, index: i + 1 } }];
      }
    });

    return pending;
  };

  const trailing = processList(
    steps,
    [{ source: triggerId, insert: { parentId: null, branch: null, index: 0 } }],
    null,
    null,
  );

  // Every open tail becomes a ghost "end" node that renders the trailing
  // "+ Add step" pill (carrying its insert slot). Its incoming edge is a plain
  // connector, so the add affordance isn't duplicated by a midpoint button.
  trailing.forEach((p, i) => {
    const endId = `end-${i}`;
    nodes.push({
      id: endId,
      type: 'end',
      position: { x: 0, y: 0 },
      data: { kind: 'end', insert: p.insert, branchLabel: p.label },
    });
    addEdge(p.source, endId, p.insert, p.label, 'plain');
  });

  const laid = layout(nodes, edges);
  return { nodes: laid, edges };
}

// layout runs dagre and writes top-left positions back onto the nodes (React Flow
// positions are top-left; dagre returns centers).
function layout(nodes: BuilderNode[], edges: BuilderEdge[]): BuilderNode[] {
  const g = new Dagre.graphlib.Graph().setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'TB', nodesep: 48, ranksep: 56 });

  for (const n of nodes) {
    g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHTS[n.data.kind] });
  }
  for (const e of edges) {
    g.setEdge(e.source, e.target);
  }
  Dagre.layout(g);

  // Dagre centers (mutable copy so enforceYesLeft can mirror forks in place).
  const pos = new Map<string, { x: number; y: number }>();
  for (const n of nodes) {
    const p = g.node(n.id);
    if (p) pos.set(n.id, { x: p.x, y: p.y });
  }
  enforceYesLeft(nodes, edges, pos);

  return nodes.map((n) => {
    const p = pos.get(n.id);
    const h = NODE_HEIGHTS[n.data.kind];
    return {
      ...n,
      position: p ? { x: p.x - NODE_WIDTH / 2, y: p.y - h / 2 } : n.position,
    };
  });
}

// A condition's branches must read Yes-left / No-right. Dagre's crossing-minimizer
// picks a side arbitrarily, so after layout we mirror any fork whose Yes subtree
// landed on the right. Conditions are processed outer→inner: a parent mirror also
// flips its nested forks, which are then corrected when each nested condition is
// processed (a mirror around a subtree's own center preserves its footprint, so the
// parent arrangement is untouched).
function enforceYesLeft(
  nodes: BuilderNode[],
  edges: BuilderEdge[],
  pos: Map<string, { x: number; y: number }>,
): void {
  const adj = new Map<string, string[]>();
  for (const e of edges) {
    const list = adj.get(e.source) ?? [];
    list.push(e.target);
    adj.set(e.source, list);
  }
  // All nodes reachable downward from a branch root (branches are disjoint trees
  // once merges are normalized away, so a branch's set includes its own end nodes).
  const reachable = (root: string): string[] => {
    const seen = new Set<string>();
    const stack = [root];
    while (stack.length) {
      const id = stack.pop()!;
      if (seen.has(id)) continue;
      seen.add(id);
      for (const t of adj.get(id) ?? []) stack.push(t);
    }
    return [...seen];
  };
  const meanX = (ids: string[]): number | null => {
    const xs = ids.map((id) => pos.get(id)?.x).filter((x): x is number => x !== undefined);
    return xs.length ? xs.reduce((s, v) => s + v, 0) / xs.length : null;
  };

  // Outer→inner: conditions higher up (smaller y) first.
  const conds = nodes
    .filter((n) => n.data.kind === 'condition')
    .sort((a, b) => (pos.get(a.id)?.y ?? 0) - (pos.get(b.id)?.y ?? 0));

  for (const c of conds) {
    const out = edges.filter((e) => e.source === c.id);
    const yesRoot = out.find((e) => e.label === 'Yes')?.target;
    const noRoot = out.find((e) => e.label === 'No')?.target;
    if (!yesRoot || !noRoot) continue;

    const branchIds = new Set([...reachable(yesRoot), ...reachable(noRoot)]);
    const yc = meanX(reachable(yesRoot));
    const nc = meanX(reachable(noRoot));
    if (yc === null || nc === null || yc <= nc) continue; // already Yes-left (or unclear)

    // Mirror the whole fork around its combined horizontal center to swap the sides.
    const xs = [...branchIds].map((id) => pos.get(id)?.x).filter((x): x is number => x !== undefined);
    const mid = (Math.min(...xs) + Math.max(...xs)) / 2;
    for (const id of branchIds) {
      const p = pos.get(id);
      if (p) p.x = 2 * mid - p.x;
    }
  }
}
