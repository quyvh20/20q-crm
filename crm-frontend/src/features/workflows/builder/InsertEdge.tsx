// Custom edge that draws the connector and a centered "+" insert button. The button
// is the sole way to add steps (no free-form wiring). It sits in a fixed-position
// wrapper and only changes colour on hover, so it stays pinned on the line.

import { BaseEdge, EdgeLabelRenderer, getBezierPath, type EdgeProps } from '@xyflow/react';
import { Plus } from 'lucide-react';
import type { BuilderEdge } from './graph';
import { useBuilderActions } from './BuilderContext';

// A clearly-visible connector colour (the design-token `--border` is nearly invisible
// against the canvas). `--muted-foreground` at partial opacity reads as a solid grey
// line in both light and dark themes.
const EDGE_STROKE = 'hsl(var(--muted-foreground) / 0.55)';

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
      <BaseEdge path={path} markerEnd={markerEnd} style={{ stroke: EDGE_STROKE, strokeWidth: 2 }} />
      <EdgeLabelRenderer>
        {label && (
          <div
            className="nodrag nopan absolute rounded bg-muted px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground"
            style={{ transform: `translate(-50%,-50%) translate(${labelX}px,${labelY - 18}px)`, pointerEvents: 'none' }}
          >
            {label}
          </div>
        )}
        {/* The "+" lives inside a fixed-position wrapper so hover styling on the button
            (scale/color) can never shift where it sits — it stays pinned on the line. */}
        {!readOnly && (
          <div
            className="nodrag nopan absolute"
            style={{ transform: `translate(-50%,-50%) translate(${labelX}px,${labelY}px)`, pointerEvents: 'all' }}
          >
            <button
              type="button"
              title="Add step"
              aria-label="Add step"
              onClick={(e) => {
                e.stopPropagation();
                if (data?.insert) onInsert(data.insert, { x: e.clientX, y: e.clientY });
              }}
              className="flex h-7 w-7 items-center justify-center rounded-full border-2 border-border bg-card text-muted-foreground shadow-sm transition-colors hover:border-primary hover:bg-primary hover:text-primary-foreground"
            >
              <Plus className="h-4 w-4" strokeWidth={2.5} />
            </button>
          </div>
        )}
      </EdgeLabelRenderer>
    </>
  );
}

// PlainEdge is a connector with no midpoint "+": used for edges into 'end' nodes,
// where the End node itself renders the "+ Add step" affordance.
export function PlainEdge({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd }: EdgeProps<BuilderEdge>) {
  const [path] = getBezierPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition });
  return <BaseEdge path={path} markerEnd={markerEnd} style={{ stroke: EDGE_STROKE, strokeWidth: 2, strokeDasharray: '5 5' }} />;
}

export const edgeTypes = { insert: InsertEdge, plain: PlainEdge };
