import type { ReportFieldDescriptor, ReportFilterGroup, ReportFilterRule } from '../../../lib/api';

// Flat filter rows (field + operator + value) joined by one AND/OR toggle —
// the same leaf shape the workflow condition builder produces. Nested groups
// stay representable in the stored JSON; the UI keeps to one level.

interface OpDef { value: string; label: string; needsValue: boolean }

const TEXT_OPS: OpDef[] = [
  { value: 'eq', label: 'equals', needsValue: true },
  { value: 'neq', label: 'not equals', needsValue: true },
  { value: 'contains', label: 'contains', needsValue: true },
  { value: 'not_contains', label: "doesn't contain", needsValue: true },
  { value: 'starts_with', label: 'starts with', needsValue: true },
  { value: 'ends_with', label: 'ends with', needsValue: true },
  { value: 'is_empty', label: 'is empty', needsValue: false },
  { value: 'is_not_empty', label: 'is not empty', needsValue: false },
];

const OPERATORS: Record<string, OpDef[]> = {
  text: TEXT_OPS,
  url: TEXT_OPS,
  select: [
    { value: 'eq', label: 'is', needsValue: true },
    { value: 'neq', label: 'is not', needsValue: true },
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
  number: [
    { value: 'eq', label: '=', needsValue: true },
    { value: 'neq', label: '≠', needsValue: true },
    { value: 'gt', label: '>', needsValue: true },
    { value: 'gte', label: '≥', needsValue: true },
    { value: 'lt', label: '<', needsValue: true },
    { value: 'lte', label: '≤', needsValue: true },
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
  date: [
    { value: 'gte', label: 'on or after', needsValue: true },
    { value: 'gt', label: 'after', needsValue: true },
    { value: 'lte', label: 'on or before', needsValue: true },
    { value: 'lt', label: 'before', needsValue: true },
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
  boolean: [{ value: 'eq', label: 'is', needsValue: true }],
  // Relations filter by presence only in the UI (picking related record ids
  // needs a record picker — group-by covers the common "per stage/owner" asks).
  relation: [
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
};

interface Props {
  fields: ReportFieldDescriptor[];
  value?: ReportFilterGroup;
  onChange: (g: ReportFilterGroup | undefined) => void;
}

export default function FilterEditor({ fields, value, onChange }: Props) {
  const rules: ReportFilterRule[] = value?.rules ?? [];
  const op = value?.op === 'OR' ? 'OR' : 'AND';

  const emit = (nextRules: ReportFilterRule[], nextOp: 'AND' | 'OR' = op) => {
    onChange(nextRules.length === 0 ? undefined : { op: nextOp, rules: nextRules });
  };

  const updateRule = (i: number, patch: Partial<ReportFilterRule>) => {
    const next = rules.map((r, j) => (j === i ? { ...r, ...patch } : r));
    emit(next);
  };

  const addRule = () => {
    const first = fields[0];
    if (!first) return;
    const firstOp = (OPERATORS[first.type] ?? TEXT_OPS)[0];
    emit([...rules, { field: first.key, operator: firstOp.value, value: defaultValueFor(first) }]);
  };

  const removeRule = (i: number) => emit(rules.filter((_, j) => j !== i));

  return (
    <div className="space-y-2">
      {rules.length > 1 && (
        <div className="flex items-center gap-2 text-xs">
          <span className="text-muted-foreground">Match</span>
          <button
            type="button"
            onClick={() => emit(rules, op === 'AND' ? 'OR' : 'AND')}
            className="rounded-md border px-2 py-1 font-medium hover:bg-accent"
          >
            {op === 'AND' ? 'ALL conditions (AND)' : 'ANY condition (OR)'}
          </button>
        </div>
      )}

      {rules.map((rule, i) => {
        const field = fields.find((f) => f.key === rule.field) ?? fields[0];
        const ops = OPERATORS[field?.type ?? 'text'] ?? TEXT_OPS;
        const opDef = ops.find((o) => o.value === rule.operator) ?? ops[0];
        return (
          <div key={i} className="flex items-center gap-2">
            <select
              aria-label="Filter field"
              value={rule.field}
              onChange={(e) => {
                const f = fields.find((x) => x.key === e.target.value);
                if (!f) return;
                const firstOp = (OPERATORS[f.type] ?? TEXT_OPS)[0];
                updateRule(i, { field: f.key, operator: firstOp.value, value: defaultValueFor(f) });
              }}
              className="w-40 rounded-md border bg-background px-2 py-1.5 text-sm"
            >
              {fields.map((f) => <option key={f.key} value={f.key}>{f.label}</option>)}
            </select>

            <select
              aria-label="Filter operator"
              value={rule.operator}
              onChange={(e) => {
                const nextOp = ops.find((o) => o.value === e.target.value);
                // Switching from a valueless operator (is_empty) to one that
                // needs a value leaves rule.value undefined, which the backend
                // rejects — seed a type-appropriate default so the preview stays
                // valid.
                const value = nextOp?.needsValue ? (rule.value ?? (field ? defaultValueFor(field) : '')) : undefined;
                updateRule(i, { operator: e.target.value, value });
              }}
              className="w-36 rounded-md border bg-background px-2 py-1.5 text-sm"
            >
              {ops.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>

            {opDef.needsValue && field && (
              <ValueInput field={field} value={rule.value} onChange={(v) => updateRule(i, { value: v })} />
            )}

            <button
              type="button"
              aria-label="Remove filter"
              onClick={() => removeRule(i)}
              className="rounded-md px-2 py-1 text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              ✕
            </button>
          </div>
        );
      })}

      <button
        type="button"
        onClick={addRule}
        className="rounded-md border border-dashed px-3 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
      >
        + Add filter
      </button>
    </div>
  );
}

function defaultValueFor(f: ReportFieldDescriptor): unknown {
  switch (f.type) {
    case 'boolean': return true;
    case 'number': return 0;
    case 'select': return f.options?.[0] ?? '';
    default: return '';
  }
}

function ValueInput({ field, value, onChange }: {
  field: ReportFieldDescriptor;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const cls = 'w-40 rounded-md border bg-background px-2 py-1.5 text-sm';
  switch (field.type) {
    case 'boolean':
      return (
        <select aria-label="Filter value" className={cls} value={value === false ? 'false' : 'true'} onChange={(e) => onChange(e.target.value === 'true')}>
          <option value="true">Yes</option>
          <option value="false">No</option>
        </select>
      );
    case 'number':
      return (
        <input
          aria-label="Filter value"
          type="number"
          className={cls}
          value={typeof value === 'number' ? value : ''}
          onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))}
        />
      );
    case 'date':
      return (
        <input
          aria-label="Filter value"
          type="date"
          className={cls}
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case 'select':
      return (
        <select aria-label="Filter value" className={cls} value={typeof value === 'string' ? value : ''} onChange={(e) => onChange(e.target.value)}>
          {(field.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
        </select>
      );
    default:
      return (
        <input
          aria-label="Filter value"
          type="text"
          className={cls}
          value={typeof value === 'string' ? value : String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
        />
      );
  }
}
