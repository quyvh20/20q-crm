import React from 'react';
import type { ConditionGroup } from '../types';
import { useBuilderStore } from '../store';

interface ConditionNodeProps {
  conditions: ConditionGroup | null;
}

export const ConditionNode: React.FC<ConditionNodeProps> = ({ conditions }) => {
  const { selectedNodeId, selectNode, errors } = useBuilderStore();
  const isSelected = selectedNodeId === 'conditions';
  const hasError = !!errors.conditions;

  const ruleCount = conditions?.rules?.length ?? 0;

  return (
    <div
      onClick={() => selectNode('conditions')}
      className={`
        relative p-4 rounded-xl cursor-pointer transition-all duration-200
        border-2 border-dashed ${hasError ? 'border-red-500' : isSelected ? 'border-purple-500' : 'border-gray-700'}
        ${isSelected ? 'bg-purple-500/10 shadow-lg shadow-purple-500/20' : 'bg-gray-800/60 hover:bg-gray-800'}
      `}
      style={{ minWidth: 280 }}
    >
      <div className="flex items-center gap-3">
        <div className="w-10 h-10 rounded-lg bg-gradient-to-br from-purple-400 to-fuchsia-500 flex items-center justify-center text-lg">
          🔀
        </div>
        <div>
          <p className="text-xs uppercase tracking-wider text-gray-400 font-semibold">Conditions</p>
          <p className="text-sm font-medium text-white">
            {ruleCount > 0
              ? `${ruleCount} rule${ruleCount !== 1 ? 's' : ''} (${conditions?.op})`
              : 'No conditions (optional)'}
          </p>
        </div>
      </div>
      {hasError && (
        <p className="text-xs text-red-400 mt-2">{errors.conditions?.[0]}</p>
      )}
    </div>
  );
};
