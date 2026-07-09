// Custom edge that draws the connector and a centered "+" insert button. The
// button is the sole way to add steps (no free-form wiring), so it's always
// present and grows on hover.

import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';
import { Plus } from 'lucide-react';
import type { BuilderEdge } from './graph';
import { useBuilderActions } from './BuilderContext';

export function InsertEdge({
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  label,
  data,
}: EdgeProps<BuilderEdge>) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
  });
  const { onInsert, readOnly } = useBuilderActions();

  return (
    <>
      <BaseEdge path={path} markerEnd={markerEnd} style={{ stroke: 'hsl(var(--border))', strokeWidth: 1.5 }} />
      <EdgeLabelRenderer>
        {label && (
          <div
            className="nodrag nopan absolute -translate-x-1/2 -translate-y-1/2 rounded-md bg-muted px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground"
            style={{ transform: `translate(-50%,-50%) translate(${labelX}px,${labelY - 14}px)`, pointerEvents: 'none' }}
          >
            {label}
          </div>
        )}
        {!readOnly && (
          <button
            type="button"
            title="Add step"
            aria-label="Add step"
            onClick={(e) => {
              e.stopPropagation();
              if (data?.insert) onInsert(data.insert, { x: e.clientX, y: e.clientY });
            }}
            className="nodrag nopan absolute flex h-5 w-5 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full border border-border bg-background text-muted-foreground opacity-70 shadow-sm transition-all hover:scale-125 hover:border-ring hover:text-foreground hover:opacity-100"
            style={{ transform: `translate(-50%,-50%) translate(${labelX}px,${labelY}px)`, pointerEvents: 'all' }}
          >
            <Plus className="h-3 w-3" />
          </button>
        )}
      </EdgeLabelRenderer>
    </>
  );
}

// PlainEdge is a connector with no midpoint "+": used for edges into 'end' nodes,
// where the End node itself renders the "+ Add step" affordance.
export function PlainEdge({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd }: EdgeProps<BuilderEdge>) {
  const [path] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });
  return <BaseEdge path={path} markerEnd={markerEnd} style={{ stroke: 'hsl(var(--border))', strokeWidth: 1.5, strokeDasharray: '4 4' }} />;
}

export const edgeTypes = { insert: InsertEdge, plain: PlainEdge };
