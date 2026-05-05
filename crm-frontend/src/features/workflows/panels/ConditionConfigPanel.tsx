import React, { useMemo, useCallback, useState, useEffect, useRef } from 'react';
import { type ConditionGroup, type ConditionRule } from '../types';
import { useBuilderStore } from '../store';
import { getOperatorsForType, isNoValueOperator, isDualValueOperator, findFieldInSchema } from '../useSchema';
import type { FiresOn } from '../useSchema';
import { FieldPicker, type FieldMeta } from './FieldPicker';
import { SmartValueInput } from './SmartValueInput';

const MAX_CONDITIONS = 10;

// ============================================================
// Conditions Panel — Step 2
// Scoped to the Source object, dynamic operators per field type + fires-on
// ============================================================

/** Derive firesOn from current trigger type string */
function deriveFiresOn(triggerType: string): FiresOn {
  if (triggerType.endsWith('_created')) return 'created';
  if (triggerType.endsWith('_updated')) return 'updated';
  if (triggerType.endsWith('_deleted')) return 'deleted';
  if (triggerType.endsWith('_any')) return 'any';
  // Special built-in types
  if (triggerType === 'deal_stage_changed') return 'updated';
  if (triggerType === 'no_activity_days') return 'any';
  if (triggerType === 'webhook_inbound') return 'any';
  return 'created';
}

/** Derive object slug from current trigger type string */
function deriveObjectSlug(triggerType: string): string {
  if (triggerType === 'deal_stage_changed') return 'deal';
  if (triggerType === 'no_activity_days') return 'contact';
  if (triggerType === 'webhook_inbound') return 'webhook';
  for (const suffix of ['_created', '_updated', '_deleted', '_any']) {
    if (triggerType.endsWith(suffix)) {
      return triggerType.slice(0, -suffix.length);
    }
  }
  return '';
}

