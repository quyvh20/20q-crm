import { useState, useEffect, useRef } from 'react';
import { X } from 'lucide-react';
import {
  createObjectRecordUnified,
  updateObjectRecordUnified,
  listObjectRecordsUnified,
  getStages,
  getPipelines,
  type ObjectSchema,
  type PipelineStage,
  type UniformRecord,
} from '../../lib/api';
import { FieldInput, type RelationOption } from './fieldHelpers';
import OwnerPicker from '../../components/records/OwnerPicker';
import { Button, Label } from '@/components/ui';

interface ObjectFormProps {
  schema: ObjectSchema;
  /** Existing record when editing; omit/null to create. */
  record?: UniformRecord | null;
  /**
   * The board a NEW record is being created on (R9.3), when the caller knows it —
   * e.g. the pipeline whose kanban is on screen. Ignored when editing: an existing
   * deal's own board always wins, because the backend refuses a stage from any
   * other one.
   */
  pipelineId?: string;
  inline?: boolean;
  onSaved: (rec: UniformRecord) => void;
  onCancel: () => void;
}

// Which board the `stage` picker must be scoped to, gathered from the props once
// (see loadStageOptions).
interface StageScope {
  editing: boolean;
  /** The record's own board, if the payload happens to carry one. */
  recordPipelineId: string;
  /** The stage the record already sits on; names its board when the above is absent. */
  recordStageId: string;
  /** The board the caller is creating on, when it knows one. */
  callerPipelineId: string;
}

// Field values are `unknown`; a relation id is only usable when it is a non-empty
// string (the deal projector writes '' for an unset one).
const fieldId = (v: unknown) => (typeof v === 'string' ? v : '');

// Resolve the option list for the `stage` pseudo-relation. R9.3 split stages
// across pipelines, so the org-wide ladder stopped being a valid menu: the
// backend refuses a stage belonging to any board but the deal's own with a 400
// ("that stage belongs to a different pipeline"), which makes every out-of-board
// option one the user cannot actually save. Scope to a single board — the
// record's own when editing, the caller's or the org default otherwise — and
// only widen back to every stage when none of those resolves, because a picker
// offering too much still beats an empty one.
async function loadStageOptions(scope: StageScope): Promise<PipelineStage[]> {
  if (scope.recordPipelineId) return getStages(scope.recordPipelineId);

  if (scope.editing) {
    // The uniform record projects `stage` but not `pipeline_id`, so for an
    // existing deal the stage it is already on is what names its board. Filtering
    // the org-wide list client-side rather than issuing a second scoped request
    // also guarantees the deal's CURRENT stage stays in the options — scoping by
    // an id we inferred wrong would drop it and silently blank the picker.
    const all = await getStages();
    const current = all.find((s) => s.id === scope.recordStageId);
    if (current?.pipeline_id) return all.filter((s) => s.pipeline_id === current.pipeline_id);
    // A stage that predates the pipeline backfill names no board. Nothing can be
    // inferred from it, and scoping by a guess would drop the very value the
    // field is already set to, so show everything.
    if (scope.recordStageId) return all;
    // An UNSTAGED record falls through to the default board instead: both the
    // create path and the R9.3 backfill stamp a deal with no stage onto the org
    // default, so that — not "every board" — is the ladder it can move within.
  } else if (scope.callerPipelineId) {
    return getStages(scope.callerPipelineId);
  }

  let defaultPipelineId = '';
  try {
    // getPipelines throws on a non-ok response — an org that cannot list its
    // boards still gets the unscoped picker below, never an empty one. Same if
    // no pipeline is flagged default, which should be impossible but is not
    // worth blanking the field over.
    defaultPipelineId = (await getPipelines()).find((p) => p.is_default)?.id ?? '';
  } catch {
    /* fall through to the unscoped list */
  }
  return defaultPipelineId ? getStages(defaultPipelineId) : getStages();
}

// ObjectForm is the single create/edit form for every object. It is driven
// entirely by the schema descriptor, so a Deal and a custom "Project" are edited
// through the exact same component — that is the P3 "one ObjectForm" deliverable.
export default function ObjectForm({ schema, record, pipelineId, inline, onSaved, onCancel }: ObjectFormProps) {
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
  // Captured once, like initialData: a parent re-render handing us an equal-but-new
  // `record` object must not restart the stage load, and the board a record is on
  // cannot change from inside this form anyway.
  const stageScope = useRef<StageScope>({
    editing: !!record,
    recordPipelineId: fieldId(record?.fields.pipeline_id),
    recordStageId: fieldId(record?.fields.stage),
    callerPipelineId: pipelineId ?? '',
  }).current;
  const [relationOptions, setRelationOptions] = useState<Record<string, RelationOption[]>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // Load pick-from options for each resolvable relation field (one fetch each).
  // A "stage" relation has no registry target (pipeline_stages isn't a registered
  // object), so it is resolved specially from the pipeline stages — scoped to one
  // board, see loadStageOptions.
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
            loadStageOptions(stageScope)
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
  }, [schema, stageScope]);

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
        {schema.fields.filter((field) => field.type !== 'mirror').map((field) => {
          // The visible <Label> used to be a bare SIBLING of the control, with no
          // htmlFor and no wrapping — so there was neither an explicit nor an
          // implicit association, and every schema-driven input on this form was
          // announced by a screen reader with no name at all ("edit text, blank").
          // The field key is unique within an object's schema, so it makes a
          // stable per-field id; threading it into FieldInput restores the
          // association for every field type at once.
          const inputId = `object-field-${field.key}`;
          return (
            <div key={field.key} className="mb-4">
              <Label htmlFor={inputId} className="mb-1">
                {field.label}
                {field.required && <span className="text-destructive"> *</span>}
              </Label>
              <FieldInput
                id={inputId}
                field={field}
                value={formData[field.key] ?? ''}
                onChange={(val) => setField(field.key, val)}
                relationOptions={field.type === 'relation' ? relationOptions[field.key] : undefined}
              />
            </div>
          );
        })}
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
