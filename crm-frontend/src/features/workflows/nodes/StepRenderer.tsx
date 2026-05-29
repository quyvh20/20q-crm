import React from 'react';
import type { WorkflowStep } from '../types';
import type { StepPath } from '../store';
import { ActionNode } from './ActionNode';
import { ConditionSplitNode } from './ConditionSplitNode';
import { DelayNode } from './DelayNode';

interface StepRendererProps {
  step: WorkflowStep;
  path: StepPath;
}

/**
 * Recursive step dispatcher — renders the correct node component
 * based on the step's discriminated `type` field.
 *
 * Wrapped in React.memo with a custom comparator to avoid re-rendering
 * the entire tree on every keystroke in a condition rule or action param.
 */
const StepRendererInner: React.FC<StepRendererProps> = ({ step, path }) => {
  switch (step.type) {
    case 'action':
      return <ActionNode step={step} path={path} />;
    case 'condition':
      return <ConditionSplitNode step={step} path={path} />;
    case 'delay':
      return <DelayNode step={step} path={path} />;
    default:
      // Exhaustiveness guard — should never reach here with valid data
      if (process.env.NODE_ENV !== 'production') {
        console.warn(`[StepRenderer] Unknown step type: "${(step as any).type}"`);
      }
      return null;
  }
};

/**
 * Custom equality check — skips re-render when:
 * - Step identity (id, type) unchanged
 * - Action/delay/condition params unchanged (JSON comparison)
 * - Branch child arrays same length (children have their own memo)
 * - Path segments identical
 */
function arePropsEqual(prev: StepRendererProps, next: StepRendererProps): boolean {
  // Fast path: same reference
  if (prev.step === next.step && prev.path === next.path) return true;

  // Step identity
  if (prev.step.id !== next.step.id || prev.step.type !== next.step.type) return false;

  // Params — JSON compare (these are small objects)
  if (JSON.stringify(prev.step.action) !== JSON.stringify(next.step.action)) return false;
  if (JSON.stringify(prev.step.delay) !== JSON.stringify(next.step.delay)) return false;
  if (JSON.stringify(prev.step.condition) !== JSON.stringify(next.step.condition)) return false;

  // Branch arrays — compare length only (children are memoized separately)
  if ((prev.step.yes_steps?.length ?? 0) !== (next.step.yes_steps?.length ?? 0)) return false;
  if ((prev.step.no_steps?.length ?? 0) !== (next.step.no_steps?.length ?? 0)) return false;

  // Check if any child IDs changed (structural change requires re-render)
  const prevYesIds = prev.step.yes_steps?.map((s) => s.id).join(',') ?? '';
  const nextYesIds = next.step.yes_steps?.map((s) => s.id).join(',') ?? '';
  if (prevYesIds !== nextYesIds) return false;
  const prevNoIds = prev.step.no_steps?.map((s) => s.id).join(',') ?? '';
  const nextNoIds = next.step.no_steps?.map((s) => s.id).join(',') ?? '';
  if (prevNoIds !== nextNoIds) return false;

  // Path: shallow segment compare
  if (prev.path.length !== next.path.length) return false;
  for (let i = 0; i < prev.path.length; i++) {
    if (prev.path[i].index !== next.path[i].index) return false;
    if (prev.path[i].branch !== next.path[i].branch) return false;
  }

  return true;
}

export const StepRenderer = React.memo(StepRendererInner, arePropsEqual);
