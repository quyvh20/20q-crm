import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { getOperatorsForType, isNoValueOperator, isDualValueOperator, isRelativeDaysOperator } from '../useSchema';
import type { FiresOn } from '../useSchema';
import { useBuilderStore } from '../store';
import type { ConditionRule } from '../types';

// The builder's operator menus are hardcoded here in TypeScript; the automation
// engine's vocabulary is declared in Go. Nothing connected the two, and they
// drifted badly: for months the picker offered eight operators the API rejected
// with a raw HTTP 400 "unknown operator" the user could not act on, and two of
// them (is_true / is_false) were the ONLY choices for a boolean field, so no
// boolean condition could be saved at all.
//
// This test reads the Go declaration and fails if the frontend can emit anything
// the backend does not implement. It is deliberately one-directional: the backend
// may know operators the builder chooses not to offer (gte/lte are reachable
// through the API but not the menus), but the reverse is always a broken save.

const GO_SOURCE = resolve(__dirname, '../../../../../crm-backend/internal/automation/condition_operators.go');

/** Parse `{Value: "eq"}` / `{Value: "between", DualValue: true}` out of ConditionOperators. */
function backendOperators(): Map<string, { noValue: boolean; dualValue: boolean }> {
  const src = readFileSync(GO_SOURCE, 'utf8');
  const block = src.match(/var ConditionOperators = \[\]ConditionOperator\{([\s\S]*?)\n\}/);
  if (!block) throw new Error(`could not find ConditionOperators in ${GO_SOURCE}`);

  const ops = new Map<string, { noValue: boolean; dualValue: boolean }>();
  for (const line of block[1].split('\n')) {
    const m = line.match(/\{Value:\s*"([a-z_]+)"(.*)\}/);
    if (!m) continue;
    ops.set(m[1], { noValue: /NoValue:\s*true/.test(m[2]), dualValue: /DualValue:\s*true/.test(m[2]) });
  }
  if (ops.size === 0) throw new Error('parsed zero operators — the Go declaration shape changed');
  return ops;
}

const FIELD_TYPES = ['string', 'number', 'boolean', 'date', 'select', 'array', 'something_unknown'];
const FIRES_ON: FiresOn[] = ['created', 'updated', 'deleted', 'any'];

/** Every operator the builder can put in front of a user, across every menu. */
function frontendOperators() {
  const seen = new Map<string, { noValue: boolean; dualValue: boolean }>();
  for (const type of FIELD_TYPES) {
    for (const firesOn of FIRES_ON) {
      for (const op of getOperatorsForType(type, firesOn)) {
        seen.set(op.value, { noValue: !!op.noValue, dualValue: !!op.dualValue });
      }
    }
  }
  return seen;
}

