import { describe, it, expect, vi } from 'vitest';
import { getOperatorsForType } from './useSchema';

describe('getOperatorsForType', () => {
  // --- Boolean ---
  it('TestOperatorFilter_BooleanFieldShowsOnlyEqNeq', () => {
    const ops = getOperatorsForType('boolean');
    const values = ops.map((o) => o.value);

    expect(values).toEqual(['eq', 'neq']);
    expect(ops.find((o) => o.value === 'eq')?.label).toBe('Is');
    expect(ops.find((o) => o.value === 'neq')?.label).toBe('Is not');
  });

  // --- Number ---
  it('number → eq, neq, gt, gte, lt, lte', () => {
    const ops = getOperatorsForType('number');
    const values = ops.map((o) => o.value);

    expect(values).toEqual(['eq', 'neq', 'gt', 'gte', 'lt', 'lte']);
    expect(ops.find((o) => o.value === 'gte')?.label).toBe('Greater than or equal');
  });

  // --- String ---
  it('string → eq, neq, contains, not_contains, starts_with, ends_with, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('string');
    const values = ops.map((o) => o.value);

    expect(values).toEqual([
      'eq', 'neq', 'contains', 'not_contains',
      'starts_with', 'ends_with', 'is_empty', 'is_not_empty',
    ]);
    expect(ops.find((o) => o.value === 'is_empty')?.label).toBe('Is empty');
  });

  // --- Array ---
  it('array → contains, not_contains, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('array');
    const values = ops.map((o) => o.value);

    expect(values).toEqual(['contains', 'not_contains', 'is_empty', 'is_not_empty']);
  });

  // --- Select ---
  it('select → eq, neq, in, not_in', () => {
    const ops = getOperatorsForType('select');
    const values = ops.map((o) => o.value);

    expect(values).toEqual(['eq', 'neq', 'in', 'not_in']);
  });

  // --- Date ---
  it('date → eq, gt, lt, gte, lte', () => {
    const ops = getOperatorsForType('date');
    const values = ops.map((o) => o.value);

    expect(values).toEqual(['eq', 'gt', 'lt', 'gte', 'lte']);
    expect(ops.find((o) => o.value === 'gte')?.label).toBe('On or after');
  });

  // --- Unknown type fallback ---
  it('unknown type → falls back to string operators + logs warning', () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});

    const ops = getOperatorsForType('foobar');
    const values = ops.map((o) => o.value);

    // Must not be empty — always has at least eq
    expect(ops.length).toBeGreaterThan(0);
    expect(values).toContain('eq');

    // Same shape as string ops
    expect(values).toEqual([
      'eq', 'neq', 'contains', 'not_contains',
      'starts_with', 'ends_with', 'is_empty', 'is_not_empty',
    ]);

    // Warning was logged
    expect(warnSpy).toHaveBeenCalledWith(
      '[getOperatorsForType] Unknown field type "foobar" — falling back to string operators',
    );

    warnSpy.mockRestore();
  });

  // --- Negative: boolean must NOT include string-only ops ---
  it('boolean must not include contains, starts_with, etc.', () => {
    const ops = getOperatorsForType('boolean');
    const values = ops.map((o) => o.value);

    expect(values).not.toContain('contains');
    expect(values).not.toContain('starts_with');
    expect(values).not.toContain('gt');
    expect(values).not.toContain('in');
  });

  // --- Negative: number must NOT include string ops ---
  it('number must not include contains, starts_with, in, not_in', () => {
    const ops = getOperatorsForType('number');
    const values = ops.map((o) => o.value);

    expect(values).not.toContain('contains');
    expect(values).not.toContain('starts_with');
    expect(values).not.toContain('in');
    expect(values).not.toContain('not_in');
  });
});

/**
 * Mirrors the reset logic from ConditionConfigPanel.handleFieldChange:
 *   const validOps = getOperatorsForType(newFieldType);
 *   const currentOpStillValid = validOps.some(op => op.value === currentOperator);
 *   resolvedOp = currentOpStillValid ? currentOperator : validOps[0].value;
 */
