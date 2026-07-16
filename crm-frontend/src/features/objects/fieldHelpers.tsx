import { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { Check, X } from 'lucide-react';
import {
  listObjectRecordsUnified,
  getObjectRecordUnified,
  type ObjectFieldDescriptor,
} from '../../lib/api';
import { recordPath } from './recordRoutes';
import { Input, Select } from '@/components/ui';

export interface RelationOption {
  id: string;
  label: string;
}

// How many records the relation picker shows: the 10 newest by default, or up to
// 10 case-insensitive matches while searching.
const PICKER_LIMIT = 10;

// formatFieldValue renders a record's value for read-only display (list cells,
// detail view). Relations show their resolved label when one is supplied, else
// the raw id — system relations are resolved lazily by the caller.
export function formatFieldValue(
  field: ObjectFieldDescriptor,
  value: unknown,
  relationLabel?: string,
) {
  if (value === null || value === undefined || value === '') {
    return <span className="text-muted-foreground">—</span>;
  }
  switch (field.type) {
    case 'boolean':
      return value ? (
        <Check aria-label="Yes" className="h-4 w-4 text-primary" />
      ) : (
        <X aria-label="No" className="h-4 w-4 text-muted-foreground" />
      );
    case 'url':
      return (
        <a href={String(value)} target="_blank" rel="noreferrer" className="text-primary hover:underline">
          {String(value).replace(/^https?:\/\//, '').slice(0, 30)}
        </a>
      );
    case 'date':
      return new Date(String(value)).toLocaleDateString();
    case 'relation': {
      const label = relationLabel || String(value);
      // A resolvable target makes the value a link to that record. The pseudo
      // "stage" relation has no target_slug, so it stays plain text. stopPropagation
      // keeps a clickable list row from also firing when the link is clicked.
      if (field.target_slug) {
        return (
          <Link
            to={recordPath(field.target_slug, String(value))}
            onClick={(e) => e.stopPropagation()}
            className="text-primary no-underline hover:underline"
          >
            {label}
          </Link>
        );
      }
      return label;
    }
    default:
      return String(value);
  }
}

interface FieldInputProps {
  field: ObjectFieldDescriptor;
  value: unknown;
  onChange: (val: unknown) => void;
  relationOptions?: RelationOption[];
}

// FieldInput is the single schema-driven editor for one field, used by ObjectForm
// for every object. The same component renders a Deal's "value" and a custom
// object's "budget" — there is no per-object form code.
export function FieldInput({ field, value, onChange, relationOptions }: FieldInputProps) {
  switch (field.type) {
    case 'number':
      return (
        <Input
          type="number"
          value={value === '' || value === null || value === undefined ? '' : Number(value)}
          onChange={(e) => onChange(e.target.value === '' ? '' : Number(e.target.value))}
        />
      );
    case 'date':
      return (
        <Input
          type="date"
          value={value ? String(value).slice(0, 10) : ''}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case 'url':
      return (
        <Input
          type="url"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          placeholder="https://..."
        />
      );
    case 'boolean':
      return (
        <label className="flex cursor-pointer items-center gap-2 text-sm text-foreground">
          <input
            type="checkbox"
            checked={!!value}
            onChange={(e) => onChange(e.target.checked)}
            className="h-4 w-4 rounded border-input accent-primary"
          />{' '}
          Yes
        </label>
      );
    case 'select':
      return (
        <Select value={String(value ?? '')} onChange={(e) => onChange(e.target.value)}>
          <option value="">— Select —</option>
          {(field.options || []).map((opt) => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </Select>
      );
    case 'relation':
      // Always use the searchable picker for a relation with a target object — it
      // searches that object server-side, so it works even when the form hasn't
      // preloaded options (or the preload failed). A relation reached without a
      // target (e.g. a deal's stage, or a misconfigured field) uses preloaded
      // options when present; only a relation with neither a target nor options
      // has nothing to pick from and falls back to a plain input.
      if (field.target_slug || relationOptions) {
        return (
          <RelationPicker
            value={value}
            onChange={onChange}
            options={relationOptions ?? []}
            targetSlug={field.target_slug}
          />
        );
      }
      return (
        <Input
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          placeholder="Related record id"
        />
      );
    case 'text':
    default:
      return (
        <Input
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`Enter ${field.label.toLowerCase()}`}
        />
      );
  }
}

// RelationPicker is a searchable single-select for relation fields: type to
// filter the target object's records by label, click to choose. It replaces the
// bare <select>, which doesn't scale past a handful of records.
//
// When a targetSlug is known it searches the SERVER as you type (so records beyond
// the preloaded page are reachable) and resolves the selected record's label even
// if it wasn't preloaded. Without a targetSlug it falls back to filtering the
// preloaded options client-side.
function RelationPicker({
  value,
  onChange,
  options,
  targetSlug,
}: {
  value: unknown;
  onChange: (val: unknown) => void;
  options: RelationOption[];
  targetSlug?: string;
}) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  // Server-side search results (null = not searching → show preloaded options).
  const [remote, setRemote] = useState<RelationOption[] | null>(null);
  // Label for a selected record that isn't among the preloaded options.
  const [fetchedLabel, setFetchedLabel] = useState<string | undefined>(undefined);

  const idStr = String(value ?? '');
  const preloadedLabel = options.find((o) => o.id === idStr)?.label;
  const selectedLabel = preloadedLabel ?? fetchedLabel;

  // Resolve the selected record's label when it wasn't in the preloaded page
  // (e.g. the target object has more records than were loaded up front).
  useEffect(() => {
    if (!idStr || preloadedLabel || !targetSlug) return;
    let cancelled = false;
    getObjectRecordUnified(targetSlug, idStr)
      .then((r) => { if (!cancelled) setFetchedLabel(r.display || r.id); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [idStr, preloadedLabel, targetSlug]);

  // Server search while the menu is open. On open (empty query) it loads the 10
  // newest so the dropdown is short and immediately useful; typing searches the
  // whole object (debounced). The backend search is case-insensitive.
  useEffect(() => {
    if (!open || !targetSlug) return;
    const term = query.trim();
    let cancelled = false;
    const t = setTimeout(() => {
      listObjectRecordsUnified(targetSlug, { q: term, limit: PICKER_LIMIT })
        .then((page) => { if (!cancelled) setRemote(page.records.map((r) => ({ id: r.id, label: r.display || r.id }))); })
        .catch(() => { if (!cancelled) setRemote(null); });
    }, term ? 250 : 0);
    return () => { cancelled = true; clearTimeout(t); };
  }, [query, open, targetSlug]);

  const q = query.trim().toLowerCase();
  // Server results when available (already query-narrowed), else the preloaded
  // options. A case-insensitive substring filter is applied either way so matching
  // never depends on letter case, and the list is capped at the 10 newest.
  const base = remote !== null ? remote : options;
  const shown = (q ? base.filter((o) => o.label.toLowerCase().includes(q)) : base).slice(0, PICKER_LIMIT);

  return (
    <div className="relative">
      <Input
        type="text"
        // While the menu is open the input is the search box; closed, it shows the
        // current selection's label.
        value={open ? query : selectedLabel ?? ''}
        onFocus={() => { setQuery(''); setRemote(null); setOpen(true); }}
        onChange={(e) => { setQuery(e.target.value); setOpen(true); }}
        // Delay close so a click on an option (mousedown) registers first.
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        placeholder={selectedLabel ?? '— None — (type to search)'}
      />
      {open && (
        <div className="absolute left-0 right-0 top-full z-30 mt-1 max-h-56 overflow-y-auto rounded-lg border border-border bg-popover text-popover-foreground shadow-lg">
          <div
            onMouseDown={() => { onChange(''); setOpen(false); }}
            className="cursor-pointer px-3 py-2 text-sm text-muted-foreground hover:bg-accent"
          >
            — None —
          </div>
          {shown.map((o) => (
            <div
              key={o.id}
              onMouseDown={() => { onChange(o.id); setOpen(false); }}
              className={`cursor-pointer px-3 py-2 text-sm hover:bg-accent ${
                o.id === idStr ? 'bg-primary/10 text-primary' : ''
              }`}
            >
              {o.label}
            </div>
          ))}
          {shown.length === 0 && (
            <div className="px-3 py-2 text-sm text-muted-foreground">No matches</div>
          )}
        </div>
      )}
    </div>
  );
}