describe('condition operator parity', () => {
  it('every operator the builder offers is implemented by the engine', () => {
    const be = backendOperators();
    const unknown = [...frontendOperators().keys()].filter((op) => !be.has(op));

    expect(unknown, `these would 400 on save with "unknown operator": ${unknown.join(', ')}`).toEqual([]);
  });

  it('the two sides agree on how many operands each operator takes', () => {
    // A mismatch here is quieter than an unknown operator and worse: the rule
    // saves, then evaluates against an operand shape the engine cannot read.
    const be = backendOperators();
    for (const [op, fe] of frontendOperators()) {
      const spec = be.get(op);
      if (!spec) continue; // reported by the test above
      expect({ op, noValue: fe.noValue }).toEqual({ op, noValue: spec.noValue });
      expect({ op, dualValue: fe.dualValue }).toEqual({ op, dualValue: spec.dualValue });
    }
  });

  it('the operand-shape helpers agree with the menus', () => {
    // isNoValueOperator / isDualValueOperator are separate hardcoded sets from
    // the menu definitions, and ConditionConfig renders from the HELPERS — so a
    // menu entry flagged dualValue whose helper disagrees renders one input for
    // a two-operand operator.
    for (const [op, fe] of frontendOperators()) {
      expect({ op, v: isNoValueOperator(op) }).toEqual({ op, v: fe.noValue });
      expect({ op, v: isDualValueOperator(op) }).toEqual({ op, v: fe.dualValue });
    }
  });

  it('a relative-days operator takes neither a field-typed value nor two of them', () => {
    // Its operand is a day COUNT. If it ever also reported dualValue or a plain
    // single value, ConditionConfig would render two inputs writing one `value`.
    for (const [op, fe] of frontendOperators()) {
      if (!isRelativeDaysOperator(op)) continue;
      expect(fe.dualValue).toBe(false);
      expect(fe.noValue).toBe(false);
    }
  });

  it('names the relative-date operator the way the rest of the product does', () => {
    // The lists/reports/segments compiler calls it last_n_days. The builder used
    // to call the same concept in_last_days — one operator, two names.
    const fe = frontendOperators();
    expect(fe.has('last_n_days')).toBe(true);
    expect(fe.has('in_last_days')).toBe(false);
  });

  it('no longer offers change-detection operators', () => {
    // Removed 2026-08-18: the engine's EvalContext carries no before-state, so
    // these could only ever have returned false. See the note in useSchema.ts.
    const fe = frontendOperators();
    for (const op of ['is_changed', 'is_set', 'is_cleared', 'changed_from_to']) {
      expect(fe.has(op), `${op} is back in the menus but still unevaluable`).toBe(false);
    }
  });
});

// The operand-emptiness rule in store.validate() is separate from the menus and
// broke once already: applying the two-element requirement to EVERY array made a
// one-chip `in` read as an empty value, with the error appearing and disappearing
// as the user added chips.
describe('condition value validation', () => {
  const seed = (rules: ConditionRule[]) => {
    useBuilderStore.getState().reset();
    useBuilderStore.setState({
      trigger: { type: 'contact_created', params: {} },
      conditions: { op: 'AND', rules },
      steps: [{ id: 'a1', type: 'action', action: { id: 'a1', type: 'create_task', params: { title: 't' } } }],
      actions: [{ id: 'a1', type: 'create_task', params: { title: 't' } }],
      name: 'w',
    });
    useBuilderStore.getState().validate();
    return useBuilderStore.getState().errors['conditions.rules.0.value'];
  };

  it('accepts a multi-value operator with any number of values', () => {
    expect(seed([{ field: 'contact.status', operator: 'in', value: ['active'] }])).toBeUndefined();
    expect(seed([{ field: 'contact.status', operator: 'in', value: ['a', 'b', 'c'] }])).toBeUndefined();
    expect(seed([{ field: 'contact.tags', operator: 'contains', value: ['vip'] }])).toBeUndefined();
  });

  it('rejects an empty multi-value selection', () => {
    expect(seed([{ field: 'contact.status', operator: 'in', value: [] }])).toBeDefined();
  });

  it('requires BOTH ends of a range', () => {
    expect(seed([{ field: 'contact.score', operator: 'between', value: [1, 10] }])).toBeUndefined();
    // The builder seeds a fresh range as ['','']; that must not save.
    expect(seed([{ field: 'contact.score', operator: 'between', value: ['', ''] }])).toBeDefined();
    expect(seed([{ field: 'contact.score', operator: 'between', value: [1, ''] }])).toBeDefined();
    expect(seed([{ field: 'contact.score', operator: 'between', value: [1] }])).toBeDefined();
    expect(seed([{ field: 'contact.score', operator: 'between', value: 5 }])).toBeDefined();
  });

  it('still accepts and rejects ordinary single values as before', () => {
    expect(seed([{ field: 'contact.email', operator: 'eq', value: 'a@b.com' }])).toBeUndefined();
    expect(seed([{ field: 'contact.email', operator: 'eq', value: '' }])).toBeDefined();
    expect(seed([{ field: 'contact.email', operator: 'is_empty', value: '' }])).toBeUndefined();
  });
});
