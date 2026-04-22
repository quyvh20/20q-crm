import React from 'react';
import { TRIGGER_LABELS, type TriggerSpec } from '../types';
import { useBuilderStore } from '../store';

interface TriggerNodeProps {
  trigger: TriggerSpec | null;
}

export const TriggerNode: React.FC<TriggerNodeProps> = ({ trigger }) => {
  const { selectedNodeId, selectNode, errors } = useBuilderStore();
  const isSelected = selectedNodeId === 'trigger';
  const hasError = !!errors.trigger;

  return (
    <div
      onClick={() => selectNode('trigger')}
      className={`
        relative p-4 rounded-xl cursor-pointer transition-all duration-200
        border-2 ${hasError ? 'border-red-500' : isSelected ? 'border-indigo-500' : 'border-gray-700'}
        ${isSelected ? 'bg-indigo-500/10 shadow-lg shadow-indigo-500/20' : 'bg-gray-800/80 hover:bg-gray-800'}
      `}
      style={{ minWidth: 280 }}
    >
      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-amber-400 to-orange-500 flex items-center justify-center text-lg">
          ⚡
        </div>
        <div>
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">Trigger</p>
          <p className="text-sm font-medium text-white">
            {trigger ? TRIGGER_LABELS[trigger.type] : 'Select a trigger...'}
          </p>
        </div>
      </div>
      {hasError && (
        <p className="text-xs text-red-400 mt-2">{errors.trigger?.[0]}</p>
      )}
    </div>
  );
};
