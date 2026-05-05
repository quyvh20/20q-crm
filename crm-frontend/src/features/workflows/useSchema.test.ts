import { describe, it, expect, vi } from 'vitest';
import { getOperatorsForType, isNoValueOperator, isDualValueOperator } from './useSchema';
import type { FiresOn } from './useSchema';

describe('getOperatorsForType — Created mode (default)', () => {
  it('boolean → is_true, is_false', () => {
    const ops = getOperatorsForType('boolean');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['is_true', 'is_false']);
    expect(ops.every((o) => o.noValue)).toBe(true);
  });

  it('number → eq, neq, gt, lt, between, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('number');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['eq', 'neq', 'gt', 'lt', 'between', 'is_empty', 'is_not_empty']);
    expect(ops.find((o) => o.value === 'between')?.dualValue).toBe(true);
  });

  it('string → eq, neq, contains, not_contains, starts_with, ends_with, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('string');
    const values = ops.map((o) => o.value);
    expect(values).toEqual([
      'eq', 'neq', 'contains', 'not_contains',
      'starts_with', 'ends_with', 'is_empty', 'is_not_empty',
    ]);
  });

  it('array → contains, not_contains, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('array');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['contains', 'not_contains', 'is_empty', 'is_not_empty']);
  });

  it('select → in, not_in, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('select');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['in', 'not_in', 'is_empty', 'is_not_empty']);
  });

  it('date → gt (after), lt (before), between, in_last_days, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('date');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['gt', 'lt', 'between', 'in_last_days', 'is_empty', 'is_not_empty']);
    expect(ops.find((o) => o.value === 'gt')?.label).toBe('after');
  });

  it('unknown type → falls back to string operators + logs warning', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const ops = getOperatorsForType('foobar');
    const values = ops.map((o) => o.value);
    expect(ops.length).toBeGreaterThan(0);
    expect(values).toContain('eq');
    expect(warnSpy).toHaveBeenCalledWith(
      '[getOperatorsForType] Unknown field type "foobar" — falling back to string operators',
    );
    warnSpy.mockRestore();
  });

  it('boolean must not include string-only ops', () => {
    const ops = getOperatorsForType('boolean');
    const values = ops.map((o) => o.value);
    expect(values).not.toContain('contains');
    expect(values).not.toContain('starts_with');
    expect(values).not.toContain('gt');
    expect(values).not.toContain('in');
  });

  it('number must not include string-only ops', () => {
    const ops = getOperatorsForType('number');
    const values = ops.map((o) => o.value);
    expect(values).not.toContain('contains');
    expect(values).not.toContain('starts_with');
    expect(values).not.toContain('in');
    expect(values).not.toContain('not_in');
  });
});

describe('getOperatorsForType — Updated mode (change-detection)', () => {
  it('string (updated) includes change-detection operators', () => {
    const ops = getOperatorsForType('string', 'updated');
    const values = ops.map((o) => o.value);
    expect(values).toContain('is_changed');
    expect(values).toContain('is_set');
    expect(values).toContain('is_cleared');
    expect(values).toContain('changed_from_to');
    // Also includes base ops
    expect(values).toContain('eq');
    expect(values).toContain('contains');
  });

  it('boolean (updated) includes is_changed but NOT changed_from_to', () => {
    const ops = getOperatorsForType('boolean', 'updated');
    const values = ops.map((o) => o.value);
    expect(values).toContain('is_changed');
    expect(values).not.toContain('changed_from_to');
  });

  it('number (any) includes change-detection operators', () => {
    const ops = getOperatorsForType('number', 'any');
    const values = ops.map((o) => o.value);
    expect(values).toContain('is_changed');
    expect(values).toContain('is_set');
    expect(values).toContain('changed_from_to');
  });
});

describe('getOperatorsForType — Deleted mode (minimal)', () => {
  it('string (deleted) → eq, is_empty only', () => {
    const ops = getOperatorsForType('string', 'deleted');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['eq', 'is_empty']);
  });

  it('boolean (deleted) → is_true, is_false only', () => {
    const ops = getOperatorsForType('boolean', 'deleted');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['is_true', 'is_false']);
  });

  it('date (deleted) → is_empty only', () => {
    const ops = getOperatorsForType('date', 'deleted');
    const values = ops.map((o) => o.value);
    expect(values).toEqual(['is_empty']);
  });
});

