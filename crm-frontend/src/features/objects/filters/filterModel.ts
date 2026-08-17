import type { FilterFieldDescriptor, FilterGroupAST, FilterRuleAST, ObjectFieldType } from '../../../lib/api';

// ============================================================
// List filter model (filtering overhaul F6)
// ============================================================
//
// The UI speaks a FLAT list of conditions plus one all/any toggle — that covers
// the overwhelming share of real filters and serializes compactly into the URL.
// The wire/AST grammar underneath (FilterGroupAST) supports nested groups to
// depth 5; a saved view authored via API with nesting still round-trips through
// the URL untouched, it just renders as an opaque "advanced filter" pill.

export interface UiFilterCondition {
  field: string;
  operator: string;
  /**
   * Shape depends on the operator: string for single-value operators,
   * [string, string] for between, string[] for in/not_in, a number-ish string
   * for last_n_days/next_n_days, absent for is_empty/is_not_empty.
   */
  value?: unknown;
}

export type FilterMatch = 'all' | 'any';

export interface UiFilterState {
  match: FilterMatch;
  conditions: UiFilterCondition[];
}

export const EMPTY_FILTER_STATE: UiFilterState = { match: 'all', conditions: [] };

// ── URL codec ───────────────────────────────────────────────────────────────
//
// One `flt` param holding {"m":"any","c":[{"f","o","v"}...]} — JSON because
// values are arbitrary text and would re-split on any delimiter scheme, short
// keys because this rides every shared link.

interface WireCondition {
  f: string;
  o: string;
  v?: unknown;
}

export function serializeFilterParam(state: UiFilterState): string | null {
  if (state.conditions.length === 0) return null;
  const wire = {
    ...(state.match === 'any' ? { m: 'any' } : {}),
    c: state.conditions.map((c): WireCondition => ({ f: c.field, o: c.operator, ...(c.value === undefined ? {} : { v: c.value }) })),
  };
  return JSON.stringify(wire);
}

export function parseFilterParam(raw: string | null): UiFilterState {
  if (!raw) return EMPTY_FILTER_STATE;
  try {
    const parsed = JSON.parse(raw) as { m?: string; c?: WireCondition[] };
    const conditions = (parsed.c ?? [])
      .filter((c) => c && typeof c.f === 'string' && typeof c.o === 'string')
      .map((c) => ({ field: c.f, operator: c.o, value: c.v }));
    return { match: parsed.m === 'any' ? 'any' : 'all', conditions };
  } catch {
    // A hand-mangled URL is not worth an error screen; an empty filter is the
    // honest fallback (the list simply shows everything).
    return EMPTY_FILTER_STATE;
  }
}

// ── AST ─────────────────────────────────────────────────────────────────────

// filterStateToAst compiles the COMPLETE conditions into the wire AST the
// backend validates. Incomplete conditions never reach the URL (the editor
// holds drafts locally), but filter defensively anyway.
export function filterStateToAst(state: UiFilterState): FilterGroupAST | undefined {
  const rules = state.conditions
    .filter((c) => isConditionComplete(c))
    .map((c) => ({ field: c.field, operator: c.operator, ...(operatorValueKind(c.operator) === 'none' ? {} : { value: normalizeAstValue(c) }) }));
  if (rules.length === 0) return undefined;
  return { op: state.match === 'any' ? 'OR' : 'AND', rules };
}

// Numbers travel as numbers where the value is numeric by construction, so the
// backend's coercion never sees "50" vs 50 ambiguity for day counts.
function normalizeAstValue(c: UiFilterCondition): unknown {
  if (operatorValueKind(c.operator) === 'days') {
    const n = Number(c.value);
    return Number.isFinite(n) ? n : c.value;
  }
  return c.value;
}

// ── Operator metadata ───────────────────────────────────────────────────────

export type OperatorValueKind = 'none' | 'single' | 'pair' | 'list' | 'days';

export function operatorValueKind(op: string): OperatorValueKind {
  switch (op) {
    case 'is_empty':
    case 'is_not_empty':
      return 'none';
    case 'between':
      return 'pair';
    case 'in':
    case 'not_in':
      return 'list';
    case 'last_n_days':
    case 'next_n_days':
      return 'days';
    default:
      return 'single';
  }
}

export function isConditionComplete(c: UiFilterCondition): boolean {
  switch (operatorValueKind(c.operator)) {
    case 'none':
      return true;
    case 'pair': {
      const [a, b] = Array.isArray(c.value) ? (c.value as unknown[]) : [];
      return a != null && a !== '' && b != null && b !== '';
    }
    case 'list':
      return Array.isArray(c.value) && (c.value as unknown[]).length > 0;
    case 'days': {
      const n = Number(c.value);
      return Number.isFinite(n) && n >= 1;
    }
    default:
      return c.value != null && c.value !== '';
  }
}

