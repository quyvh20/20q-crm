import { describe, expect, it } from 'vitest';
import type { FilterGroupAST } from '../../../../lib/api';
import {
  astToUiState,
  conditionSummary,
  defaultOperatorFor,
  EMPTY_FILTER_STATE,
  filterStateToAst,
  isConditionComplete,
  operatorsFor,
  operatorValueKind,
  parseFilterParam,
  serializeFilterParam,
} from '../filterModel';

describe('filter URL codec', () => {
  it('round-trips conditions through the flt param', () => {
    const state = {
      match: 'any' as const,
      conditions: [
        { field: 'email', operator: 'contains', value: '@gmail' },
        { field: 'lead_score', operator: 'between', value: ['10', '50'] },
        { field: 'company', operator: 'is_empty', value: undefined },
      ],
    };
    const raw = serializeFilterParam(state);
    expect(raw).toBeTruthy();
    expect(parseFilterParam(raw)).toEqual(state);
  });

  it('serializes an empty state to null so the param leaves the URL', () => {
    expect(serializeFilterParam(EMPTY_FILTER_STATE)).toBeNull();
  });

  it('falls back to the empty state on a mangled param instead of throwing', () => {
    expect(parseFilterParam('{not json')).toEqual(EMPTY_FILTER_STATE);
    expect(parseFilterParam('{"c":"nope"}')).toEqual(EMPTY_FILTER_STATE);
    expect(parseFilterParam(null)).toEqual(EMPTY_FILTER_STATE);
  });
});

describe('filterStateToAst', () => {
  it('compiles complete conditions and maps any → OR', () => {
    const ast = filterStateToAst({
      match: 'any',
      conditions: [
        { field: 'email', operator: 'contains', value: 'x' },
        { field: 'status', operator: 'is_empty', value: undefined },
      ],
    });
    expect(ast).toEqual({
      op: 'OR',
      rules: [
        { field: 'email', operator: 'contains', value: 'x' },
        { field: 'status', operator: 'is_empty' },
      ],
    });
  });

  it('drops incomplete conditions and returns undefined when none survive', () => {
    expect(
      filterStateToAst({ match: 'all', conditions: [{ field: 'email', operator: 'contains', value: '' }] }),
    ).toBeUndefined();
  });

  it('sends day counts as numbers, not strings', () => {
    const ast = filterStateToAst({
      match: 'all',
      conditions: [{ field: 'created_at', operator: 'last_n_days', value: '30' }],
    });
    expect(ast?.rules[0].value).toBe(30);
  });
});

describe('astToUiState', () => {
  it('maps a flat AST back to conditions', () => {
    const ast: FilterGroupAST = {
      op: 'OR',
      rules: [{ field: 'email', operator: 'eq', value: 'a@b.c' }],
    };
    expect(astToUiState(ast)).toEqual({
      match: 'any',
      conditions: [{ field: 'email', operator: 'eq', value: 'a@b.c' }],
    });
  });

  it('returns null for a nested AST the flat bar cannot render', () => {
    const ast: FilterGroupAST = {
      op: 'AND',
      rules: [{ op: 'OR', rules: [{ field: 'email', operator: 'is_empty' }] }],
    };
    expect(astToUiState(ast)).toBeNull();
  });

  it('treats an absent filter as the empty state', () => {
    expect(astToUiState(undefined)).toEqual(EMPTY_FILTER_STATE);
  });
});

describe('operator metadata', () => {
  it('classifies value shapes per operator', () => {
    expect(operatorValueKind('is_empty')).toBe('none');
    expect(operatorValueKind('between')).toBe('pair');
    expect(operatorValueKind('in')).toBe('list');
    expect(operatorValueKind('last_n_days')).toBe('days');
    expect(operatorValueKind('eq')).toBe('single');
  });

  it('judges completeness by shape', () => {
    expect(isConditionComplete({ field: 'f', operator: 'is_empty' })).toBe(true);
    expect(isConditionComplete({ field: 'f', operator: 'between', value: ['1', ''] })).toBe(false);
    expect(isConditionComplete({ field: 'f', operator: 'between', value: ['1', '2'] })).toBe(true);
    expect(isConditionComplete({ field: 'f', operator: 'last_n_days', value: '0' })).toBe(false);
    expect(isConditionComplete({ field: 'f', operator: 'last_n_days', value: '7' })).toBe(true);
    expect(isConditionComplete({ field: 'f', operator: 'in', value: [] })).toBe(false);
  });

  it('text defaults to contains, dates to on, the rest to eq', () => {
    expect(defaultOperatorFor('text')).toBe('contains');
    expect(defaultOperatorFor('date')).toBe('on');
    expect(defaultOperatorFor('relation')).toBe('eq');
  });

  it('hides relation in/not_in until a multi-record picker exists', () => {
    const relation = { key: 'company', label: 'Company', type: 'relation' as const };
    const served = { relation: ['eq', 'neq', 'in', 'not_in', 'is_empty', 'is_not_empty'] };
    expect(operatorsFor(relation, served)).toEqual(['eq', 'neq', 'is_empty', 'is_not_empty']);
  });
});

describe('conditionSummary', () => {
  const email = { key: 'email', label: 'Email', type: 'text' as const };

  it('renders field, operator and resolved value', () => {
    const s = conditionSummary(email, { field: 'email', operator: 'contains', value: '@x' }, (_f, v) => String(v));
    expect(s).toBe('Email contains @x');
  });

  it('substitutes the day count into relative-date labels', () => {
    const created = { key: 'created_at', label: 'Created At', type: 'date' as const };
    const s = conditionSummary(created, { field: 'created_at', operator: 'last_n_days', value: 7 }, (_f, v) => String(v));
    expect(s).toBe('Created At in the last 7 days');
  });
});
