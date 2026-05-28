import React from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import type { WorkflowStep } from '../types';
import { useBuilderStore } from '../store';

interface DelayNodeProps {
  step: WorkflowStep;
  index: number;
}

/** Format delay duration into a human-readable string */
function formatDelay(sec: number): string {
  if (sec <= 0) return 'Configure delay';
  const units: [number, string][] = [[86400, 'day'], [3600, 'hour'], [60, 'minute'], [1, 'second']];
  for (const [factor, label] of units) {
    if (sec >= factor && sec % factor === 0) {
      const v = sec / factor;
      return `Wait ${v} ${label}${v !== 1 ? 's' : ''}`;
    }
  }
  return `Wait ${sec}s`;
}

export const DelayNode: React.FC<DelayNodeProps> = ({ step, index }) => {
  const { selectedNodeId, selectNode, removeStep } = useBuilderStore();
  const isSelected = selectedNodeId === step.id;

  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: step.id,
    data: { source: 'canvas', index },
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    minWidth: 280,
  };

  const sec = Number(step.delay?.duration_sec) || 0;
  const summary = formatDelay(sec);

  return (
    <div
      ref={setNodeRef}
      style={style}
      onClick={() => selectNode(step.id)}
      className={`
        relative p-4 rounded-xl cursor-pointer transition-all duration-200
        border-2 ${isSelected ? 'border-amber-500' : 'border-gray-700'}
        ${isSelected ? 'bg-amber-500/10 shadow-lg shadow-amber-500/20' : 'bg-gray-800/80 hover:bg-gray-800'}
      `}
    >
      <div className="flex items-center gap-3">
        <div
          {...attributes}
          {...listeners}
          className="w-10 h-10 rounded-lg bg-gradient-to-br from-amber-400 to-orange-500 flex items-center justify-center text-lg cursor-grab active:cursor-grabbing"
        >
          ⏱️
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">
            Step {index + 1}
          </p>
          <p className="text-sm font-medium text-white truncate">
            Delay
          </p>
        </div>
        <button
          onClick={(e) => {
            e.stopPropagation();
            removeStep(step.id);
          }}
          className="w-7 h-7 rounded-full flex items-center justify-center text-gray-500 hover:text-red-400 hover:bg-red-400/10 transition-colors"
        >
          ✕
        </button>
      </div>
      <p className="text-xs text-gray-400 mt-2 truncate pl-13">
        {summary}
      </p>
    </div>
  );
};
