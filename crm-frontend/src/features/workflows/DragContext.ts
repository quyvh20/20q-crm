import { createContext, useContext } from 'react';
import type { StepPath } from './store';

/**
 * DnD context — passes the currently-dragged step's path down to
 * all drop zones so they can compute validity (isDescendant check)
 * and show green/red visual feedback.
 */
export interface DragContextValue {
  /** Path of the step being dragged from canvas, or null if palette/no drag */
  activeDragPath: StepPath | null;
  /** ID of the step being dragged from canvas */
  activeDragStepId: string | null;
}

export const WorkflowDragContext = createContext<DragContextValue>({
  activeDragPath: null,
  activeDragStepId: null,
});

export function useWorkflowDrag() {
  return useContext(WorkflowDragContext);
}
