import { useState, useEffect } from 'react';
import {
  type ObjectSchema,
  type UniformRecord,
  type LayoutSection,
  type ObjectFieldDescriptor,
  getObjectRecordUnified,
  getStages,
} from '../../lib/api';
import { formatFieldValue } from './fieldHelpers';
import RecordRelations from './RecordRelations';

interface ObjectDetailViewProps {
  schema: ObjectSchema;
  record: UniformRecord;
  onEdit: () => void;
  onDelete: () => void;
  onClose: () => void;
}

// FieldRow is the shared single-field renderer, used by both the flat list and
// the sectioned layout so the display is always identical.
function FieldRow({
  field,
  value,
  relationLabel,
}: {
  field: ObjectFieldDescriptor;
  value: unknown;
  relationLabel?: string;
}) {
  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase', marginBottom: 2 }}>
        {field.label}
      </div>
      <div style={{ fontSize: 14, color: '#0f172a' }}>
        {formatFieldValue(field, value, relationLabel)}
      </div>
    </div>
  );
}

// SectionPanel renders one LayoutSection with its fields in a 1- or 2-column grid.
function SectionPanel({
  section,
  schema,
  record,
  relationLabels,
}: {
  section: LayoutSection;
  schema: ObjectSchema;
  record: UniformRecord;
  relationLabels: Record<string, string>;
}) {
  const fieldMap = Object.fromEntries(schema.fields.map((f) => [f.key, f]));

  return (
    <div style={{ marginBottom: 24 }}>
      {section.label && (
        <div style={{
          fontSize: 11,
          fontWeight: 700,
          color: '#94a3b8',
          textTransform: 'uppercase',
          letterSpacing: '0.08em',
          borderBottom: '1px solid #e2e8f0',
          paddingBottom: 6,
          marginBottom: 14,
        }}>
          {section.label}
        </div>
      )}
      <div
        style={
          section.columns === 2
            ? { display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: 24 }
            : undefined
        }
      >
        {section.fields.map((slot) => {
          const field = fieldMap[slot.key];
          if (!field) return null;
          const span = section.columns === 2 && slot.width === 'full' ? { gridColumn: '1 / -1' } : undefined;
          return (
            <div key={slot.key} style={span}>
              <FieldRow
                field={field}
                value={record.fields[slot.key]}
                relationLabel={relationLabels[slot.key]}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ObjectDetailView is the read-only record panel for every object, rendered from
// the same schema as the list and form.
//
// P8 layout rendering: when schema.layout is non-empty, fields are grouped into
// LayoutSection panels (1 or 2-column). Fields absent from every section are
// collected into a trailing "Other" section so nothing is ever lost — the layout
// controls visual priority, not visibility (FLS controls that).
export default function ObjectDetailView({ schema, record, onEdit, onDelete, onClose }: ObjectDetailViewProps) {
  const [relationLabels, setRelationLabels] = useState<Record<string, string>>({});

  useEffect(() => {
    let cancelled = false;
    const relations = schema.fields.filter(
      (f) => f.type === 'relation' && f.target_slug && record.fields[f.key],
    );
    const stageField = schema.fields.find(
      (f) => f.key === 'stage' && f.type === 'relation' && !f.target_slug && record.fields[f.key],
    );
    if (relations.length === 0 && !stageField) {
      setRelationLabels({});
      return;
    }
    Promise.all([
      ...relations.map(async (f) => {
        try {
          const target = await getObjectRecordUnified(f.target_slug!, String(record.fields[f.key]));
          return [f.key, target.display] as const;
        } catch {
          return [f.key, ''] as const;
        }
      }),
      ...(stageField
        ? [
            getStages()
              .then((stages) => {
                const match = stages.find((s) => s.id === String(record.fields[stageField.key]));
                return [stageField.key, match?.name ?? ''] as const;
              })
              .catch(() => [stageField.key, ''] as const),
          ]
        : []),
    ]).then((pairs) => {
      if (cancelled) return;
      const map: Record<string, string> = {};
      for (const [k, v] of pairs) if (v) map[k] = v;
      setRelationLabels(map);
    });
    return () => {
      cancelled = true;
    };
  }, [schema, record]);

  // Determine render mode: sectioned (P8 layout) or flat (legacy / no layout).
  const sections = schema.layout && schema.layout.length > 0 ? schema.layout : null;

  // Build an "Other" section for fields absent from all layout sections so they
  // are always visible even if the admin forgot to add them.
  let otherSection: LayoutSection | null = null;
  if (sections) {
    const inLayout = new Set(sections.flatMap((s) => s.fields.map((f) => f.key)));
    const missing = schema.fields.filter((f) => !inLayout.has(f.key));
    if (missing.length > 0) {
      otherSection = {
        id: '__other__',
        label: 'Other',
        columns: 1,
        fields: missing.map((f) => ({ key: f.key })),
      };
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>{schema.icon} {record.display || 'Untitled'}</h3>
        <button onClick={onClose} aria-label="Close" style={{ background: 'none', border: 'none', fontSize: 20, cursor: 'pointer', color: '#64748b' }}>×</button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
        {sections ? (
          // Sectioned layout (P8)
          <>
            {sections.map((section) => (
              <SectionPanel
                key={section.id}
                section={section}
                schema={schema}
                record={record}
                relationLabels={relationLabels}
              />
            ))}
            {otherSection && (
              <SectionPanel
                key="__other__"
                section={otherSection}
                schema={schema}
                record={record}
                relationLabels={relationLabels}
              />
            )}
          </>
        ) : (
          // Flat field order (fallback when no layout configured)
          schema.fields.map((field) => (
            <FieldRow
              key={field.key}
              field={field}
              value={record.fields[field.key]}
              relationLabel={relationLabels[field.key]}
            />
          ))
        )}

        <RecordRelations slug={schema.slug} recordId={record.id} />
      </div>

      <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', gap: 8 }}>
        <button onClick={onDelete} style={{ padding: '10px 16px', background: '#fef2f2', color: '#dc2626', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Delete</button>
        <button onClick={onEdit} style={{ flex: 1, padding: '10px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600 }}>Edit {schema.label}</button>
      </div>
    </div>
  );
}
