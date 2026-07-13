import { useEffect, useMemo, useState } from 'react';
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useSensor,
  useSensors,
  useDraggable,
  useDroppable,
  type DragStartEvent,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  getStages,
  listObjectRecordsUnified,
  updateObjectRecordUnified,
  type ObjectSchema,
  type PipelineStage,
  type UniformRecord,
} from '../../lib/api';
import { usePermissions } from '../../lib/auth';

interface ObjectKanbanProps {
  schema: ObjectSchema;
  /** Relation field key the board groups by (e.g. "stage"). */
  stageKey: string;
  onCardClick: (record: UniformRecord) => void;
}

// ObjectKanban renders any stage-bearing object as a board, generically: columns
// come from the pipeline stages, cards from the uniform record list, and a drag
// applies the stage change through the uniform write path — which routes deals
// through ChangeStage (won/lost + automation) on the backend (P7). Today only Deals
// expose a "stage" field, but any future object with one gets a board for free.
export default function ObjectKanban({ schema, stageKey, onCardClick }: ObjectKanbanProps) {
  const [stages, setStages] = useState<PipelineStage[]>([]);
  const [records, setRecords] = useState<UniformRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [moveError, setMoveError] = useState('');
  // A card drag is a record edit: roles without the OLS edit bit get a
  // read-only board instead of drags that only 403-and-snap-back (U3). Fails
  // open while permissions load; the server enforces the write regardless.
  const { canAccess } = usePermissions();
  const canEdit = canAccess(schema.slug, 'edit');

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 8 } }));

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    Promise.all([
      getStages().catch(() => [] as PipelineStage[]),
      listObjectRecordsUnified(schema.slug, { limit: 200 }).then((p) => p.records).catch(() => [] as UniformRecord[]),
    ]).then(([s, recs]) => {
      if (cancelled) return;
      setStages(s);
      setRecords(recs);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [schema.slug]);

  const byStage = useMemo(() => {
    const map: Record<string, UniformRecord[]> = {};
    stages.forEach((s) => { map[s.id] = []; });
    records.forEach((r) => {
      let sid = String(r.fields[stageKey] ?? '');
      if (!sid || !map[sid]) sid = stages[0]?.id ?? '';
      if (sid && map[sid]) map[sid].push(r);
    });
    return map;
  }, [stages, records, stageKey]);

  const activeRecord = records.find((r) => r.id === activeId) || null;

  const handleDragStart = (e: DragStartEvent) => setActiveId(String(e.active.id));

  const handleDragEnd = (e: DragEndEvent) => {
    setActiveId(null);
    const { active, over } = e;
    if (!over) return;
    const rec = records.find((r) => r.id === active.id);
    const targetStage = String(over.id);
    if (!rec || String(rec.fields[stageKey] ?? '') === targetStage) return;
    if (!stages.some((s) => s.id === targetStage)) return;

    // Optimistic move; revert on failure — and say so, a silent snap-back
    // reads as a broken board.
    const prev = records;
    setMoveError('');
    setRecords((rs) => rs.map((r) => (r.id === rec.id ? { ...r, fields: { ...r.fields, [stageKey]: targetStage } } : r)));
    updateObjectRecordUnified(schema.slug, rec.id, { [stageKey]: targetStage }).catch((err) => {
      setRecords(prev);
      setMoveError(err instanceof Error ? err.message : 'Failed to move record');
    });
  };

  if (loading) {
    return <div style={{ padding: 40, color: '#94a3b8', textAlign: 'center' }}>Loading board...</div>;
  }
  if (stages.length === 0) {
    return (
      <div style={{ padding: 40, color: '#64748b', textAlign: 'center', border: '2px dashed #e2e8f0', borderRadius: 12 }}>
        No pipeline stages yet. Create them in Settings → Pipeline to use the board.
      </div>
    );
  }

  return (
    <DndContext sensors={sensors} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
      {moveError && (
        <div style={{ background: '#fef2f2', color: '#dc2626', padding: '8px 12px', borderRadius: 6, marginBottom: 12, fontSize: 13 }}>{moveError}</div>
      )}
      <div style={{ display: 'flex', gap: 16, overflowX: 'auto', paddingBottom: 8 }}>
        {stages.map((stage) => (
          <KanbanColumn key={stage.id} stage={stage} count={(byStage[stage.id] || []).length}>
            {(byStage[stage.id] || []).map((rec) => (
              <KanbanCard key={rec.id} record={rec} schema={schema} stageKey={stageKey} disabled={!canEdit} onClick={() => onCardClick(rec)} />
            ))}
          </KanbanColumn>
        ))}
      </div>
      <DragOverlay>
        {activeRecord ? (
          <div style={{ transform: 'rotate(2deg)' }}>
            <KanbanCardBody record={activeRecord} schema={schema} stageKey={stageKey} />
          </div>
        ) : null}
      </DragOverlay>
    </DndContext>
  );
}

function KanbanColumn({ stage, count, children }: { stage: PipelineStage; count: number; children: React.ReactNode }) {
  const { setNodeRef, isOver } = useDroppable({ id: stage.id });
  return (
    <div style={{ minWidth: 260, width: 260, flexShrink: 0 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <span style={{ width: 10, height: 10, borderRadius: 999, background: stage.color || '#94a3b8' }} />
        <span style={{ fontWeight: 600, fontSize: 13 }}>{stage.name}</span>
        <span style={{ color: '#94a3b8', fontSize: 12 }}>{count}</span>
      </div>
      <div
        ref={setNodeRef}
        style={{
          minHeight: 120, padding: 8, borderRadius: 10,
          background: isOver ? '#eff6ff' : '#f8fafc',
          border: isOver ? '1px dashed #3b82f6' : '1px solid #e2e8f0',
          display: 'flex', flexDirection: 'column', gap: 8,
        }}
      >
        {children}
      </div>
    </div>
  );
}

function KanbanCard({ record, schema, stageKey, disabled, onClick }: { record: UniformRecord; schema: ObjectSchema; stageKey: string; disabled: boolean; onClick: () => void }) {
  // dnd-kit's own disable: no listeners are attached and aria-disabled is set,
  // so the card stays a plain clickable link to the record page.
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({ id: record.id, disabled });
  return (
    <div
      ref={setNodeRef}
      {...listeners}
      {...attributes}
      onClick={onClick}
      style={{ opacity: isDragging ? 0.4 : 1, cursor: disabled ? 'pointer' : 'grab' }}
    >
      <KanbanCardBody record={record} schema={schema} stageKey={stageKey} />
    </div>
  );
}

function KanbanCardBody({ record, schema, stageKey }: { record: UniformRecord; schema: ObjectSchema; stageKey: string }) {
  // Show the display title plus the first non-relation, non-stage value as a subtitle.
  const subtitleField = schema.fields.find(
    (f) => f.key !== stageKey && f.type !== 'relation' && f.key !== schema.display_field,
  );
  const subtitle = subtitleField ? record.fields[subtitleField.key] : undefined;
  return (
    <div style={{ background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8, padding: '10px 12px', boxShadow: '0 1px 2px rgba(0,0,0,0.04)' }}>
      <div style={{ fontWeight: 600, fontSize: 13, marginBottom: subtitle != null && subtitle !== '' ? 4 : 0 }}>
        {record.display || 'Untitled'}
      </div>
      {subtitle != null && subtitle !== '' && (
        <div style={{ fontSize: 12, color: '#64748b' }}>
          {subtitleField?.label}: {String(subtitle)}
        </div>
      )}
    </div>
  );
}
