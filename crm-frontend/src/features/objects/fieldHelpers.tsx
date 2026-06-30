import { useState } from 'react';
import type { ObjectFieldDescriptor } from '../../lib/api';

export interface RelationOption {
  id: string;
  label: string;
}

const inputStyle = {
  width: '100%' as const,
  padding: '8px 10px',
  border: '1px solid #d1d5db',
  borderRadius: 6,
  fontSize: 14,
  boxSizing: 'border-box' as const,
};

// formatFieldValue renders a record's value for read-only display (list cells,
// detail view). Relations show their resolved label when one is supplied, else
// the raw id — system relations are resolved lazily by the caller.
export function formatFieldValue(
  field: ObjectFieldDescriptor,
  value: unknown,
  relationLabel?: string,
) {
  if (value === null || value === undefined || value === '') {
    return <span style={{ color: '#94a3b8' }}>—</span>;
  }
  switch (field.type) {
    case 'boolean':
      return value ? '✅' : '❌';
    case 'url':
      return (
        <a href={String(value)} target="_blank" rel="noreferrer" style={{ color: '#3b82f6' }}>
          {String(value).replace(/^https?:\/\//, '').slice(0, 30)}
        </a>
      );
    case 'date':
      return new Date(String(value)).toLocaleDateString();
    case 'relation':
      return relationLabel || String(value);
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
        <input
          type="number"
          value={value === '' || value === null || value === undefined ? '' : Number(value)}
          onChange={(e) => onChange(e.target.value === '' ? '' : Number(e.target.value))}
          style={inputStyle}
        />
      );
    case 'date':
      return (
        <input
          type="date"
          value={value ? String(value).slice(0, 10) : ''}
          onChange={(e) => onChange(e.target.value)}
          style={inputStyle}
        />
      );
    case 'url':
      return (
        <input
          type="url"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          placeholder="https://..."
          style={inputStyle}
        />
      );
    case 'boolean':
      return (
        <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
          <input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} /> Yes
        </label>
      );
    case 'select':
      return (
        <select value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
          <option value="">— Select —</option>
          {(field.options || []).map((opt) => (
            <option key={opt} value={opt}>{opt}</option>
          ))}
        </select>
      );
    case 'relation':
      // A registered target gives us a searchable picker; an unresolved relation
      // (e.g. a deal's stage, not yet a registered object) falls back to a
      // raw-id input so the field is still editable.
      if (relationOptions) {
        return <RelationPicker value={value} onChange={onChange} options={relationOptions} />;
      }
      return (
        <input
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          placeholder="Related record id"
          style={inputStyle}
        />
      );
    case 'text':
    default:
      return (
        <input
          type="text"
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
          placeholder={`Enter ${field.label.toLowerCase()}`}
          style={inputStyle}
        />
      );
  }
}

// RelationPicker is a searchable single-select for relation fields: type to
// filter the target object's records by label, click to choose. It replaces the
// bare <select>, which doesn't scale past a handful of records. The selected
// label shows when not actively searching; clearing resets the relation.
function RelationPicker({
  value,
  onChange,
  options,
}: {
  value: unknown;
  onChange: (val: unknown) => void;
  options: RelationOption[];
}) {
  const [query, setQuery] = useState('');
  const [open, setOpen] = useState(false);
  const selected = options.find((o) => o.id === String(value ?? ''));
  const q = query.trim().toLowerCase();
  const filtered = (q ? options.filter((o) => o.label.toLowerCase().includes(q)) : options).slice(0, 50);

  return (
    <div style={{ position: 'relative' }}>
      <input
        type="text"
        // While the menu is open the input is the search box; closed, it shows the
        // current selection's label.
        value={open ? query : selected?.label ?? ''}
        onFocus={() => { setQuery(''); setOpen(true); }}
        onChange={(e) => { setQuery(e.target.value); setOpen(true); }}
        // Delay close so a click on an option (mousedown) registers first.
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        placeholder={selected ? selected.label : '— None — (type to search)'}
        style={inputStyle}
      />
      {open && (
        <div
          style={{
            position: 'absolute', top: '100%', left: 0, right: 0, zIndex: 30,
            background: '#fff', border: '1px solid #d1d5db', borderRadius: 6,
            marginTop: 2, maxHeight: 220, overflowY: 'auto', boxShadow: '0 8px 24px rgba(0,0,0,0.12)',
          }}
        >
          <div
            onMouseDown={() => { onChange(''); setOpen(false); }}
            style={{ padding: '8px 10px', fontSize: 13, color: '#94a3b8', cursor: 'pointer' }}
          >
            — None —
          </div>
          {filtered.map((o) => (
            <div
              key={o.id}
              onMouseDown={() => { onChange(o.id); setOpen(false); }}
              style={{
                padding: '8px 10px', fontSize: 14, cursor: 'pointer',
                background: o.id === String(value ?? '') ? '#eff6ff' : '#fff',
              }}
            >
              {o.label}
            </div>
          ))}
          {filtered.length === 0 && (
            <div style={{ padding: '8px 10px', fontSize: 13, color: '#94a3b8' }}>No matches</div>
          )}
        </div>
      )}
    </div>
  );
}