describe('isNoValueOperator', () => {
  it('marks is_empty, is_not_empty, is_true, is_false as no-value', () => {
    expect(isNoValueOperator('is_empty')).toBe(true);
    expect(isNoValueOperator('is_not_empty')).toBe(true);
    expect(isNoValueOperator('is_true')).toBe(true);
    expect(isNoValueOperator('is_false')).toBe(true);
    expect(isNoValueOperator('is_changed')).toBe(true);
    expect(isNoValueOperator('is_set')).toBe(true);
    expect(isNoValueOperator('is_cleared')).toBe(true);
  });

  it('marks eq, contains, etc. as requiring value', () => {
    expect(isNoValueOperator('eq')).toBe(false);
    expect(isNoValueOperator('contains')).toBe(false);
    expect(isNoValueOperator('gt')).toBe(false);
  });
});

describe('isDualValueOperator', () => {
  it('between and changed_from_to are dual-value', () => {
    expect(isDualValueOperator('between')).toBe(true);
    expect(isDualValueOperator('changed_from_to')).toBe(true);
  });

  it('eq, contains, etc. are not dual-value', () => {
    expect(isDualValueOperator('eq')).toBe(false);
    expect(isDualValueOperator('gt')).toBe(false);
  });
});

// --- Operator reset simulation ---
function simulateOperatorReset(currentOperator: string, newFieldType: string, firesOn: FiresOn = 'created') {
  const validOps = getOperatorsForType(newFieldType, firesOn);
  const currentOpStillValid = validOps.some((op) => op.value === currentOperator);
  return currentOpStillValid ? currentOperator : validOps[0].value;
}

describe('TestOperatorReset_OnFieldTypeChange', () => {
  it('string "contains" → boolean: resets to "is_true"', () => {
    expect(simulateOperatorReset('contains', 'boolean')).toBe('is_true');
  });

  it('string "starts_with" → number: resets to "eq"', () => {
    expect(simulateOperatorReset('starts_with', 'number')).toBe('eq');
  });

  it('string "is_empty" → array: keeps "is_empty" (valid in both)', () => {
    expect(simulateOperatorReset('is_empty', 'array')).toBe('is_empty');
  });

  it('string "contains" → array: keeps "contains" (valid in both)', () => {
    expect(simulateOperatorReset('contains', 'array')).toBe('contains');
  });

  it('boolean "is_true" → string: resets to "eq"', () => {
    expect(simulateOperatorReset('is_true', 'string')).toBe('eq');
  });

  it('number "gt" → string: resets to "eq" (gt not in string)', () => {
    expect(simulateOperatorReset('gt', 'string')).toBe('eq');
  });

  it('change-detection op preserved during type change in updated mode', () => {
    // is_changed is valid for all types in updated mode
    expect(simulateOperatorReset('is_changed', 'number', 'updated')).toBe('is_changed');
    expect(simulateOperatorReset('is_changed', 'string', 'updated')).toBe('is_changed');
    expect(simulateOperatorReset('is_changed', 'boolean', 'updated')).toBe('is_changed');
  });
});

// --- Toast trigger logic ---
function shouldShowToast(
  currentOperator: string,
  currentField: string | null,
  oldFieldType: string,
  newFieldType: string,
  firesOn: FiresOn = 'created',
): boolean {
  if (oldFieldType === newFieldType && currentField) return false;
  const validOps = getOperatorsForType(newFieldType, firesOn);
  const currentOpStillValid = validOps.some((op) => op.value === currentOperator);
  const didReset = !currentOpStillValid && !!currentField;
  return didReset;
}

describe('TestOperatorReset_ShowsToast', () => {
  it('shows toast: string "contains" → boolean (operator incompatible)', () => {
    expect(shouldShowToast('contains', 'contact.first_name', 'string', 'boolean')).toBe(true);
  });

  it('shows toast: string "starts_with" → number (operator incompatible)', () => {
    expect(shouldShowToast('starts_with', 'contact.first_name', 'string', 'number')).toBe(true);
  });

  it('shows toast: number "gt" → boolean (operator incompatible)', () => {
    expect(shouldShowToast('gt', 'deal.value', 'number', 'boolean')).toBe(true);
  });

  it('no toast: string "is_empty" → array (is_empty valid in both)', () => {
    expect(shouldShowToast('is_empty', 'contact.first_name', 'string', 'array')).toBe(false);
  });

  it('no toast: first field selection (currentField is null)', () => {
    expect(shouldShowToast('eq', null, 'string', 'boolean')).toBe(false);
  });

  it('no toast: first field selection (currentField is empty string)', () => {
    expect(shouldShowToast('contains', '', 'string', 'boolean')).toBe(false);
  });

  it('no toast: same type different field (string → string)', () => {
    expect(shouldShowToast('contains', 'contact.first_name', 'string', 'string')).toBe(false);
  });

  it('no toast: string "contains" → array (contains valid in both)', () => {
    expect(shouldShowToast('contains', 'contact.first_name', 'string', 'array')).toBe(false);
  });
});
