import React from 'react';
import { CONDITION_OPERATORS, type ConditionGroup, type ConditionRule } from '../types';
import { useBuilderStore } from '../store';

export const ConditionConfigPanel: React.FC = () => {
  const { conditions, setConditions } = useBuilderStore();

  const group: ConditionGroup = conditions || { op: 'AND', rules: [] };

  const updateGroup = (updates: Partial<ConditionGroup>) => {
    setConditions({ ...group, ...updates });
  };

  const addRule = () => {
    updateGroup({
      rules: [...group.rules, { field: '', operator: 'eq', value: '' }],
    });
  };

  const updateRule = (index: number, patch: Partial<ConditionRule>) => {
    const newRules = [...group.rules];
    newRules[index] = { ...newRules[index], ...patch };
    updateGroup({ rules: newRules });
  };

  const removeRule = (index: number) => {
    const newRules = group.rules.filter((_, i) => i !== index);
    if (newRules.length === 0) {
      setConditions(null);
    } else {
      updateGroup({ rules: newRules });
    }
  };

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Conditions</h3>
      <p className="text-xs text-gray-400">Only execute actions when all/any conditions match.</p>

      <div className="flex gap-2">
        <button
          onClick={() => updateGroup({ op: 'AND' })}
          className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
            group.op === 'AND' ? 'bg-purple-500 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'
          }`}
        >
          AND (all match)
        </button>
        <button
          onClick={() => updateGroup({ op: 'OR' })}
          className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
            group.op === 'OR' ? 'bg-purple-500 text-white' : 'bg-gray-800 text-gray-400 hover:text-white'
          }`}
        >
          OR (any match)
        </button>
      </div>

      <div className="space-y-3">
        {group.rules.map((rule, idx) => (
          <div key={idx} className="flex gap-2 items-start">
            <div className="flex-1 space-y-2">
              <input
                type="text"
                placeholder="Field path (e.g. contact.tags)"
                value={rule.field || ''}
                onChange={(e) => updateRule(idx, { field: e.target.value })}
                className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none"
              />
              <div className="flex gap-2">
                <select
                  value={rule.operator || 'eq'}
                  onChange={(e) => updateRule(idx, { operator: e.target.value })}
                  className="flex-1 bg-gray-800 border border-gray-700 rounded-lg px-2 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none"
                >
                  {CONDITION_OPERATORS.map((op) => (
                    <option key={op.value} value={op.value}>{op.label}</option>
                  ))}
                </select>
                {!['is_empty', 'is_not_empty'].includes(rule.operator || '') && (
                  <input
                    type="text"
                    placeholder="Value"
                    value={String(rule.value ?? '')}
                    onChange={(e) => {
                      const num = Number(e.target.value);
                      updateRule(idx, { value: isNaN(num) ? e.target.value : num });
                    }}
                    className="flex-1 bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none"
                  />
                )}
              </div>
            </div>
            <button
              onClick={() => removeRule(idx)}
              className="mt-1 w-7 h-7 rounded-full flex items-center justify-center text-gray-500 hover:text-red-400 hover:bg-red-400/10 transition-colors"
            >
              ✕
            </button>
          </div>
        ))}
      </div>

      <button
        onClick={addRule}
        className="w-full py-2 border border-dashed border-gray-700 rounded-lg text-sm text-gray-400 hover:text-white hover:border-gray-500 transition-colors"
      >
        + Add Rule
      </button>

      {conditions && group.rules.length > 0 && (
        <button
          onClick={() => setConditions(null)}
          className="text-xs text-red-400 hover:text-red-300"
        >
          Remove all conditions
        </button>
      )}
    </div>
  );
};
