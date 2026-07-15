import { useState, useEffect, useRef } from 'react';
import {
  createObjectRecordUnified,
  updateObjectRecordUnified,
  listObjectRecordsUnified,
  getStages,
  type ObjectSchema,
  type UniformRecord,
} from '../../lib/api';
import { FieldInput, type RelationOption } from './fieldHelpers';
import OwnerPicker from '../../components/records/OwnerPicker';

interface ObjectFormProps {
  schema: ObjectSchema;
  /** Existing record when editing; omit/null to create. */
  record?: UniformRecord | null;
  inline?: boolean;
  onSaved: (rec: UniformRecord) => void;
  onCancel: () => void;
}

// ObjectForm is the single create/edit form for every object. It is driven
// entirely by the schema descriptor, so a Deal and a custom "Project" are edited
// through the exact same component — that is the P3 "one ObjectForm" deliverable.
export default function ObjectForm({ schema, record, inline, onSaved, onCancel }: ObjectFormProps) {
  const [formData, setFormData] = useState<Record<string, unknown>>(() => {
    const base: Record<string, unknown> = { ...(record?.fields ?? {}) };
    // Owner (U6) rides in the fields map, but seed it from the record's own
    // owner_user_id too so an older payload that only carries the column still
    // edits correctly. Unassigning writes null — never fall back through it.
    if (record && base.owner_user_id === undefined) base.owner_user_id = record.owner_user_id ?? '';
    return base;
  });
  // Snapshot of the loaded values, captured once. On save we send ONLY the keys the
  // user actually changed (diffed against this). Two bugs die with it: (1) a field the
  // role may see but not write (FLS "read") is no longer in the payload, so the write
  // guard stops 403-ing every save; (2) a field the user never touched is no longer
  // sent, so a concurrent editor's change to it isn't silently reverted. Untouched
  // keys keep their reference from the initial spread, so a per-key `!==` is an exact
  // "did the user change this" test — even for array/object values.
  const initialData = useRef(formData).current;
  const [relationOptions, setRelationOptions] = useState<Record<string, RelationOption[]>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // Load pick-from options for each resolvable relation field (one fetch each).
  // A "stage" relation has no registry target (pipeline_stages isn't a registered
  // object), so it is resolved specially from the pipeline stages.
  useEffect(() => {
    let cancelled = false;
    const relationFields = schema.fields.filter((f) => f.type === 'relation' && f.target_slug);
    const hasStage = schema.fields.some((f) => f.key === 'stage' && f.type === 'relation' && !f.target_slug);
    Promise.all([
      ...relationFields.map(async (f) => {
        try {
          const page = await listObjectRecordsUnified(f.target_slug!, { limit: 100 });
          return [f.key, page.records.map((r) => ({ id: r.id, label: r.display || r.id }))] as const;
        } catch {
          return [f.key, [] as RelationOption[]] as const;
        }
      }),
      ...(hasStage
        ? [
            getStages()
              .then((stages) => ['stage', stages.map((s) => ({ id: s.id, label: s.name }))] as const)
              .catch(() => ['stage', [] as RelationOption[]] as const),
          ]
        : []),
    ]).then((entries) => {
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
      let saved: UniformRecord;
      if (record) {
        // Send only the keys the user actually changed (see initialData). The backend
        // merges them over the current record, so untouched fields — and any concurrent
        // edit to them — survive; an unchanged FLS read-only field is never in the diff,
        // so it can't trip the field-write guard.
        const changed: Record<string, unknown> = {};
        for (const key of Object.keys(formData)) {
          if (formData[key] !== initialData[key]) changed[key] = formData[key];
        }
        saved = await updateObjectRecordUnified(schema.slug, record.id, changed);
      } else {
        saved = await createObjectRecordUnified(schema.slug, formData);
      }
      onSaved(saved);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: inline ? 'auto' : '100%', minHeight: 0 }}>
      <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>
          {record ? `Edit ${schema.label}` : `New ${schema.label}`}
        </h3>
        <button onClick={onCancel} aria-label="Close" style={{ background: 'none', border: 'none', fontSize: 20, cursor: 'pointer', color: '#64748b' }}>×</button>
      </div>

      <div style={{ flex: 1, overflowY: inline ? 'visible' : 'auto', padding: 24 }}>
        {error && (
          <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 16, fontSize: 13 }}>{error}</div>
        )}

        {/* Owner (U6) is not a registry field — it never appears in schema.fields —
            so it gets its own control, above the field loop, on every object that
            has one. It still travels inside the fields map on save. */}
        {schema.has_owner && (
          <div style={{ marginBottom: 16 }}>
            <label htmlFor="object-form-owner" style={{ fontSize: 13, fontWeight: 500, color: '#374151', display: 'block', marginBottom: 4 }}>
              Owner
            </label>
            <OwnerPicker
              id="object-form-owner"
              value={(formData.owner_user_id as string | null | undefined) ?? ''}
              onChange={(userId) => setField('owner_user_id', userId)}
              disabled={saving}
            />
          </div>
        )}

        {/* Mirror fields are read-only (their value is pulled from a linked record),
            so they have no editor here — they only render on the record's detail page. */}
        {schema.fields.filter((field) => field.type !== 'mirror').map((field) => (
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
        <button id="object-form-submit" onClick={handleSubmit} disabled={saving} style={{ flex: 1, padding: '10px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600 }}>
          {saving ? 'Saving...' : record ? `Update ${schema.label}` : `Create ${schema.label}`}
        </button>
      </div>
    </div>
  );
}
