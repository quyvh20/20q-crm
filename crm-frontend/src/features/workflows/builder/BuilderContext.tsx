import { createContext, useContext } from 'react';
import type { InsertContext } from './graph';
import type { TestRunStep } from '../types';
import type { CanvasIssues } from './issueMap';

// Dry-run overlay state (A3.5): the last test run's per-step outcomes keyed by step
// id, plus the top-level condition gate and the sample record's label. Null when no
// dry run is active.
export interface DryRunState {
  byStep: Record<string, TestRunStep>;
  conditionResult: boolean;
  sampleLabel: string;
}

// An in-flight drag from the palette sidebar. Carried in context (not in the
// HTML5 dataTransfer) because dataTransfer payloads are unreadable during
// dragover, and the edge/end drop targets render inside React Flow's layers with
// no prop channel from the palette.
export interface PaletteDrag {
  kind: 'step' | 'trigger';
  /** Step type ("send_email", "condition", …) or palette trigger item id. */
  type: string;
}

// Interaction callbacks the canvas nodes/edges call, provided by the builder page.
// Kept in context so the pure graph transform stays free of handlers.
export interface BuilderActions {
  /** User clicked a "+" insert slot on an edge. */
  onInsert: (slot: InsertContext, anchor?: { x: number; y: number }) => void;
  /** User selected a node (opens the config panel). */
  onSelect: (nodeId: string) => void;
  selectedId: string | null;
  /** Read-only canvas (e.g. preview) disables insert/select affordances. */
  readOnly?: boolean;
  /** Active dry-run overlay, or null. Nodes tint run/skip from this. */
  dryRun?: DryRunState | null;
  /** Delete a step from a node's kebab menu. */
  onDelete?: (stepId: string) => void;
  /** Duplicate an action/delay step from its kebab menu. */
  onDuplicate?: (stepId: string) => void;
  /** Validation issues mapped to canvas nodes — nodes badge themselves from this. */
  issues?: CanvasIssues | null;
  /** Palette drag in flight — insert slots / trigger node highlight as drop targets. */
  dragging?: PaletteDrag | null;
  /** A palette STEP item was dropped on an insert slot. */
  onDropStep?: (slot: InsertContext) => void;
  /** A palette TRIGGER item was dropped on the trigger node. */
  onDropTrigger?: () => void;
}

const noop = () => {};
export const BuilderContext = createContext<BuilderActions>({
  onInsert: noop,
  onSelect: noop,
  selectedId: null,
  readOnly: true,
  dryRun: null,
  dragging: null,
});

export function useBuilderActions(): BuilderActions {
  return useContext(BuilderContext);
}
