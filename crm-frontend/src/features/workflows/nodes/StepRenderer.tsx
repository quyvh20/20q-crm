import React from 'react';
import type { WorkflowStep } from '../types';
import { ActionNode } from './ActionNode';
import { ConditionSplitNode } from './ConditionSplitNode';
import { DelayNode } from './DelayNode';

interface StepRendererProps {
  step: WorkflowStep;
  index: number;
}

/**
 * Recursive step dispatcher — renders the correct node component
 * based on the step's discriminated `type` field.
 *
 * - `action`    → ActionNode
 * - `condition` → ConditionSplitNode (which recurses via WorkflowStepList)
 * - `delay`     → DelayNode
 */
export const StepRenderer: React.FC<StepRendererProps> = ({ step, index }) => {
  switch (step.type) {
    case 'action':
      return <ActionNode step={step} index={index} />;
    case 'condition':
      return <ConditionSplitNode step={step} index={index} />;
    case 'delay':
      return <DelayNode step={step} index={index} />;
    default:
      // Exhaustiveness guard — should never reach here with valid data
      if (process.env.NODE_ENV !== 'production') {
        console.warn(`[StepRenderer] Unknown step type: "${(step as any).type}"`);
      }
      return null;
  }
};
