import React, { useMemo, useCallback, useState, useEffect, useRef } from 'react';
import { X } from 'lucide-react';
import { type ConditionGroup, type ConditionRule } from '../../types';
import { useBuilderStore } from '../../store';
import { getOperatorsForType, isNoValueOperator, isDualValueOperator, findFieldInSchema } from '../../useSchema';
import type { FiresOn } from '../../useSchema';
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

export const ConditionConfig: React.FC = () => {
  const { trigger, conditions, setConditions, schema, schemaLoading, schemaError, invalidateSchema, errors, fetchObjectFields, selectedNodeId, steps, findStep, updateStep } = useBuilderStore();

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

  const isGlobalConditions = selectedNodeId === 'conditions';
  // NOTE: `steps` in deps is critical — `findStep` is a stable function ref
  // that never changes, so without `steps` this memo would go stale after
  // every updateStep call, causing field picks and value edits to not reflect.
  const step = useMemo(() => {
    if (isGlobalConditions || !selectedNodeId) return null;
    return findStep(selectedNodeId);
  }, [selectedNodeId, findStep, isGlobalConditions, steps]);

  const group: ConditionGroup = useMemo(() => {
    if (isGlobalConditions) {
      return conditions || { op: 'AND', rules: [] };
    }
    if (step && step.type === 'condition') {
      return step.condition || { op: 'AND', rules: [] };
    }
    return { op: 'AND', rules: [] };
  }, [isGlobalConditions, conditions, step]);

  const updateGroup = (updates: Partial<ConditionGroup>) => {
    if (isGlobalConditions) {
      setConditions({ ...group, ...updates });
    } else if (step) {
      updateStep(step.id, { condition: { ...group, ...updates } });
    }
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
      if (isGlobalConditions) {
        setConditions(null);
      } else if (step) {
        updateStep(step.id, { condition: { op: 'AND', rules: [] } });
      }
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

      // Edge case 4: refetch fields for this object to get fresh picklist values
      // Don't trust cached picklist values — they may have changed on the backend
      if (objectSlug) {
        fetchObjectFields(objectSlug, true);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [group.rules, scopedFields, firesOn, objectSlug],
  );

  // --- Build live preview as rich JSX ---
  const { previewNodes, isIncomplete } = useMemo(() => {
    if (!trigger || group.rules.length === 0) return { previewNodes: null, isIncomplete: false };

    const firesOnLabel = {
      created: 'created', updated: 'updated', deleted: 'deleted', any: 'created or updated',
    }[firesOn] || firesOn;

    // Use "an" for vowel-start objects
    const article = /^[aeiou]/i.test(objectLabel) ? 'an' : 'a';

    const nodes: React.ReactNode[] = [];
    let incomplete = false;

    // Header: "When a {Object} is {event}"
    nodes.push(
      <React.Fragment key="header">
        When {article}{' '}
        <span className="font-semibold text-primary">{objectLabel}</span>
        {' '}is{' '}
        <span className="font-semibold text-primary">{firesOnLabel}</span>
      </React.Fragment>
    );

    // Condition clauses
    let hasConditions = false;
    for (let i = 0; i < group.rules.length; i++) {
      const rule = group.rules[i];
      if (!rule.field && !rule.operator) continue;

      const fieldDef = scopedFields.find((f) => f.path === rule.field);
      const fieldLabel = fieldDef?.label || rule.field?.split('.').pop() || '…';
      const op = rule.operator || 'eq';
      const opDef = getOperatorsForType(getFieldType(rule.field || ''), firesOn).find((o) => o.value === op);
      const opLabel = opDef?.label || op;
      const noVal = isNoValueOperator(op);
      const dualVal = isDualValueOperator(op);

      // Join connector
      if (!hasConditions) {
        nodes.push(<span key={`join-${i}`}>, and </span>);
      } else {
        const joinOp = rule.op || group.op;
        nodes.push(
          <span key={`join-${i}`} className="font-bold text-primary"> {joinOp} </span>
        );
      }
      hasConditions = true;

      // Field name
      nodes.push(
        <span key={`field-${i}`} className="font-medium text-primary">{fieldLabel}</span>
      );
      nodes.push(<span key={`sp1-${i}`}> </span>);

      // Operator
      nodes.push(
        <span key={`op-${i}`} className="text-primary/80">{opLabel}</span>
      );

      // Value rendering
      if (noVal) {
        // No value needed — sentence is complete as-is
      } else if (op === 'in_last_days') {
        // Special: "in last N days"
        const val = rule.value;
        if (val !== null && val !== undefined && val !== '') {
          nodes.push(
            <span key={`val-${i}`}>
              {' '}<span className="text-primary font-medium">'{String(val)}'</span> days
            </span>
          );
        } else {
          incomplete = true;
          nodes.push(
            <span key={`val-${i}`} className="text-muted-foreground italic"> {'{N}'} days</span>
          );
        }
      } else if (dualVal) {
        // Dual value: "from X to Y" or "X to Y"
        const arr = Array.isArray(rule.value) ? rule.value : ['', ''];
        const v0 = arr[0] !== '' && arr[0] != null ? arr[0] : null;
        const v1 = arr[1] !== '' && arr[1] != null ? arr[1] : null;

        if (v0 == null || v1 == null) incomplete = true;

        nodes.push(
          <span key={`val-${i}`}>
            {' '}
            {v0 != null
              ? <span className="text-primary font-medium">'{String(v0)}'</span>
              : <span className="text-muted-foreground italic">{'{value}'}</span>
            }
            <span className="text-primary/60"> to </span>
            {v1 != null
              ? <span className="text-primary font-medium">'{String(v1)}'</span>
              : <span className="text-muted-foreground italic">{'{value}'}</span>
            }
          </span>
        );
      } else {
        // Standard single value
        const val = rule.value;
        const hasVal = val !== null && val !== undefined && val !== '';

        if (hasVal) {
          // Format arrays as comma-separated
          const display = Array.isArray(val) ? val.join(', ') : String(val);
          nodes.push(
            <span key={`val-${i}`}>
              {' '}<span className="text-primary font-medium">'{display}'</span>
            </span>
          );
        } else {
          incomplete = true;
          nodes.push(
            <span key={`val-${i}`} className="text-muted-foreground italic"> {'{value}'}</span>
          );
        }
      }
    }

    return { previewNodes: nodes, isIncomplete: incomplete };
  }, [trigger, group, objectLabel, firesOn, scopedFields]);

  // Edge case 2: object has no fields user can read
  const hasNoFields = objectSlug && !schemaLoading && !schemaError && scopedFields.length === 0;

  // Edge case 3: detect orphaned fields (fields in condition rules that are no longer in schema)
  const orphanedFieldIndices = useMemo(() => {
    if (!scopedFields.length || !conditions) return new Set<number>();
    const validPaths = new Set(scopedFields.map((f) => f.path));
    const orphans = new Set<number>();
    for (let i = 0; i < group.rules.length; i++) {
      const field = group.rules[i].field;
      if (field && !validPaths.has(field)) {
        orphans.add(i);
      }
    }
    return orphans;
  }, [scopedFields, conditions, group.rules]);

  // No source object selected
  if (!objectSlug) {
    return (
      <div className="space-y-4">
        <h3 className="text-lg font-semibold text-foreground">Conditions</h3>
        <p className="text-xs text-muted-foreground italic">Select a Source object first.</p>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-foreground">Conditions</h3>
      <p className="text-xs text-muted-foreground">Optional — filter when actions should run.</p>

      {/* Edge case 2: no readable fields */}
      {hasNoFields && (
        <div className="p-4 rounded-xl border border-dashed border-border bg-muted/40 text-center space-y-2">
          <p className="text-sm text-muted-foreground">No fields available for conditions</p>
          <p className="text-xs text-muted-foreground">
            The selected object has no fields you can read. Conditions are disabled until the object has accessible fields.
          </p>
        </div>
      )}

      {/* Schema error banner */}
      {schemaError && (
        <div className="flex items-center gap-2 p-2.5 rounded-lg bg-destructive/10 border border-destructive/40">
          <span className="text-xs text-destructive flex-1">⚠ Failed to load fields: {schemaError}</span>
          <button
            onClick={invalidateSchema}
            className="text-xs text-destructive hover:text-foreground underline whitespace-nowrap"
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
          const ruleValueError = errors[`conditions.rules.${idx}.value`]?.[0];
          const isOrphaned = orphanedFieldIndices.has(idx);

          return (
            <React.Fragment key={idx}>
              {/* AND/OR toggle between rows */}
              {idx > 0 && (
                <div className="flex items-center justify-center py-1">
                  <button
                    onClick={() => toggleRowOp(idx)}
                    className={`px-3 py-0.5 rounded-full text-[10px] font-bold uppercase tracking-wider transition-all ${
                      (rule.op || group.op) === 'AND'
                        ? 'bg-primary/10 text-primary hover:bg-primary/20'
                        : 'bg-amber-500/20 text-amber-600 dark:text-amber-400 hover:bg-amber-500/30'
                    }`}
                  >
                    {rule.op || group.op}
                  </button>
                </div>
              )}

              <div className={`group/rule rounded-xl border bg-muted/40 p-3 space-y-2 transition-colors ${
                isOrphaned
                  ? 'border-amber-500/50 hover:border-amber-500/70'
                  : ruleValueError
                  ? 'border-destructive/40 hover:border-destructive/60'
                  : 'border-border hover:border-border'
              }`}>
                {/* Edge case 3: orphaned field warning */}
                {isOrphaned && (
                  <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-amber-500/10 border border-amber-500/30 text-[11px] text-amber-600 dark:text-amber-400">
                    ⚠ Field "{rule.field}" is no longer accessible. Select a different field or remove this condition.
                  </div>
                )}
                {/* Inline toast: operator was auto-reset */}
                {resetNotice === idx && (
                  <div className="flex items-center gap-1.5 px-2.5 py-1.5 rounded-lg bg-amber-500/10 border border-amber-500/30 text-[11px] text-amber-600 dark:text-amber-400 animate-pulse">
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
                    className="mt-0.5 w-7 h-7 rounded-full flex items-center justify-center text-muted-foreground/70 hover:text-destructive hover:bg-destructive/10 transition-colors opacity-0 group-hover/rule:opacity-100"
                  >
                    <X className="h-3.5 w-3.5" />
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
                      className="bg-background border border-border rounded-lg px-2 py-1.5 text-sm text-foreground focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring min-w-[130px]"
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
                        <span className="text-xs text-muted-foreground font-medium">to</span>
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
                          className="w-16 bg-background border border-border rounded-lg px-2 py-1.5 text-sm text-foreground text-center focus:outline-none focus:border-ring focus:ring-1 focus:ring-ring"
                        />
                        <span className="text-xs text-muted-foreground">days</span>
                      </div>
                    )}
                  </div>
                )}

                {/* Inline validation error for missing value */}
                {ruleValueError && (
                  <p className="text-[11px] text-destructive flex items-center gap-1">
                    <span>⚠</span> {ruleValueError}
                  </p>
                )}

                {/* Field type indicator */}
                {rule.field && (
                  <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground/70">
                    <span className="uppercase tracking-wider font-medium" style={{
                      color: TYPE_COLORS[fieldType] || '#6B7280',
                    }}>
                      {fieldType}
                    </span>
                    <span>·</span>
                    <span className="font-mono text-muted-foreground/70">{rule.field}</span>
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
        disabled={schemaLoading || !!schemaError || hasNoFields || group.rules.length >= MAX_CONDITIONS}
        className="w-full py-2 border border-dashed border-border rounded-lg text-sm text-muted-foreground hover:text-foreground hover:border-border transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
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
          className="text-xs text-destructive hover:text-destructive/80"
        >
          Remove all conditions
        </button>
      )}

      {/* Live preview — rich JSX with muted placeholders for missing values */}
      {previewNodes && (
        <div className={`px-3 py-2.5 rounded-lg border transition-colors ${
          isIncomplete
            ? 'bg-muted/40 border-border/60'
            : 'bg-primary/5 border-primary/40'
        }`}>
          <p className={`text-xs leading-relaxed ${
            isIncomplete ? 'text-muted-foreground' : 'text-primary/80'
          }`}>
            {previewNodes}
          </p>
          {isIncomplete && (
            <p className="text-[10px] text-muted-foreground mt-1 italic">
              ⚠ Fill in missing values to complete this rule.
            </p>
          )}
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
