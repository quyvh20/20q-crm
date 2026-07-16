import { useState, useEffect, useRef } from 'react';
import { X } from 'lucide-react';
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
import { Button, Label } from '@/components/ui';

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
    <div className={`flex flex-col ${inline ? '' : 'h-full min-h-0'}`}>
      <div className="flex items-center justify-between border-b border-border px-6 py-4">
        <h3 className="text-base font-semibold text-foreground">
          {record ? `Edit ${schema.label}` : `New ${schema.label}`}
        </h3>
        <button
          onClick={onCancel}
          aria-label="Close"
          className="-mr-1.5 shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <X className="h-[18px] w-[18px]" />
        </button>
      </div>

      <div className={`flex-1 p-6 ${inline ? '' : 'overflow-y-auto'}`}>
        {error && (
          <div className="mb-4 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
        )}

        {/* Owner (U6) is not a registry field — it never appears in schema.fields —
            so it gets its own control, above the field loop, on every object that
            has one. It still travels inside the fields map on save. */}
        {schema.has_owner && (
          <div className="mb-4">
            <Label htmlFor="object-form-owner" className="mb-1">
              Owner
            </Label>
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
          <div key={field.key} className="mb-4">
            <Label className="mb-1">
              {field.label}
              {field.required && <span className="text-destructive"> *</span>}
            </Label>
            <FieldInput
              field={field}
              value={formData[field.key] ?? ''}
              onChange={(val) => setField(field.key, val)}
              relationOptions={field.type === 'relation' ? relationOptions[field.key] : undefined}
            />
          </div>
        ))}
      </div>

      <div className="flex gap-2 border-t border-border bg-muted/30 px-6 py-4">
        <Button variant="outline" onClick={onCancel} className="flex-1">
          Cancel
        </Button>
        <Button id="object-form-submit" onClick={handleSubmit} disabled={saving} className="flex-1">
          {saving ? 'Saving...' : record ? `Update ${schema.label}` : `Create ${schema.label}`}
        </Button>
      </div>
    </div>
  );
}
