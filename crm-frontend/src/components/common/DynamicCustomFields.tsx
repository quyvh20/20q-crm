import { useQuery } from '@tanstack/react-query';
import { getFieldDefs, type CustomFieldDef } from '../../lib/api';
import { Input, Label, Select, Spinner } from '@/components/ui';

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
    return <Spinner size="sm" label="Loading custom fields…" className="py-3" />;
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
  return (
    <div className="space-y-1.5">
      <Label>
        {def.label}
        {def.required && <span className="ml-0.5 text-destructive">*</span>}
      </Label>

      {def.type === 'text' && (
        <Input
          type="text"
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          placeholder={`Enter ${def.label.toLowerCase()}`}
        />
      )}

      {def.type === 'url' && (
        <Input
          type="url"
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
          placeholder="https://example.com"
        />
      )}

      {def.type === 'number' && (
        <Input
          type="number"
          value={value !== undefined && value !== null ? String(value) : ''}
          onChange={(e) => {
            const v = e.target.value;
            onChange(v === '' ? null : parseFloat(v));
          }}
          disabled={disabled}
          placeholder="0"
        />
      )}

      {def.type === 'date' && (
        <Input
          type="date"
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value)}
          disabled={disabled}
        />
      )}

      {def.type === 'boolean' && (
        <label className="flex cursor-pointer items-center gap-2">
          <input
            type="checkbox"
            checked={!!value}
            onChange={(e) => onChange(e.target.checked)}
            disabled={disabled}
            className="h-4 w-4 rounded border-input accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
          <span className="text-sm text-muted-foreground">
            {value ? 'Yes' : 'No'}
          </span>
        </label>
      )}

      {def.type === 'select' && (
        <Select
          value={(value as string) ?? ''}
          onChange={(e) => onChange(e.target.value || null)}
          disabled={disabled}
        >
          <option value="">Select {def.label.toLowerCase()}…</option>
          {def.options?.map((opt) => (
            <option key={opt} value={opt}>
              {opt}
            </option>
          ))}
        </Select>
      )}
    </div>
  );
}
