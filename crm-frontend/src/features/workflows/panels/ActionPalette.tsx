import React from 'react';
import { useDraggable } from '@dnd-kit/core';

const PALETTE_ITEMS = [
  { type: 'send_email', label: 'Send Email', icon: '✉️' },
  { type: 'create_task', label: 'Create Task', icon: '✅' },
  { type: 'assign_user', label: 'Assign User', icon: '👤' },
  { type: 'send_webhook', label: 'Send Webhook', icon: '🔗' },
  { type: 'delay', label: 'Delay', icon: '⏱️' },
  { type: 'update_record', label: 'Update Record', icon: '📝' },
  { type: 'condition', label: 'Condition Split', icon: '🔀' },
];

export const ActionPalette: React.FC = () => {
  return (
    <div className="space-y-2">
      <h4 className="text-xs uppercase tracking-wider text-gray-400 font-semibold px-1">Actions</h4>
      <p className="text-xs text-gray-500 px-1">Drag to add to workflow</p>
      {PALETTE_ITEMS.map((item) => (
        <PaletteItem key={item.type} type={item.type} label={item.label} icon={item.icon} />
      ))}
    </div>
  );
};

const PaletteItem: React.FC<{ type: string; label: string; icon: string }> = ({ type, label, icon }) => {
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
        {icon}
      </div>
      <span className="text-sm text-white font-medium">{label}</span>
    </div>
  );
};
