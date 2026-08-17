import { Input, Select } from '@/components/ui';
import type { FilterFieldDescriptor } from '../../../lib/api';
import { RelationPicker, type RelationOption } from '../fieldHelpers';
import { operatorValueKind } from './filterModel';

// ValueSources abstracts where a relation-ish field's choices come from: real
// target objects get a server-searching RelationPicker; the special sources
// (user/stage/pipeline — not registry objects) get their preloaded lists.
export interface ValueSources {
  optionsFor: (field: FilterFieldDescriptor) => RelationOption[];
  /** Real object slug for server-side picker search; undefined for user/stage/pipeline. */
  targetSlugFor: (field: FilterFieldDescriptor) => string | undefined;
}

// FilterValueInput renders the value editor for one condition, switched on the
// operator's value shape first and the field type second.
//
// onChange updates the draft; onCommit additionally applies it — DISCRETE
// controls (selects, pickers, dates, checkboxes) commit on choice because the
// choice is the whole gesture, while free-typing controls (text, numbers) only
// draft, and the editor's Apply/Enter commits — committing per keystroke would
// fire a server query per letter.
export default function FilterValueInput({
  field,
  operator,
  value,
  onChange,
  onCommit,
  sources,
}: {
  field: FilterFieldDescriptor;
  operator: string;
  value: unknown;
  onChange: (v: unknown) => void;
  onCommit: (v: unknown) => void;
  sources: ValueSources;
}) {
  const kind = operatorValueKind(operator);
  if (kind === 'none') return null;

  if (kind === 'days') {
    return (
      <div className="flex items-center gap-2">
        <Input
          type="number"
          min={1}
          max={3650}
          value={value == null ? '' : String(value)}
          onChange={(e) => onChange(e.target.value)}
          aria-label="Number of days"
          className="w-24"
        />
        <span className="text-sm text-muted-foreground">days</span>
      </div>
    );
  }

  if (kind === 'pair') {
    const pair = Array.isArray(value) ? (value as unknown[]) : ['', ''];
    const inputType = field.type === 'date' ? 'date' : 'number';
    const setHalf = (i: 0 | 1, v: string) => {
      const next: unknown[] = [pair[0] ?? '', pair[1] ?? ''];
      next[i] = v;
      // Pairs only DRAFT — never auto-commit, even for dates. When editing an
      // existing complete range, changing one end leaves both ends non-empty,
      // and committing there would close the editor after the first half of a
      // two-part edit (typically minting an inverted range).
      onChange(next);
    };
    return (
      <div className="flex items-center gap-2">
        <Input
          type={inputType}
          value={String(pair[0] ?? '')}
          onChange={(e) => setHalf(0, e.target.value)}
          aria-label="From"
          className="w-36"
        />
        <span className="text-sm text-muted-foreground">and</span>
        <Input
          type={inputType}
          value={String(pair[1] ?? '')}
          onChange={(e) => setHalf(1, e.target.value)}
          aria-label="To"
          className="w-36"
        />
      </div>
    );
  }

  if (kind === 'list') {
    // Multi-select over the field's declared options (fields with options only
    // — operatorsFor hides in/not_in everywhere else until richer pickers
    // exist). Toggles only DRAFT: committing per checkbox would close the
    // editor after the first of several picks, and unchecking the last option
    // would silently no-op (an empty list is not a committable condition).
    const selected = new Set(Array.isArray(value) ? (value as unknown[]).map(String) : []);
    const toggle = (opt: string) => {
      const next = new Set(selected);
      if (next.has(opt)) next.delete(opt);
      else next.add(opt);
      onChange([...next]);
    };
    return (
      <div className="flex max-h-44 flex-col gap-1 overflow-y-auto rounded-lg border border-border p-2">
        {(field.options ?? []).map((opt) => (
          <label key={opt} className="flex cursor-pointer items-center gap-2 rounded px-1.5 py-1 text-sm hover:bg-accent">
            <input
              type="checkbox"
              className="h-4 w-4 cursor-pointer rounded border-input accent-primary"
              checked={selected.has(opt)}
              onChange={() => toggle(opt)}
            />
            {opt}
          </label>
        ))}
        {(field.options ?? []).length === 0 && (
          <span className="px-1.5 py-1 text-sm text-muted-foreground">No options defined</span>
        )}
      </div>
    );
  }

  // kind === 'single'
  switch (field.type) {
    case 'relation':
      return (
        <RelationPicker
          value={value ?? ''}
          onChange={(v) => onCommit(v ?? '')}
          options={sources.optionsFor(field)}
          targetSlug={sources.targetSlugFor(field)}
        />
      );
    case 'select':
      return (
        <Select
          value={String(value ?? '')}
          onChange={(e) => onCommit(e.target.value)}
          aria-label={`${field.label} value`}
        >
          <option value="">Choose…</option>
          {(field.options ?? []).map((opt) => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </Select>
      );
    case 'boolean':
      return (
        <Select
          value={String(value ?? '')}
          onChange={(e) => onCommit(e.target.value)}
          aria-label={`${field.label} value`}
        >
          <option value="">Choose…</option>
          <option value="true">True</option>
          <option value="false">False</option>
        </Select>
      );
    case 'date':
      return (
        <Input
          type="date"
          value={String(value ?? '')}
          onChange={(e) => onCommit(e.target.value)}
          aria-label={`${field.label} value`}
          className="w-40"
        />
      );
    case 'number':
      return (
        <Input
          type="number"
          value={value == null ? '' : String(value)}
          onChange={(e) => onChange(e.target.value)}
          aria-label={`${field.label} value`}
          className="w-32"
        />
      );
    default:
      return (
        <Input
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          aria-label={`${field.label} value`}
          placeholder="Value…"
        />
      );
  }
}