export const ConditionConfigPanel: React.FC = () => {
  const { trigger, conditions, setConditions, schema, schemaLoading, schemaError, invalidateSchema } = useBuilderStore();

  // Derive firesOn + object from current trigger
  const firesOn = useMemo<FiresOn>(() => {
    if (!trigger) return 'created';
    return deriveFiresOn(trigger.type);
  }, [trigger]);

  const objectSlug = useMemo(() => {
    if (!trigger) return '';
    return deriveObjectSlug(trigger.type);
  }, [trigger]);

  // Get only fields scoped to the selected Source object
  const scopedFields = useMemo(() => {
    if (!schema || !objectSlug) return [];
    const allEntities = [...schema.entities, ...(schema.custom_objects || [])];
    const entity = allEntities.find((e) => e.key === objectSlug);
    return entity?.fields || [];
  }, [schema, objectSlug]);

  // Entity label for preview
  const objectLabel = useMemo(() => {
    if (!schema || !objectSlug) return objectSlug;
    const allEntities = [...schema.entities, ...(schema.custom_objects || [])];
    const entity = allEntities.find((e) => e.key === objectSlug);
    return entity?.label || objectSlug;
  }, [schema, objectSlug]);

  // Inline toast
  const [resetNotice, setResetNotice] = useState<number | null>(null);
  const resetTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const showResetNotice = (ruleIndex: number) => {
    clearTimeout(resetTimer.current);
    setResetNotice(ruleIndex);
    resetTimer.current = setTimeout(() => setResetNotice(null), 3000);
  };
  useEffect(() => () => clearTimeout(resetTimer.current), []);

  const group: ConditionGroup = conditions || { op: 'AND', rules: [] };

  const updateGroup = (updates: Partial<ConditionGroup>) => {
    setConditions({ ...group, ...updates });
  };

  const addRule = () => {
    if (group.rules.length >= MAX_CONDITIONS) return;
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

  /** Toggle AND/OR between two adjacent rows */
  const toggleRowOp = (index: number) => {
    const newRules = [...group.rules];
    const currentOp = newRules[index].op || group.op;
    newRules[index] = { ...newRules[index], op: currentOp === 'AND' ? 'OR' : 'AND' };
    updateGroup({ rules: newRules });
  };

  // Get the schema field type for a given field path
  const getFieldType = (path: string): string => {
    const found = scopedFields.find((f) => f.path === path);
    return found?.type || 'string';
  };

  /**
   * Handle field selection — reset operator + value when field type changes.
   */
  const handleFieldChange = useCallback(
    (index: number, path: string, fieldMeta: FieldMeta) => {
      const currentRule = group.rules[index];
      const oldFieldType = getFieldType(currentRule.field || '');
      const newFieldType = fieldMeta.type;

      if (oldFieldType !== newFieldType || !currentRule.field) {
        const validOps = getOperatorsForType(newFieldType, firesOn);
        const currentOpStillValid = validOps.some((op) => op.value === currentRule.operator);
        const didReset = !currentOpStillValid && !!currentRule.field;

        updateRule(index, {
          field: path,
          operator: currentOpStillValid ? currentRule.operator : validOps[0]?.value || 'eq',
          value: null,
        });

        if (didReset) showResetNotice(index);
      } else {
        updateRule(index, { field: path, value: null });
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [group.rules, scopedFields, firesOn],
  );

  // --- Build live preview sentence ---
  const previewSentence = useMemo(() => {
    if (!trigger || group.rules.length === 0) return '';
    const firesOnLabel = {
      created: 'created', updated: 'updated', deleted: 'deleted', any: 'created or updated',
    }[firesOn] || firesOn;

    let sentence = `When a ${objectLabel} is ${firesOnLabel}`;
    const parts: string[] = [];

    for (let i = 0; i < group.rules.length; i++) {
      const rule = group.rules[i];
      if (!rule.field) continue;

      const fieldDef = scopedFields.find((f) => f.path === rule.field);
      const fieldLabel = fieldDef?.label || rule.field?.split('.').pop() || '…';
      const op = rule.operator || 'eq';
      const opDef = getOperatorsForType(getFieldType(rule.field), firesOn).find((o) => o.value === op);
      const opLabel = opDef?.label || op;

      let part = `${fieldLabel} ${opLabel}`;
      if (!isNoValueOperator(op) && rule.value !== null && rule.value !== undefined && rule.value !== '') {
        if (isDualValueOperator(op) && Array.isArray(rule.value)) {
          part += ` ${rule.value[0] ?? '…'} to ${rule.value[1] ?? '…'}`;
        } else {
          part += ` "${String(rule.value)}"`;
        }
      }

      if (i > 0) {
        const joinOp = group.rules[i].op || group.op;
        parts.push(joinOp);
      }
      parts.push(part);
    }

    if (parts.length > 0) {
      sentence += ', and ' + parts.join(' ');
    }
    return sentence;
  }, [trigger, group, objectLabel, firesOn, scopedFields]);

  // No source object selected
  if (!objectSlug) {
    return (
      <div className="space-y-4">
        <h3 className="text-lg font-semibold text-white">Conditions</h3>
        <p className="text-xs text-gray-500 italic">Select a Source object first.</p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-white">Conditions</h3>
      <p className="text-xs text-gray-400">Optional — filter when actions should run.</p>

      {/* Schema error banner */}
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

      {/* Condition rows */}
      <div className="space-y-0">
        {group.rules.map((rule, idx) => {
          const fieldType = getFieldType(rule.field || '');
          const operators = getOperatorsForType(fieldType, firesOn);
          const currentOp = rule.operator || 'eq';
          const noValue = isNoValueOperator(currentOp);
          const dualValue = isDualValueOperator(currentOp);
          const resolvedField = rule.field ? findFieldInSchema(schema, rule.field) : null;

          return (
            <React.Fragment key={idx}>
              {/* AND/OR toggle between rows */}
              {idx > 0 && (
                <div className="flex items-center justify-center py-1">
                  <button
                    onClick={() => toggleRowOp(idx)}
                    className={`px-3 py-0.5 rounded-full text-[10px] font-bold uppercase tracking-wider transition-all ${
                      (rule.op || group.op) === 'AND'
                        ? 'bg-purple-500/20 text-purple-400 hover:bg-purple-500/30'
                        : 'bg-amber-500/20 text-amber-400 hover:bg-amber-500/30'
                    }`}
                  >
                    {rule.op || group.op}
                  </button>
                </div>
              )}

              <div className="group/rule rounded-xl border border-gray-800 bg-gray-900/50 p-3 space-y-2 transition-colors hover:border-gray-700">
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
                      entities={[objectSlug]}
                    />
                  </div>
                  <button
                    onClick={() => removeRule(idx)}
                    className="mt-0.5 w-7 h-7 rounded-full flex items-center justify-center text-gray-600 hover:text-red-400 hover:bg-red-400/10 transition-colors opacity-0 group-hover/rule:opacity-100"
                  >
                    ✕
                  </button>
                </div>

                {/* Row 2: Operator + Value */}
                {rule.field && (
                  <div className="flex gap-2 items-center flex-wrap">
                    {/* Operator dropdown */}
                    <select
                      value={currentOp}
                      onChange={(e) => {
                        const newOp = e.target.value;
                        const newNoValue = isNoValueOperator(newOp);
                        const newDual = isDualValueOperator(newOp);
                        const oldDual = isDualValueOperator(currentOp);

                        if (newNoValue) {
                          updateRule(idx, { operator: newOp, value: null });
                        } else if (newDual !== oldDual) {
                          updateRule(idx, { operator: newOp, value: newDual ? ['', ''] : null });
                        } else {
                          updateRule(idx, { operator: newOp });
                        }
                      }}
                      className="bg-gray-800 border border-gray-700 rounded-lg px-2 py-1.5 text-sm text-white focus:border-purple-500 focus:outline-none min-w-[130px]"
                    >
                      {operators.map((op) => (
                        <option key={op.value} value={op.value}>{op.label}</option>
                      ))}
                    </select>

                    {/* Single value input */}
                    {!noValue && !dualValue && resolvedField && (
                      <div className="flex-1 min-w-[120px]">
                        <SmartValueInput
                          field={resolvedField}
                          operator={currentOp}
                          value={rule.value}
                          onChange={(v) => updateRule(idx, { value: v })}
                        />
                      </div>
                    )}

                    {/* Dual value input (between, changed from…to) */}
                    {!noValue && dualValue && resolvedField && (
                      <div className="flex items-center gap-1.5 flex-1 min-w-[200px]">
                        <div className="flex-1">
                          <SmartValueInput
                            field={resolvedField}
                            operator={currentOp}
                            value={Array.isArray(rule.value) ? rule.value[0] : ''}
                            onChange={(v) => {
                              const arr = Array.isArray(rule.value) ? [...rule.value] : ['', ''];
                              arr[0] = v;
                              updateRule(idx, { value: arr });
                            }}
                          />
                        </div>
                        <span className="text-xs text-gray-500 font-medium">to</span>
                        <div className="flex-1">
                          <SmartValueInput
                            field={resolvedField}
                            operator={currentOp}
                            value={Array.isArray(rule.value) ? rule.value[1] : ''}
                            onChange={(v) => {
                              const arr = Array.isArray(rule.value) ? [...rule.value] : ['', ''];
                              arr[1] = v;
                              updateRule(idx, { value: arr });
                            }}
                          />
                        </div>
                      </div>
                    )}

                    {/* "in last N days" — special number input */}
                    {currentOp === 'in_last_days' && (
                      <div className="flex items-center gap-1.5">
                        <input
                          type="number"
                          min={1}
                          value={typeof rule.value === 'number' ? rule.value : ''}
                          placeholder="N"
                          onChange={(e) => updateRule(idx, { value: parseInt(e.target.value) || null })}
                          className="w-16 bg-gray-800 border border-gray-700 rounded-lg px-2 py-1.5 text-sm text-white text-center focus:border-purple-500 focus:outline-none"
                        />
                        <span className="text-xs text-gray-500">days</span>
                      </div>
                    )}
                  </div>
                )}

                {/* Field type indicator */}
                {rule.field && (
                  <div className="flex items-center gap-1.5 text-[10px] text-gray-600">
                    <span className="uppercase tracking-wider font-medium" style={{
                      color: TYPE_COLORS[fieldType] || '#6B7280',
                    }}>
                      {fieldType}
                    </span>
                    <span>·</span>
                    <span className="font-mono text-gray-600">{rule.field}</span>
                  </div>
                )}
              </div>
            </React.Fragment>
          );
        })}
      </div>

      {/* Add rule button */}
      <button
        onClick={addRule}
        disabled={schemaLoading || !!schemaError || group.rules.length >= MAX_CONDITIONS}
        className="w-full py-2 border border-dashed border-gray-700 rounded-lg text-sm text-gray-400 hover:text-white hover:border-gray-500 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
      >
        {schemaLoading
          ? 'Loading fields…'
          : schemaError
          ? 'Schema unavailable'
          : group.rules.length >= MAX_CONDITIONS
          ? `Max ${MAX_CONDITIONS} conditions`
          : '+ Add Condition'}
      </button>

      {/* Remove all */}
      {conditions && group.rules.length > 0 && (
        <button
          onClick={() => setConditions(null)}
          className="text-xs text-red-400 hover:text-red-300"
        >
          Remove all conditions
        </button>
      )}

      {/* Live preview */}
      {previewSentence && (
        <div className="px-3 py-2 rounded-lg bg-purple-500/5 border border-purple-500/10">
          <p className="text-xs text-purple-300/70">
            <span className="text-purple-400 font-medium">Preview: </span>
            {previewSentence}
          </p>
        </div>
      )}
    </div>
  );
};

// --- Field type indicator colors ---
const TYPE_COLORS: Record<string, string> = {
  string: '#9CA3AF',
  number: '#60A5FA',
  boolean: '#F59E0B',
  array: '#A78BFA',
  select: '#34D399',
  date: '#FB923C',
};
