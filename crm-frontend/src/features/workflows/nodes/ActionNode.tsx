import React from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { ACTION_LABELS, ACTION_ICONS, type WorkflowStep } from '../types';
import { useBuilderStore, generateActionId, type StepPath } from '../store';

interface ActionNodeProps {
  step: WorkflowStep;
  path: StepPath;
}

/** Generate a short human-readable subtitle for the action step node */
function getStepSummary(step: WorkflowStep): string | null {
  const action = step.action;
  if (!action) return null;

  switch (action.type) {
    case 'send_email':
      return action.params.subject ? String(action.params.subject) : null;
    case 'send_webhook':
      return action.params.url ? String(action.params.url) : null;
    case 'log_activity':
      return action.params.title ? String(action.params.title) : null;
    default:
      return typeof action.params.title === 'string' && action.params.title ? action.params.title : null;
  }
}

/** Derive a display step number from the path (last segment index + 1) */
function stepLabel(path: StepPath): string {
  const last = path[path.length - 1];
  return `Step ${last ? last.index + 1 : 1}`;
}

const ActionNodeInner: React.FC<ActionNodeProps> = ({ step, path }) => {
  const { selectedNodeId, selectNode, removeStep, updateStep } = useBuilderStore();
  const isSelected = selectedNodeId === step.id;

  /** Wrap this action inside a new condition split (action becomes first yes_step) */
  const convertToCondition = (e: React.MouseEvent) => {
    e.stopPropagation();
    // Transform the current step into a condition split,
    // placing the original action as the first step in the Yes branch
    const originalAction: WorkflowStep = {
      id: generateActionId(),
      type: step.type,
      action: step.action ? { ...step.action, id: generateActionId() } : undefined,
      delay: step.delay ? { ...step.delay } : undefined,
    };
    updateStep(step.id, {
      type: 'condition',
      condition: { op: 'AND', rules: [{ field: '', operator: 'eq', value: '' }] },
      yes_steps: [originalAction],
      no_steps: [],
      // Clear action/delay fields since it's now a condition
      action: undefined,
      delay: undefined,
    });
    selectNode(step.id);
  };

  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: step.id,
    data: { source: 'canvas', path },
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    minWidth: 280,
  };

  const summary = getStepSummary(step);
  const type = step.action?.type || 'update_record';
  const label = ACTION_LABELS[type] || 'Update Record';
  const icon = ACTION_ICONS[type] || '⚙️';

  return (
    <div
      ref={setNodeRef}
      style={style}
      onClick={() => selectNode(step.id)}
      className={`
        group/action relative p-4 rounded-xl cursor-pointer transition-all duration-200
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
            {stepLabel(path)}
          </p>
          <p className="text-sm font-medium text-white truncate">
            {label}
          </p>
        </div>
        {/* Convert to condition split */}
        <button
          onClick={convertToCondition}
          title="Convert to condition split"
          className="w-7 h-7 rounded-full flex items-center justify-center text-gray-500 hover:text-purple-400 hover:bg-purple-400/10 transition-colors opacity-0 group-hover/action:opacity-100"
        >
          🔀
        </button>
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

export const ActionNode = React.memo(ActionNodeInner);
