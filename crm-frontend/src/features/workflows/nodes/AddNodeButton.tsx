import React, { useState } from 'react';
import { useDroppable } from '@dnd-kit/core';
import { useBuilderStore, generateActionId } from '../store';
import { ACTION_LABELS, ACTION_ICONS, type ActionType } from '../types';

const ACTION_TYPES: ActionType[] = [
  'send_email', 'create_task', 'assign_user', 'send_webhook', 'delay',
];

interface AddNodeButtonProps {
  index: number;
}

function getDefaultParams(type: ActionType): Record<string, unknown> {
  switch (type) {
    case 'send_email':
      return { to: '{{contact.email}}', subject: '', body_html: '' };
    case 'create_task':
      return { title: '', priority: 'medium', due_in_days: 3 };
    case 'assign_user':
      return { entity: 'contact', strategy: 'round_robin' };
    case 'send_webhook':
      return { url: '', method: 'POST', timeout_sec: 10 };
    case 'delay':
      return { duration_sec: 60 };
    default:
      return {};
  }
}

export const AddNodeButton: React.FC<AddNodeButtonProps> = ({ index }) => {
  const [showMenu, setShowMenu] = useState(false);
  const insertAction = useBuilderStore((s) => s.insertAction);

  const { isOver, setNodeRef } = useDroppable({
    id: `dropzone-${index}`,
    data: { targetIndex: index },
  });

  const handleAddAction = (type: ActionType) => {
    insertAction(
      {
        type,
        id: generateActionId(),
        params: getDefaultParams(type),
      },
      index
    );
    setShowMenu(false);
  };

  return (
    <div className="flex flex-col items-center py-1 relative">
      <div className="w-px h-6 bg-gray-700" />
      <div
        ref={setNodeRef}
        onClick={() => setShowMenu(!showMenu)}
        className={`
          flex items-center justify-center w-8 h-8 rounded-full border-2 border-dashed
          transition-all duration-200 cursor-pointer
          ${isOver
            ? 'border-emerald-400 bg-emerald-400/20 scale-125'
            : 'border-gray-600 hover:border-indigo-400 hover:bg-indigo-400/10'}
        `}
      >
        <span className={`text-sm transition-colors ${isOver ? 'text-emerald-400' : 'text-gray-500 hover:text-indigo-400'}`}>+</span>
      </div>

      {/* Quick-add dropdown menu */}
      {showMenu && (
        <>
          <div className="fixed inset-0 z-40" onClick={() => setShowMenu(false)} />
          <div className="absolute top-16 z-50 w-48 py-1 bg-gray-800 border border-gray-700 rounded-xl shadow-xl shadow-black/40 animate-in fade-in slide-in-from-top-2 duration-150">
            {ACTION_TYPES.map((type) => (
              <button
                key={type}
                onClick={() => handleAddAction(type)}
                className="w-full flex items-center gap-2.5 px-3 py-2 text-sm text-gray-300 hover:bg-gray-700 hover:text-white transition-colors"
              >
                <span className="w-6 h-6 rounded-md bg-gradient-to-br from-emerald-400 to-teal-500 flex items-center justify-center text-xs">
                  {ACTION_ICONS[type]}
                </span>
                {ACTION_LABELS[type]}
              </button>
            ))}
          </div>
        </>
      )}

      <div className="w-px h-6 bg-gray-700" />
    </div>
  );
};
