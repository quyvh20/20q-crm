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
      // A registered target gives us a proper picker; an unresolved relation
      // (e.g. a deal's stage, not yet a registered object) falls back to a
      // raw-id input so the field is still editable.
      if (relationOptions) {
        return (
          <select value={String(value ?? '')} onChange={(e) => onChange(e.target.value)} style={inputStyle}>
            <option value="">— None —</option>
            {relationOptions.map((opt) => (
              <option key={opt.id} value={opt.id}>{opt.label}</option>
            ))}
          </select>
        );
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
