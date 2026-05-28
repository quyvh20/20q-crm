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
  return (
    <SortableContext
      items={steps.map((s) => s.id)}
      strategy={verticalListSortingStrategy}
    >
      <div className="flex flex-col items-center w-full">
        <AddNodeButton parentId={parentId} branch={branch} index={0} />
        {steps.map((step, idx) => (
          <React.Fragment key={step.id}>
            <StepRenderer step={step} index={idx} />
            <AddNodeButton parentId={parentId} branch={branch} index={idx + 1} />
          </React.Fragment>
        ))}
      </div>
    </SortableContext>
  );
};
