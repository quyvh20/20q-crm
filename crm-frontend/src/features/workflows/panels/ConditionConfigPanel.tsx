import React, { useMemo, useCallback, useState, useEffect, useRef } from 'react';
import { type ConditionGroup, type ConditionRule } from '../types';
import { useBuilderStore } from '../store';
import { getOperatorsForType, findFieldInSchema } from '../useSchema';
import { FieldPicker, type FieldMeta } from './FieldPicker';
import { SmartValueInput } from './SmartValueInput';

export const ConditionConfigPanel: React.FC = () => {
  const { conditions, setConditions, schema, schemaLoading, schemaError, invalidateSchema } = useBuilderStore();

  // Inline toast: shows briefly when operator is auto-reset on field type change
  const [resetNotice, setResetNotice] = useState<number | null>(null);
  const resetTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const showResetNotice = (ruleIndex: number) => {
    clearTimeout(resetTimer.current);
    setResetNotice(ruleIndex);
    resetTimer.current = setTimeout(() => setResetNotice(null), 3000);
  };
  useEffect(() => () => clearTimeout(resetTimer.current), []);

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

  /**
   * Handle field selection from FieldPicker.
   * ALWAYS reset value on field change (don't carry old field's data).
   * Also reset operator when the field type changes.
   */
  const handleFieldChange = useCallback(
    (index: number, path: string, fieldMeta: FieldMeta) => {
      const currentRule = group.rules[index];
      const oldFieldType = getFieldType(currentRule.field || '');
      const newFieldType = fieldMeta.type;

      // If type changed (or first selection), reset operator to a valid one
      if (oldFieldType !== newFieldType || !currentRule.field) {
        const validOps = getOperatorsForType(newFieldType);
        const currentOpStillValid = validOps.some((op) => op.value === currentRule.operator);
        const didReset = !currentOpStillValid && !!currentRule.field;

        updateRule(index, {
          field: path,
          operator: currentOpStillValid ? currentRule.operator : validOps[0]?.value || 'eq',
          value: null,
        });

        if (didReset) showResetNotice(index);
      } else {
        // Same type, different field → still reset value (don't carry stale data)
        updateRule(index, { field: path, value: null });
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [group.rules, fieldOptions]
  );

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
          const isUnary = ['is_empty', 'is_not_empty'].includes(rule.operator || '');
          const resolvedField = rule.field ? findFieldInSchema(schema, rule.field) : null;

          return (
            <div key={idx} className="group/rule rounded-xl border border-gray-800 bg-gray-900/50 p-3 space-y-2 transition-colors hover:border-gray-700">
              {/* Inline toast: operator was auto-reset */}
              {resetNotice === idx && (
                <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-amber-500/10 border border-amber-500/30 text-[11px] text-amber-400 animate-pulse">
                  ⚡ Operator reset because field type changed
                </div>
              )}
              {/* Row 1: Field picker */}
              <div className="flex gap-2 items-start">
                <div className="flex-1">
                  <FieldPicker
                    value={rule.field || null}
                    onChange={(path, fieldMeta) => handleFieldChange(idx, path, fieldMeta)}
                    disabled={!!schemaError}
                    placeholder="Select field…"
                  />
                </div>
                <button
                  onClick={() => removeRule(idx)}
                  className="mt-0.5 w-7 h-7 rounded-full flex items-center justify-center text-gray-600 hover:text-red-400 hover:bg-red-400/10 transition-colors opacity-0 group-hover/rule:opacity-100"
                >
                  ✕
                </button>
              </div>

              {/* Row 2: Operator + Value (only show when field is selected) */}
              {rule.field && (
                <div className="flex gap-2 items-center">
                  {/* Operator dropdown — filtered by field type */}
                  <select
                    value={rule.operator || 'eq'}
                    onChange={(e) => {
                      const newOp = e.target.value;
                      const oldOp = rule.operator || 'eq';
                      const isNewUnary = ['is_empty', 'is_not_empty'].includes(newOp);
                      const isNewMulti = ['in', 'not_in'].includes(newOp);
                      const isOldMulti = ['in', 'not_in'].includes(oldOp);

                      // Unary → null value, hide input
                      if (isNewUnary) {
                        updateRule(idx, { operator: newOp, value: null });
                      // Switching between multi↔scalar → reset value
                      } else if (isNewMulti !== isOldMulti) {
                        updateRule(idx, { operator: newOp, value: isNewMulti ? [] : null });
                      } else {
                        updateRule(idx, { operator: newOp });
                      }
                    }}
                    className="bg-gray-800 border border-gray-700 rounded-lg px-2 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none min-w-[120px]"
                  >
                    {operators.map((op) => (
                      <option key={op.value} value={op.value}>{op.label}</option>
                    ))}
                  </select>

                  {/* Value input — hidden for unary operators */}
                  {!isUnary && resolvedField && (
                    <SmartValueInput
                      field={resolvedField}
                      operator={rule.operator || 'eq'}
                      value={rule.value}
                      onChange={(v) => updateRule(idx, { value: v })}
                    />
                  )}
                </div>
              )}

              {/* Field type indicator when selected */}
              {rule.field && (
                <div className="flex items-center gap-1.5 text-[10px] text-gray-600">
                  <span className="uppercase tracking-wider font-medium" style={{
                    color: TYPE_INDICATOR_COLORS[fieldType] || '#6B7280',
                  }}>
                    {fieldType}
                  </span>
                  <span>·</span>
                  <span className="font-mono text-gray-600">{rule.field}</span>
                </div>
              )}
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

// --- Field type indicator colors (used by rule row) ---

const TYPE_INDICATOR_COLORS: Record<string, string> = {
  string: '#9CA3AF',
  number: '#60A5FA',
  boolean: '#F59E0B',
  array: '#A78BFA',
  select: '#34D399',
  date: '#FB923C',
};