function simulateOperatorReset(currentOperator: string, newFieldType: string) {
  const validOps = getOperatorsForType(newFieldType);
  const currentOpStillValid = validOps.some((op) => op.value === currentOperator);
  return currentOpStillValid ? currentOperator : validOps[0].value;
}

describe('TestOperatorReset_OnFieldTypeChange', () => {
  it('string "contains" → boolean: resets to "eq"', () => {
    const result = simulateOperatorReset('contains', 'boolean');
    expect(result).toBe('eq');
  });

  it('string "starts_with" → number: resets to "eq"', () => {
    const result = simulateOperatorReset('starts_with', 'number');
    expect(result).toBe('eq');
  });

  it('string "eq" → boolean: keeps "eq" (valid in both)', () => {
    const result = simulateOperatorReset('eq', 'boolean');
    expect(result).toBe('eq');
  });

  it('string "eq" → number: keeps "eq" (valid in both)', () => {
    const result = simulateOperatorReset('eq', 'number');
    expect(result).toBe('eq');
  });

  it('number "gt" → string: resets to "eq" (gt not in string)', () => {
    const result = simulateOperatorReset('gt', 'string');
    expect(result).toBe('eq');
  });

  it('number "gte" → boolean: resets to "eq"', () => {
    const result = simulateOperatorReset('gte', 'boolean');
    expect(result).toBe('eq');
  });

  it('string "is_empty" → array: keeps "is_empty" (valid in both)', () => {
    const result = simulateOperatorReset('is_empty', 'array');
    expect(result).toBe('is_empty');
  });

  it('string "contains" → array: keeps "contains" (valid in both)', () => {
    const result = simulateOperatorReset('contains', 'array');
    expect(result).toBe('contains');
  });

  it('array "is_not_empty" → boolean: resets to "eq"', () => {
    const result = simulateOperatorReset('is_not_empty', 'boolean');
    expect(result).toBe('eq');
  });

  it('select "in" → string: resets to "eq" (in not in string)', () => {
    const result = simulateOperatorReset('in', 'string');
    expect(result).toBe('eq');
  });

  it('date "gt" → number: keeps "gt" (valid in both)', () => {
    const result = simulateOperatorReset('gt', 'number');
    expect(result).toBe('gt');
  });

  it('number "neq" → boolean: keeps "neq" (valid in both)', () => {
    const result = simulateOperatorReset('neq', 'boolean');
    expect(result).toBe('neq');
  });
});

/**
 * Mirrors the toast trigger logic from ConditionConfigPanel.handleFieldChange:
 *   const didReset = !currentOpStillValid && !!currentRule.field;
 *   if (didReset) showResetNotice(index);
 *
 * Toast should fire ONLY when:
 *   1. The operator was actually changed (not still valid)
 *   2. A field was already selected (not first selection)
 */
function shouldShowToast(
  currentOperator: string,
  currentField: string | null,
  oldFieldType: string,
  newFieldType: string,
): boolean {
  if (oldFieldType === newFieldType && currentField) return false; // same type, no reset
  const validOps = getOperatorsForType(newFieldType);
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

  it('shows toast: number "gte" → boolean (operator incompatible)', () => {
    expect(shouldShowToast('gte', 'deal.value', 'number', 'boolean')).toBe(true);
  });

  it('shows toast: array "is_not_empty" → boolean (operator incompatible)', () => {
    expect(shouldShowToast('is_not_empty', 'contact.tags', 'array', 'boolean')).toBe(true);
  });

  it('no toast: string "eq" → boolean (eq valid in both)', () => {
    expect(shouldShowToast('eq', 'contact.first_name', 'string', 'boolean')).toBe(false);
  });

  it('no toast: string "neq" → number (neq valid in both)', () => {
    expect(shouldShowToast('neq', 'contact.first_name', 'string', 'number')).toBe(false);
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

  it('no toast: string "is_empty" → array (is_empty valid in both)', () => {
    expect(shouldShowToast('is_empty', 'contact.first_name', 'string', 'array')).toBe(false);
  });
});
