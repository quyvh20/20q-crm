import React from 'react';
import { useDraggable } from '@dnd-kit/core';
import { ACTION_LABELS, ACTION_ICONS, type ActionType } from '../types';

const ACTION_TYPES: ActionType[] = [
  'send_email', 'create_task', 'assign_user', 'send_webhook', 'delay',
];

export const ActionPalette: React.FC = () => {
  return (
    <div className="space-y-2">
      <h4 className="text-xs uppercase tracking-wider text-gray-400 font-semibold px-1">Actions</h4>
      <p className="text-xs text-gray-500 px-1">Drag to add to workflow</p>
      {ACTION_TYPES.map((type) => (
        <PaletteItem key={type} type={type} />
      ))}
    </div>
  );
};

const PaletteItem: React.FC<{ type: ActionType }> = ({ type }) => {
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `palette-${type}`,
    data: { source: 'palette', actionType: type },
  });

  return (
    <div
      ref={setNodeRef}
      {...attributes}
      {...listeners}
      className={`
        flex items-center gap-3 p-3 rounded-xl cursor-grab active:cursor-grabbing
        border border-gray-700 bg-gray-800/60 hover:bg-gray-800 transition-all
        ${isDragging ? 'opacity-50 scale-95' : 'hover:border-gray-600'}
      `}
    >
      <div className="w-8 h-8 rounded-lg bg-gradient-to-br from-emerald-400 to-teal-500 flex items-center justify-center text-sm">
        {ACTION_ICONS[type]}
      </div>
      <span className="text-sm text-white font-medium">{ACTION_LABELS[type]}</span>
    </div>
  );
};
