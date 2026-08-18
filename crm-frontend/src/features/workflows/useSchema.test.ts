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

  it('date → gt (after), lt (before), between, last_n_days, is_empty, is_not_empty', () => {
    const ops = getOperatorsForType('date');
    const values = ops.map((o) => o.value);
    // last_n_days, not in_last_days: one name for one operator, matching the
    // lists/reports/segments compiler that already implements it.
    expect(values).toEqual(['gt', 'lt', 'between', 'last_n_days', 'is_empty', 'is_not_empty']);
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

// Change-detection operators were REMOVED on 2026-08-18. The engine's
// EvalContext carries no before-state — only one of six *_updated emitters
// snapshots the old record, and buildEvalContext discards the changed-field
// list anyway because it is an array — so these could never have evaluated to
// anything but false. They were also offered on email_opened / webhook_inbound /
// schedule triggers, where nothing changed at all. Change detection lives at the
// TRIGGER instead, as watch_field / watch_value.
//
// These assert ABSENCE on purpose: re-adding them to the menus without first
// plumbing a before/after diff into EvalContext would turn a loud 400 at save
// into a silent permanent No branch.
describe('getOperatorsForType — change-detection operators are not offered', () => {
  const REMOVED = ['is_changed', 'is_set', 'is_cleared', 'changed_from_to'];

  it.each(['string', 'number', 'date', 'boolean', 'select', 'array', 'foobar'])(
    '%s offers none of them, on any fires-on',
    (type) => {
      const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
      for (const firesOn of ['created', 'updated', 'deleted', 'any'] as const) {
        const values = getOperatorsForType(type, firesOn).map((o) => o.value);
        for (const op of REMOVED) expect(values).not.toContain(op);
      }
      warnSpy.mockRestore();
    },
  );

  it('still offers the base operators it always did on an updated trigger', () => {
    const values = getOperatorsForType('string', 'updated').map((o) => o.value);
    expect(values).toContain('eq');
    expect(values).toContain('contains');
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
  });

  it('no longer marks the removed change-detection operators', () => {
    expect(isNoValueOperator('is_changed')).toBe(false);
    expect(isNoValueOperator('is_set')).toBe(false);
    expect(isNoValueOperator('is_cleared')).toBe(false);
  });

  it('marks eq, contains, etc. as requiring value', () => {
    expect(isNoValueOperator('eq')).toBe(false);
    expect(isNoValueOperator('contains')).toBe(false);
    expect(isNoValueOperator('gt')).toBe(false);
  });
});

describe('isDualValueOperator', () => {
  it('between is dual-value; changed_from_to no longer exists', () => {
    expect(isDualValueOperator('between')).toBe(true);
    expect(isDualValueOperator('changed_from_to')).toBe(false);
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

  it('a removed change-detection op resets to the type default rather than sticking', () => {
    // A workflow saved before the removal cannot exist (these never passed
    // validation), but a stale draft in local state can still carry one — it
    // must fall back, not persist an operator no menu offers.
    expect(simulateOperatorReset('is_changed', 'number', 'updated')).toBe('eq');
    expect(simulateOperatorReset('is_changed', 'string', 'updated')).toBe('eq');
    expect(simulateOperatorReset('is_changed', 'boolean', 'updated')).toBe('is_true');
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
