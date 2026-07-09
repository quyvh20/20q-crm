import { createContext, useContext } from 'react';
import type { InsertContext } from './graph';
import type { TestRunStep } from '../types';

// Dry-run overlay state (A3.5): the last test run's per-step outcomes keyed by step
// id, plus the top-level condition gate and the sample record's label. Null when no
// dry run is active.
export interface DryRunState {
  byStep: Record<string, TestRunStep>;
  conditionResult: boolean;
  sampleLabel: string;
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
}

const noop = () => {};
export const BuilderContext = createContext<BuilderActions>({
  onInsert: noop,
  onSelect: noop,
  selectedId: null,
  readOnly: true,
  dryRun: null,
});

export function useBuilderActions(): BuilderActions {
  return useContext(BuilderContext);
}
