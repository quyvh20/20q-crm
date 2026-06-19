import { useState, useEffect } from 'react';
import { type ObjectSchema, type UniformRecord, getObjectRecordUnified } from '../../lib/api';
import { formatFieldValue } from './fieldHelpers';
import RecordRelations from './RecordRelations';

interface ObjectDetailViewProps {
  schema: ObjectSchema;
  record: UniformRecord;
  onEdit: () => void;
  onDelete: () => void;
  onClose: () => void;
}

// ObjectDetailView is the read-only record panel for every object, rendered from
// the same schema as the list and form. Below the fields it shows the record's
// universal relationships + tags (P4), identical for system and custom objects.
export default function ObjectDetailView({ schema, record, onEdit, onDelete, onClose }: ObjectDetailViewProps) {
  // Resolve native relation FIELDS (e.g. a deal's contact/company) to the target's
  // display title, so they no longer render as raw UUIDs (closes a P3 deferral).
  // Bounded to this single record's relation fields — the list view stays raw to
  // avoid the per-row N+1 the plan warns about (§11).
  const [relationLabels, setRelationLabels] = useState<Record<string, string>>({});
  useEffect(() => {
    let cancelled = false;
    const relations = schema.fields.filter(
      (f) => f.type === 'relation' && f.target_slug && record.fields[f.key],
    );
    if (relations.length === 0) {
      setRelationLabels({});
      return;
    }
    Promise.all(
      relations.map(async (f) => {
        try {
          const target = await getObjectRecordUnified(f.target_slug!, String(record.fields[f.key]));
          return [f.key, target.display] as const;
        } catch {
          return [f.key, ''] as const;
        }
      }),
    ).then((pairs) => {
      if (cancelled) return;
      const map: Record<string, string> = {};
      for (const [k, v] of pairs) if (v) map[k] = v;
      setRelationLabels(map);
    });
    return () => {
      cancelled = true;
    };
  }, [schema, record]);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
      <div style={{ padding: '20px 24px', borderBottom: '1px solid #e2e8f0', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ margin: 0, fontWeight: 600, fontSize: 16 }}>{schema.icon} {record.display || 'Untitled'}</h3>
        <button onClick={onClose} aria-label="Close" style={{ background: 'none', border: 'none', fontSize: 20, cursor: 'pointer', color: '#64748b' }}>×</button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 24 }}>
        {schema.fields.map((field) => (
          <div key={field.key} style={{ marginBottom: 16 }}>
            <div style={{ fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase', marginBottom: 2 }}>{field.label}</div>
            <div style={{ fontSize: 14, color: '#0f172a' }}>{formatFieldValue(field, record.fields[field.key], relationLabels[field.key])}</div>
          </div>
        ))}

        <RecordRelations slug={schema.slug} recordId={record.id} />
      </div>

      <div style={{ padding: '16px 24px', borderTop: '1px solid #e2e8f0', display: 'flex', gap: 8 }}>
        <button onClick={onDelete} style={{ padding: '10px 16px', background: '#fef2f2', color: '#dc2626', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 500 }}>Delete</button>
        <button onClick={onEdit} style={{ flex: 1, padding: '10px', background: '#3b82f6', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer', fontWeight: 600 }}>Edit {schema.label}</button>
      </div>
    </div>
  );
}