// operatorLabel renders an operator for a field type — dates read as time
// ("is after"), numbers as comparison. The type-specific overrides come first.
const DATE_LABELS: Record<string, string> = {
  on: 'is on',
  gt: 'is after',
  gte: 'is on or after',
  lt: 'is before',
  lte: 'is on or before',
  between: 'is between',
  last_n_days: 'in the last N days',
  next_n_days: 'in the next N days',
  eq: 'equals exactly',
  neq: 'does not equal',
};
const COMMON_LABELS: Record<string, string> = {
  eq: 'is',
  neq: 'is not',
  contains: 'contains',
  not_contains: "doesn't contain",
  starts_with: 'starts with',
  ends_with: 'ends with',
  in: 'is any of',
  not_in: 'is none of',
  gt: 'greater than',
  gte: 'at least',
  lt: 'less than',
  lte: 'at most',
  between: 'is between',
  is_empty: 'is empty',
  is_not_empty: 'is not empty',
};

export function operatorLabel(type: ObjectFieldType | string, op: string): string {
  if (type === 'date' && DATE_LABELS[op]) return DATE_LABELS[op];
  return COMMON_LABELS[op] ?? op;
}

export function defaultOperatorFor(type: ObjectFieldType | string): string {
  switch (type) {
    case 'text':
    case 'url':
      return 'contains';
    case 'date':
      return 'on';
    default:
      return 'eq';
  }
}

// FALLBACK_OPERATORS keeps the bar usable against a server that predates the
// served matrix (schema.filter_operators). Mirrors domain.FilterOperatorsOrdered
// minus the new operators old servers would reject.
export const FALLBACK_OPERATORS: Record<string, string[]> = {
  text: ['eq', 'neq', 'contains', 'not_contains', 'starts_with', 'ends_with', 'is_empty', 'is_not_empty'],
  url: ['eq', 'neq', 'contains', 'not_contains', 'starts_with', 'ends_with', 'is_empty', 'is_not_empty'],
  select: ['eq', 'neq', 'in', 'not_in', 'is_empty', 'is_not_empty'],
  number: ['eq', 'neq', 'gt', 'gte', 'lt', 'lte', 'is_empty', 'is_not_empty'],
  date: ['gt', 'gte', 'lt', 'lte', 'is_empty', 'is_not_empty'],
  boolean: ['eq', 'neq', 'is_empty', 'is_not_empty'],
  relation: ['eq', 'neq', 'is_empty', 'is_not_empty'],
};

// operatorsFor narrows the served matrix to what this UI can actually edit:
// in/not_in need a multi-value picker, and the only one that exists is the
// options checkbox list — so they are offered ONLY for fields with declared
// options. A relation multi-record picker and a free-text list input are
// follow-ups; until then, offering the operator would produce a condition
// whose value editor can never be completed.
export function operatorsFor(
  field: FilterFieldDescriptor,
  served: Record<string, string[]> | undefined,
): string[] {
  const ops = served?.[field.type] ?? FALLBACK_OPERATORS[field.type] ?? [];
  if (field.options?.length) return ops;
  return ops.filter((o) => o !== 'in' && o !== 'not_in');
}

// astToUiState maps a wire AST back to the flat UI model — null when the AST
// uses nesting the flat bar cannot represent (an API-authored saved view). The
// caller then applies the AST opaquely instead of dropping it silently.
export function astToUiState(ast: FilterGroupAST | undefined | null): UiFilterState | null {
  if (!ast) return EMPTY_FILTER_STATE;
  if (!ast.rules?.length) {
    // The wire grammar (mirroring the backend's automation shape) allows a
    // LEAF disguised as the root group. Treating it as "no rules → empty"
    // would apply an API-authored view unfiltered under its own name — the
    // one outcome worse than refusing to render it.
    const leaf = ast as FilterRuleAST;
    if (leaf.field && leaf.operator) {
      return { match: 'all', conditions: [{ field: leaf.field, operator: leaf.operator, value: leaf.value }] };
    }
    return EMPTY_FILTER_STATE;
  }
  const conditions: UiFilterCondition[] = [];
  for (const r of ast.rules) {
    if (r.op || r.rules?.length) return null; // nested group — not flat-representable
    if (!r.field || !r.operator) return null;
    conditions.push({ field: r.field, operator: r.operator, value: r.value });
  }
  return { match: ast.op === 'OR' ? 'any' : 'all', conditions };
}

// conditionSummary renders a pill's text. resolveValue maps a raw value (e.g. a
// relation id) to a display label.
export function conditionSummary(
  field: FilterFieldDescriptor | undefined,
  c: UiFilterCondition,
  resolveValue: (field: FilterFieldDescriptor | undefined, v: unknown) => string,
): string {
  const label = field?.label ?? c.field;
  const op = operatorLabel(field?.type ?? 'text', c.operator);
  switch (operatorValueKind(c.operator)) {
    case 'none':
      return `${label} ${op}`;
    case 'pair': {
      const [a, b] = Array.isArray(c.value) ? (c.value as unknown[]) : ['', ''];
      return `${label} ${op} ${resolveValue(field, a)} – ${resolveValue(field, b)}`;
    }
    case 'list': {
      const items = Array.isArray(c.value) ? (c.value as unknown[]) : [];
      return `${label} ${op} ${items.map((v) => resolveValue(field, v)).join(', ')}`;
    }
    case 'days':
      return `${label} ${op.replace('N', String(c.value ?? '?'))}`;
    default:
      return `${label} ${op} ${resolveValue(field, c.value)}`;
  }
}
