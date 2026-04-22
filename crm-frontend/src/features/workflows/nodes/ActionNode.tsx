import React from 'react';
import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { ACTION_LABELS, ACTION_ICONS, type ActionSpec } from '../types';
import { useBuilderStore } from '../store';

interface ActionNodeProps {
  action: ActionSpec;
  index: number;
}

export const ActionNode: React.FC<ActionNodeProps> = ({ action, index }) => {
  const { selectedNodeId, selectNode, removeAction } = useBuilderStore();
  const isSelected = selectedNodeId === action.id;

  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: action.id,
    data: { source: 'canvas', index },
  });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
    minWidth: 280,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      onClick={() => selectNode(action.id)}
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
          {ACTION_ICONS[action.type] || '⚙️'}
        </div>
        <div className="flex-1 min-w-0">
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">
            Step {index + 1}
          </p>
          <p className="text-sm font-medium text-white truncate">
            {ACTION_LABELS[action.type]}
          </p>
        </div>
        <button
          onClick={(e) => {
            e.stopPropagation();
            removeAction(action.id);
          }}
          className="w-7 h-7 rounded-full flex items-center justify-center text-gray-500 hover:text-red-400 hover:bg-red-400/10 transition-colors"
        >
          ✕
        </button>
      </div>
      {action.params.title && (
        <p className="text-xs text-gray-400 mt-2 truncate pl-13">
          {String(action.params.title)}
        </p>
      )}
    </div>
  );
};
