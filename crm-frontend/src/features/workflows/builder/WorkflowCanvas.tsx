// The React Flow canvas: renders a workflow (trigger + steps) as an auto-laid-out
// graph. Steps can be reordered by dragging a node up/down within its sibling list;
// structure is otherwise edited via edge/end "+" buttons and node selection. Nodes
// can't be freely wired.
//
// Uses useNodesState/useEdgesState (not fully-controlled props) so React Flow can
// apply its internal measurement (handle bounds) — without that, edges have no
// endpoints and don't render. The derived graph is synced in via an effect.

import { useCallback, useEffect } from 'react';
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  type NodeMouseHandler,
  type OnNodeDrag,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { TriggerSpec, WorkflowStep } from '../types';
import { stepsToGraph, reorderTargetIndex, type BuilderNode, type BuilderEdge } from './graph';
import { findStepLocation } from '../store';
import { nodeTypes } from './nodes';
import { edgeTypes } from './InsertEdge';

interface Props {
  trigger: TriggerSpec | null;
  steps: WorkflowStep[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  /** Reorder a step within its sibling list (drag-to-reorder). Omit to disable drag. */
  onReorder?: (parentId: string | null, branch: 'yes' | 'no' | null, fromIdx: number, toIdx: number) => void;
  /** Set false to disable dragging (e.g. the read-only mobile canvas). */
  canDrag?: boolean;
}

// Only real steps are draggable — the trigger is fixed at the top and 'end' nodes
// are add-step affordances.
const DRAGGABLE_KINDS = new Set(['action', 'delay', 'condition']);

export function WorkflowCanvas({ trigger, steps, selectedId, onSelect, onReorder, canDrag = true }: Props) {
  const [nodes, setNodes, onNodesChange] = useNodesState<BuilderNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<BuilderEdge>([]);
  const dragEnabled = canDrag && !!onReorder;

  // Rebuild the graph whenever the workflow structure changes.
  useEffect(() => {
    const g = stepsToGraph(trigger, steps);
    setNodes(g.nodes.map((n) => ({
      ...n,
      draggable: dragEnabled && DRAGGABLE_KINDS.has(n.type as string),
      connectable: false,
    })));
    setEdges(g.edges);
  }, [trigger, steps, dragEnabled, setNodes, setEdges]);

  // Reflect selection without rebuilding the graph (keeps measured bounds).
  useEffect(() => {
    setNodes((ns) => ns.map((n) => ({ ...n, selected: n.id === selectedId })));
  }, [selectedId, setNodes]);

  const onNodeClick: NodeMouseHandler = (_e, node) => {
    if (node.type === 'end' || node.type === 'trigger') {
      onSelect(node.type);
      return;
    }
    onSelect(node.id);
  };

  // Drag-to-reorder: on drop, map the node's Y to a new index among its siblings and
  // reorder in the tree. The graph re-lays-out from steps, snapping the node to its
  // correct position — including a no-op drag, which snaps straight back.
  const onNodeDragStop: OnNodeDrag<BuilderNode> = useCallback(
    (_e, node) => {
      if (!onReorder) return;
      const loc = findStepLocation(steps, node.id);
      if (!loc) return;
      const droppedY = node.position.y;
      const siblingYs = loc.siblingIds
        .filter((sid) => sid !== node.id)
        .map((sid) => nodes.find((n) => n.id === sid)?.position.y ?? 0);
      const toIdx = reorderTargetIndex(droppedY, siblingYs);
      if (toIdx === loc.index) {
        // Order unchanged — snap the node back to its laid-out home.
        const home = stepsToGraph(trigger, steps).nodes.find((n) => n.id === node.id)?.position;
        if (home) setNodes((ns) => ns.map((n) => (n.id === node.id ? { ...n, position: home } : n)));
        return;
      }
      onReorder(loc.parentId, loc.branch, loc.index, toIdx);
    },
    [onReorder, steps, trigger, nodes, setNodes],
  );

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      onNodeClick={onNodeClick}
      onNodeDragStop={onNodeDragStop}
      nodesConnectable={false}
      elementsSelectable
      fitView
      fitViewOptions={{ padding: 0.25, maxZoom: 1 }}
      proOptions={{ hideAttribution: true }}
      minZoom={0.3}
      maxZoom={1.5}
      className="bg-background"
    >
      <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="hsl(var(--border))" />
      <Controls showInteractive={false} className="!border-border !bg-card" />
      <MiniMap pannable zoomable className="!border-border !bg-card" maskColor="hsl(var(--muted) / 0.6)" />
    </ReactFlow>
  );
}
