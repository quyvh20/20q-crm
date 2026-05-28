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
 * - `action`    → ActionNode
 * - `condition` → ConditionSplitNode (which recurses via WorkflowStepList)
 * - `delay`     → DelayNode
 */
export const StepRenderer: React.FC<StepRendererProps> = ({ step, path }) => {
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
