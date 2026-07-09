// The React Flow canvas: renders a workflow (trigger + steps) as an auto-laid-out
// graph. Structure is edited via edge/end "+" buttons and node selection — nodes
// are not draggable and can't be freely wired.
//
// Uses useNodesState/useEdgesState (not fully-controlled props) so React Flow can
// apply its internal measurement (handle bounds) — without that, edges have no
// endpoints and don't render. The derived graph is synced in via an effect.

import { useEffect } from 'react';
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  type NodeMouseHandler,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import type { TriggerSpec, WorkflowStep } from '../types';
import { stepsToGraph, type BuilderNode, type BuilderEdge } from './graph';
import { nodeTypes } from './nodes';
import { edgeTypes } from './InsertEdge';

interface Props {
  trigger: TriggerSpec | null;
  steps: WorkflowStep[];
  selectedId: string | null;
  onSelect: (id: string) => void;
}

export function WorkflowCanvas({ trigger, steps, selectedId, onSelect }: Props) {
  const [nodes, setNodes, onNodesChange] = useNodesState<BuilderNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<BuilderEdge>([]);

  // Rebuild the graph whenever the workflow structure changes.
  useEffect(() => {
    const g = stepsToGraph(trigger, steps);
    setNodes(g.nodes.map((n) => ({ ...n, draggable: false, connectable: false })));
    setEdges(g.edges);
  }, [trigger, steps, setNodes, setEdges]);

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

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      nodeTypes={nodeTypes}
      edgeTypes={edgeTypes}
      onNodeClick={onNodeClick}
      nodesDraggable={false}
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
