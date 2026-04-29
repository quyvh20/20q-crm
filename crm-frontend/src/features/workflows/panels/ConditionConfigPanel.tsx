import React, { useMemo } from 'react';
import { type ConditionGroup, type ConditionRule } from '../types';
import { useBuilderStore } from '../store';
import { getOperatorsForType } from '../useSchema';

export const ConditionConfigPanel: React.FC = () => {
  const { conditions, setConditions, schema, schemaLoading, schemaError, invalidateSchema } = useBuilderStore();

  const group: ConditionGroup = conditions || { op: 'AND', rules: [] };

  // Build flat list of all field paths from schema entities + custom objects
  const fieldOptions = useMemo(() => {
    if (!schema) return [];
    const allEntities = [...schema.entities, ...(schema.custom_objects || [])];
    return allEntities.flatMap((e) =>
      e.fields.map((f) => ({
        path: f.path,
        label: `${e.label} → ${f.label}`,
        type: f.type,
      }))
    );
  }, [schema]);

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

  // Get the schema field type for a given field path
  const getFieldType = (path: string): string => {
    const found = fieldOptions.find((f) => f.path === path);
    return found?.type || 'string';
  };

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Conditions</h3>
      <p className="text-xs text-gray-400">Only execute actions when all/any conditions match.</p>

      {/* Schema error banner — no silent fallback to stale data */}
      {schemaError && (
        <div className="flex items-center gap-2 p-2.5 rounded-lg bg-red-500/10 border border-red-500/30">
          <span className="text-xs text-red-400 flex-1">⚠ Failed to load fields: {schemaError}</span>
          <button
            onClick={invalidateSchema}
            className="text-xs text-red-300 hover:text-white underline whitespace-nowrap"
          >
            Retry
          </button>
        </div>
      )}

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
        {group.rules.map((rule, idx) => {
          const fieldType = getFieldType(rule.field || '');
          const operators = getOperatorsForType(fieldType);

          return (
            <div key={idx} className="flex gap-2 items-start">
              <div className="flex-1 space-y-2">
                {/* Field picker — skeleton while loading, dropdown when ready, error state shown above */}
                {schemaLoading ? (
                  <div className="w-full h-[34px] bg-gray-800 border border-gray-700 rounded-lg animate-pulse" />
                ) : (
                  <select
                    value={rule.field || ''}
                    onChange={(e) => updateRule(idx, { field: e.target.value, operator: 'eq', value: '' })}
                    disabled={!!schemaError}
                    className="w-full bg-gray-800 border border-gray-700 rounded-lg px-3 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none disabled:opacity-50 disabled:cursor-not-allowed"
                  >
                    <option value="">{schemaError ? 'Schema unavailable' : 'Select field…'}</option>
                    {fieldOptions.map((f) => (
                      <option key={f.path} value={f.path}>{f.label}</option>
                    ))}
                  </select>
                )}
                <div className="flex gap-2">
                  {schemaLoading ? (
                    <div className="flex-1 h-[34px] bg-gray-800 border border-gray-700 rounded-lg animate-pulse" />
                  ) : (
                    <select
                      value={rule.operator || 'eq'}
                      onChange={(e) => updateRule(idx, { operator: e.target.value })}
                      className="flex-1 bg-gray-800 border border-gray-700 rounded-lg px-2 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none"
                    >
                      {operators.map((op) => (
                        <option key={op.value} value={op.value}>{op.label}</option>
                      ))}
                    </select>
                  )}
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
          );
        })}
      </div>

      <button
        onClick={addRule}
        disabled={schemaLoading || !!schemaError}
        className="w-full py-2 border border-dashed border-gray-700 rounded-lg text-sm text-gray-400 hover:text-white hover:border-gray-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {schemaLoading ? 'Loading fields…' : schemaError ? 'Schema unavailable' : '+ Add Rule'}
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
