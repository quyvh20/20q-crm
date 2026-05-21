import React from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { ACTION_LABELS, ACTION_ICONS, type WorkflowStep } from '../types';
import { useBuilderStore } from '../store';

interface ActionNodeProps {
  step: WorkflowStep;
  index: number;
}

/** Generate a short human-readable subtitle for the step node on the canvas */
function getStepSummary(step: WorkflowStep): string | null {
  if (step.type === 'delay') {
    const sec = Number(step.delay?.duration_sec) || 0;
    if (sec <= 0) return null;
    const units: [number, string][] = [[86400, 'day'], [3600, 'hour'], [60, 'minute'], [1, 'second']];
    for (const [factor, label] of units) {
      if (sec >= factor && sec % factor === 0) {
        const v = sec / factor;
        return `Wait ${v} ${label}${v !== 1 ? 's' : ''}`;
      }
    }
    return `Wait ${sec}s`;
  }
  
  const action = step.action;
  if (!action) return null;

  switch (action.type) {
    case 'send_email':
      return action.params.subject ? String(action.params.subject) : null;
    case 'send_webhook':
      return action.params.url ? String(action.params.url) : null;
    default:
      return typeof action.params.title === 'string' && action.params.title ? action.params.title : null;
  }
}

export const ActionNode: React.FC<ActionNodeProps> = ({ step, index }) => {
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

  const summary = getStepSummary(step);
  const type = step.type === 'delay' ? 'delay' : step.action?.type || 'update_record';
  const label = step.type === 'delay' ? 'Delay' : ACTION_LABELS[type] || 'Update Record';
  const icon = step.type === 'delay' ? '⏱️' : ACTION_ICONS[type] || '⚙️';

  return (
    <div
      ref={setNodeRef}
      style={style}
      onClick={() => selectNode(step.id)}
      className={`
        relative p-4 rounded-xl cursor-pointer transition-all duration-200
        border-2 ${isSelected ? 'border-emerald-500' : 'border-gray-700'}
        ${isSelected ? 'bg-emerald-500/10 shadow-lg shadow-emerald-500/20' : 'bg-gray-800/80 hover:bg-gray-800'}
      `}
    >
      <div className="flex items-center gap-3">
        <div
          {...attributes}
          {...listeners}
          className="w-10 h-10 rounded-lg bg-gradient-to-br from-emerald-400 to-teal-500 flex items-center justify-center text-lg cursor-grab active:cursor-grabbing"
        >
          {icon}
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">
            Step {index + 1}
          </p>
          <p className="text-sm font-medium text-white truncate">
            {label}
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
      {summary && (
        <p className="text-xs text-gray-400 mt-2 truncate pl-13">
          {summary}
        </p>
      )}
    </div>
  );
};
