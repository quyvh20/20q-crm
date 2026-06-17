import { useState, useEffect } from 'react';
import {
  createObjectRecordUnified,
  updateObjectRecordUnified,
  listObjectRecordsUnified,
  type ObjectSchema,
  type UniformRecord,
} from '../../lib/api';
import { FieldInput, type RelationOption } from './fieldHelpers';

interface ObjectFormProps {
  schema: ObjectSchema;
  /** Existing record when editing; omit/null to create. */
  record?: UniformRecord | null;
  onSaved: (rec: UniformRecord) => void;
  onCancel: () => void;
}

// ObjectForm is the single create/edit form for every object. It is driven
// entirely by the schema descriptor, so a Deal and a custom "Project" are edited
// through the exact same component — that is the P3 "one ObjectForm" deliverable.
export default function ObjectForm({ schema, record, onSaved, onCancel }: ObjectFormProps) {
  const [formData, setFormData] = useState<Record<string, unknown>>(() => ({ ...(record?.fields ?? {}) }));
  const [relationOptions, setRelationOptions] = useState<Record<string, RelationOption[]>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // Load pick-from options for each resolvable relation field (one fetch each).
  useEffect(() => {
    let cancelled = false;
    const relationFields = schema.fields.filter((f) => f.type === 'relation' && f.target_slug);
    Promise.all(
      relationFields.map(async (f) => {
        try {
          const page = await listObjectRecordsUnified(f.target_slug!, { limit: 100 });
          return [f.key, page.records.map((r) => ({ id: r.id, label: r.display || r.id }))] as const;
        } catch {
          return [f.key, [] as RelationOption[]] as const;
        }
      }),
    ).then((entries) => {
      if (cancelled) return;
      const map: Record<string, RelationOption[]> = {};
      for (const [key, opts] of entries) map[key] = opts;
      setRelationOptions(map);
    });
    return () => {
      cancelled = true;
    };
  }, [schema]);

  const setField = (key: string, val: unknown) => setFormData((prev) => ({ ...prev, [key]: val }));

  const handleSubmit = async () => {
    setError('');
    setSaving(true);
    try {
      const saved = record
        ? await updateObjectRecordUnified(schema.slug, record.id, formData)
        : await createObjectRecordUnified(schema.slug, formData);
      onSaved(saved);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>
          {record ? `Edit ${schema.label}` : `New ${schema.label}`}
        </h3>
        <button onClick={onCancel} aria-label="Close" style={{ background: 'none', border: 'none', fontSize: 20, cursor: 'pointer', color: '#64748b' }}>×</button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
        {error && (
          <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 16, fontSize: 13 }}>{error}</div>
        )}

        {schema.fields.map((field) => (
          <div key={field.key} style={{ marginBottom: 16 }}>
            <label style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>
              {field.label}
              {field.required && <span style={{ color: '#ef4444' }}> *</span>}
            </label>
            <FieldInput
              field={field}
              value={formData[field.key] ?? ''}
              onChange={(val) => setField(field.key, val)}
              relationOptions={field.type === 'relation' ? relationOptions[field.key] : undefined}
            />
          </div>
        ))}
      </div>

      <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', gap: 8 }}>
        <button onClick={onCancel} style={{ flex: 1, padding: '10px', background: '#f1f5f9', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Cancel</button>
        <button onClick={handleSubmit} disabled={saving} style={{ flex: 1, padding: '10px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600 }}>
          {saving ? 'Saving...' : record ? `Update ${schema.label}` : `Create ${schema.label}`}
        </button>
      </div>
    </div>
  );
}
