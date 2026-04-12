import { useQuery } from '@tanstack/react-query';
import { getFieldDefs, type CustomFieldDef } from '../../lib/api';

interface DynamicCustomFieldsProps {
  entityType: 'contact' | 'company' | 'deal';
  values: Record<string, unknown>;
  onChange: (values: Record<string, unknown>) => void;
  disabled?: boolean;
}

/**
 * Renders custom fields dynamically based on the org's field definitions.
 * Used inside ContactForm, DealFormModal, etc.
 */
export default function DynamicCustomFields({
  entityType,
  values,
  onChange,
  disabled = false,
}: DynamicCustomFieldsProps) {
  const { data: fieldDefs = [], isLoading } = useQuery({
    queryKey: ['field-defs', entityType],
    queryFn: () => getFieldDefs(entityType),
    staleTime: 60_000,
  });

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 py-3 text-sm text-muted-foreground">
        <span className="animate-spin h-3.5 w-3.5 border-2 border-blue-500 border-t-transparent rounded-full" />
        Loading custom fields…
      </div>
    );
  }

  if (fieldDefs.length === 0) return null;

  const handleChange = (key: string, val: unknown) => {
    onChange({ ...values, [key]: val });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2 pt-2">
        <div className="h-px flex-1 bg-border" />
        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          Custom Fields
        </span>
        <div className="h-px flex-1 bg-border" />
      </div>

      {fieldDefs.map((def) => (
        <FieldInput
          key={def.key}
          def={def}
          value={values[def.key]}
          onChange={(v) => handleChange(def.key, v)}
          disabled={disabled}
        />
      ))}
    </div>
  );
}

// ────────────────────────────────────────────────────────────────
// Per-type field input renderer
// ────────────────────────────────────────────────────────────────

function FieldInput({
  def,
  value,
  onChange,
  disabled,
}: {
  def: CustomFieldDef;
  value: unknown;
  onChange: (v: unknown) => void;
  disabled: boolean;
}) {
  const inputClass =
    'w-full rounded-lg border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all disabled:opacity-50';

  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        {def.label}
        {def.required && <span className="text-red-400 ml-0.5">*</span>}
      </label>

      {def.type === 'text' && (
        <input
          type="text"
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className={inputClass}
          placeholder={`Enter ${def.label.toLowerCase()}`}
        />
      )}

      {def.type === 'url' && (
        <input
          type="url"
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className={inputClass}
          placeholder="https://example.com"
        />
      )}

      {def.type === 'number' && (
        <input
          type="number"
          value={value !== undefined && value !== null ? String(value) : ''}
          onChange={(e) => {
            const v = e.target.value;
            onChange(v === '' ? null : parseFloat(v));
          }}
          disabled={disabled}
          className={inputClass}
          placeholder="0"
        />
      )}

      {def.type === 'date' && (
        <input
          type="date"
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          className={inputClass}
        />
      )}

      {def.type === 'boolean' && (
        <label className="flex items-center gap-2 cursor-pointer">
          <input
            type="checkbox"
            checked={!!value}
            onChange={(e) => onChange(e.target.checked)}
            disabled={disabled}
            className="h-4 w-4 rounded border-gray-500 text-blue-600 focus:ring-blue-500/40"
          />
          <span className="text-sm text-muted-foreground">
            {value ? 'Yes' : 'No'}
          </span>
        </label>
      )}

      {def.type === 'select' && (
        <select
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value || null)}
          disabled={disabled}
          className={inputClass}
        >
          <option value="">Select {def.label.toLowerCase()}…</option>
          {def.options?.map((opt) => (
            <option key={opt} value={opt}>
              {opt}
            </option>
          ))}
        </select>
      )}
    </div>
  );
}
