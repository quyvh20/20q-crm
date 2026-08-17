import { useMemo, useState } from 'react';
import { ListFilter, Pencil, Plus, X } from 'lucide-react';
import { Button, Popover, PopoverAnchor, Select } from '@/components/ui';
import type { FilterFieldDescriptor, ObjectSchema } from '../../../lib/api';
import FilterValueInput, { type ValueSources } from './FilterValueInput';
import {
  conditionSummary,
  defaultOperatorFor,
  isConditionComplete,
  operatorLabel,
  operatorsFor,
  operatorValueKind,
  type UiFilterCondition,
  type UiFilterState,
} from './filterModel';

// FilterBar is the list's condition builder: active conditions render as
// editable pills, "+ Filter" opens a searchable field picker, and each
// condition edits in an anchored popover (field-appropriate operator menu +
// value editor, both derived from the server-served catalog and matrix).
//
// The bar edits a DRAFT locally and only calls onChange with complete
// conditions, so a half-built condition never hits the URL or the server.
export default function FilterBar({
  schema,
  state,
  onChange,
  sources,
  resolveValue,
  advancedActive,
  onClearAdvanced,
}: {
  schema: ObjectSchema;
  state: UiFilterState;
  onChange: (next: UiFilterState) => void;
  sources: ValueSources;
  resolveValue: (field: FilterFieldDescriptor | undefined, v: unknown) => string;
  /** True when an opaque (nested, view-authored) filter AST is applied. */
  advancedActive?: boolean;
  onClearAdvanced?: () => void;
}) {
  const fields = useMemo(() => schema.filterable_fields ?? [], [schema]);
  const fieldByKey = useMemo(() => {
    const m = new Map<string, FilterFieldDescriptor>();
    for (const f of fields) m.set(f.key, f);
    return m;
  }, [fields]);

  // null = closed; -1 = the "add" editor; >= 0 = editing that pill.
  const [editing, setEditing] = useState<number | null>(null);
  const [draft, setDraft] = useState<UiFilterCondition | null>(null);
  const [fieldSearch, setFieldSearch] = useState('');

  const close = () => {
    setEditing(null);
    setDraft(null);
    setFieldSearch('');
  };

  const commitDraft = (d: UiFilterCondition | null = draft) => {
    if (!d || !isConditionComplete(d)) return;
    const conditions = [...state.conditions];
    if (editing != null && editing >= 0) conditions[editing] = d;
    else conditions.push(d);
    onChange({ ...state, conditions });
    close();
  };

  const removeCondition = (i: number) => {
    const conditions = state.conditions.filter((_, idx) => idx !== i);
    onChange({ ...state, conditions });
    if (editing === i) close();
  };

  const pickField = (f: FilterFieldDescriptor) => {
    const operator = defaultOperatorFor(f.type);
    setDraft({ field: f.key, operator, value: undefined });
  };

  const setDraftOperator = (op: string) => {
    if (!draft) return;
    // Changing the operator changes the value SHAPE — carrying a scalar into
    // `between` (or a pair into `eq`) would render garbage, so the value resets
    // whenever the shape differs.
    const next: UiFilterCondition = { ...draft, operator: op };
    if (operatorValueKind(op) !== operatorValueKind(draft.operator)) next.value = undefined;
    // No-value operators are complete the moment they're chosen.
    if (operatorValueKind(op) === 'none') {
      commitDraft(next);
      return;
    }
    setDraft(next);
  };

  const searched = fieldSearch.trim().toLowerCase();
  const shownFields = searched
    ? fields.filter((f) => f.label.toLowerCase().includes(searched) || f.key.toLowerCase().includes(searched))
    : fields;

  const draftField = draft ? fieldByKey.get(draft.field) : undefined;

  // The shared editor body: operator menu + value input + Apply. Rendered for
  // both the add flow (after a field is picked) and pill edits.
  const editor = draft && draftField && (
    <div
      className="flex w-72 flex-col gap-2.5"
      onKeyDown={(e) => {
        if (e.key === 'Enter' && !(e.target instanceof HTMLTextAreaElement)) {
          e.preventDefault();
          commitDraft();
        }
      }}
    >
      <div className="text-xs font-medium uppercase tracking-wider text-muted-foreground">{draftField.label}</div>
      <Select
        value={draft.operator}
        onChange={(e) => setDraftOperator(e.target.value)}
        aria-label="Operator"
      >
        {operatorsFor(draftField, schema.filter_operators).map((op) => (
          <option key={op} value={op}>{operatorLabel(draftField.type, op)}</option>
        ))}
      </Select>
      <FilterValueInput
        field={draftField}
        operator={draft.operator}
        value={draft.value}
        onChange={(v) => setDraft((d) => (d ? { ...d, value: v } : d))}
        onCommit={(v) => commitDraft({ ...draft, value: v })}
        sources={sources}
      />
      {operatorValueKind(draft.operator) !== 'none' && (
        <div className="flex justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={close}>Cancel</Button>
          <Button size="sm" disabled={!isConditionComplete(draft)} onClick={() => commitDraft()}>
            Apply
          </Button>
        </div>
      )}
    </div>
  );

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {/* Opaque advanced filter (a nested AST from a saved view): one honest,
          un-editable pill rather than silently dropping conditions the flat
          bar can't render. */}
      {advancedActive && (
        <span className="inline-flex items-center gap-1 rounded-full border border-primary/40 bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">
          <ListFilter aria-hidden className="h-3 w-3" />
          Advanced filter
          {onClearAdvanced && (
            <button
              type="button"
              onClick={onClearAdvanced}
              aria-label="Clear advanced filter"
              className="ml-0.5 rounded-full p-0.5 hover:bg-primary/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <X aria-hidden className="h-3 w-3" />
            </button>
          )}
        </span>
      )}

      {state.conditions.map((c, i) => {
        const f = fieldByKey.get(c.field);
        return (
          <PopoverAnchor key={`${c.field}-${i}`}>
            <span className="inline-flex items-center gap-1 rounded-full border border-border bg-muted/40 py-1 pl-2.5 pr-1 text-xs font-medium text-foreground">
              <button
                type="button"
                onClick={() => {
                  setEditing(i);
                  setDraft({ ...c });
                }}
                className="inline-flex items-center gap-1 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                title="Edit filter"
              >
                {conditionSummary(f, c, resolveValue)}
                <Pencil aria-hidden className="h-3 w-3 opacity-40" />
              </button>
              <button
                type="button"
                onClick={() => removeCondition(i)}
                aria-label={`Remove filter on ${f?.label ?? c.field}`}
                className="rounded-full p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <X aria-hidden className="h-3 w-3" />
              </button>
            </span>
            <Popover open={editing === i} onClose={close} className="p-3">
              {editor}
            </Popover>
          </PopoverAnchor>
        );
      })}

      {state.conditions.length >= 2 && (
        <div className="inline-flex items-center rounded-full border border-input bg-background p-0.5 text-xs">
          {(['all', 'any'] as const).map((m) => (
            <button
              key={m}
              type="button"
              onClick={() => onChange({ ...state, match: m })}
              aria-pressed={state.match === m}
              className={`rounded-full px-2 py-0.5 font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                state.match === m ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {m === 'all' ? 'Match all' : 'Match any'}
            </button>
          ))}
        </div>
      )}

      {fields.length > 0 && (
        <PopoverAnchor>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              if (editing === -1) close();
              else {
                setEditing(-1);
                setDraft(null);
                setFieldSearch('');
              }
            }}
            className="text-muted-foreground"
          >
            <Plus aria-hidden /> Filter
          </Button>
          <Popover open={editing === -1} onClose={close} className="p-3">
            {draft && draftField ? (
              editor
            ) : (
              <div className="flex w-60 flex-col gap-2">
                <input
                  type="text"
                  value={fieldSearch}
                  onChange={(e) => setFieldSearch(e.target.value)}
                  placeholder="Filter by…"
                  aria-label="Search fields"
                  // A popover opened by an explicit click is the one place
                  // autofocus is expected (this is NOT a Modal — U7's
                  // never-autofocus rule is about the focus-trap primitive).
                  autoFocus
                  className="h-8 rounded-lg border border-input bg-background px-2.5 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
                <div className="flex max-h-64 flex-col overflow-y-auto">
                  {shownFields.map((f) => (
                    <button
                      key={f.key}
                      type="button"
                      onClick={() => pickField(f)}
                      className="rounded px-2 py-1.5 text-left text-sm hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    >
                      {f.label}
                      <span className="ml-1.5 text-xs text-muted-foreground">{f.type}</span>
                    </button>
                  ))}
                  {shownFields.length === 0 && (
                    <span className="px-2 py-1.5 text-sm text-muted-foreground">No matching fields</span>
                  )}
                </div>
              </div>
            )}
          </Popover>
        </PopoverAnchor>
      )}
    </div>
  );
}
