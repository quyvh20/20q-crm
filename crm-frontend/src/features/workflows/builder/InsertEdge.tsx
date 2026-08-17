// Custom edges that draw the connector and a centered "+" insert button. The
// button is the click path for adding steps (no free-form wiring); during a
// palette drag it turns into a highlighted drop target. Edges are smooth-step
// (Brevo-style orthogonal lines with rounded elbows) with an arrowhead set by
// the graph transform.

import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react';
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
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 12,
  });
  const { onInsert, readOnly, dragging, onDropStep } = useBuilderActions();
  const droppable = !readOnly && dragging?.kind === 'step' && !!data?.insert;

  return (
    <>
      <BaseEdge path={path} markerEnd={markerEnd} style={{ stroke: EDGE_STROKE, strokeWidth: 1.5 }} />
      <EdgeLabelRenderer>
        {label && (
          <div
            className="nodrag nopan absolute rounded-md border border-border bg-card px-2 py-0.5 text-[10px] font-bold uppercase tracking-wide text-foreground shadow-sm"
            style={{ transform: `translate(-50%,-50%) translate(${labelX}px,${labelY - 20}px)`, pointerEvents: 'none' }}
          >
            {label}
          </div>
        )}
        {/* The "+" lives inside a fixed-position wrapper so hover styling on the button
            (scale/color) can never shift where it sits — it stays pinned on the line.
            During a palette drag the wrapper grows (p-2) into a forgiving drop zone. */}
        {!readOnly && (
          <div
            className={`nodrag nopan absolute ${droppable ? 'p-2' : ''}`}
            style={{ transform: `translate(-50%,-50%) translate(${labelX}px,${labelY}px)`, pointerEvents: 'all' }}
            onDragOver={
              droppable
                ? (e) => {
                    e.preventDefault();
                    e.dataTransfer.dropEffect = 'copy';
                  }
                : undefined
            }
            onDrop={
              droppable
                ? (e) => {
                    e.preventDefault();
                    if (data?.insert) onDropStep?.(data.insert);
                  }
                : undefined
            }
          >
            <button
              type="button"
              title="Add step"
              aria-label="Add step"
              onClick={(e) => {
                e.stopPropagation();
                if (data?.insert) onInsert(data.insert, { x: e.clientX, y: e.clientY });
              }}
              className={`flex items-center justify-center rounded-full border-2 shadow-sm transition-colors ${
                droppable
                  ? 'h-8 w-8 animate-pulse border-primary bg-primary/10 text-primary'
                  : 'h-7 w-7 border-border bg-card text-muted-foreground hover:border-primary hover:bg-primary hover:text-primary-foreground'
              }`}
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
  const [path] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 12 });
  return <BaseEdge path={path} markerEnd={markerEnd} style={{ stroke: EDGE_STROKE, strokeWidth: 1.5, strokeDasharray: '5 5' }} />;
}

export const edgeTypes = { insert: InsertEdge, plain: PlainEdge };
