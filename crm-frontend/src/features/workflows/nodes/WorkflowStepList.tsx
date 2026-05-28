import React from 'react';
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import type { WorkflowStep } from '../types';
import { StepRenderer } from './StepRenderer';
import { AddNodeButton } from './AddNodeButton';

interface WorkflowStepListProps {
  steps: WorkflowStep[];
  parentId: string | null;
  branch: 'yes' | 'no' | null;
}

export const WorkflowStepList: React.FC<WorkflowStepListProps> = ({ steps, parentId, branch }) => {
  // A condition is a terminal divergence — no steps can follow it at the
  // same level, so we cut the list at the first condition.
  const condIdx = steps.findIndex((s) => s.type === 'condition');
  const visibleSteps = condIdx >= 0 ? steps.slice(0, condIdx + 1) : steps;

  return (
    <SortableContext
      items={visibleSteps.map((s) => s.id)}
      strategy={verticalListSortingStrategy}
    >
      <div className="flex flex-col items-center w-full">
        <AddNodeButton parentId={parentId} branch={branch} index={0} />
        {visibleSteps.map((step, idx) => {
          const isCondition = step.type === 'condition';
          return (
            <React.Fragment key={step.id}>
              <StepRenderer step={step} index={idx} />
              {/* No AddNodeButton after a condition — flow diverges into branches */}
              {!isCondition && (
                <AddNodeButton parentId={parentId} branch={branch} index={idx + 1} />
              )}
            </React.Fragment>
          );
        })}
      </div>
    </SortableContext>
  );
};
