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
import { LayoutGrid } from 'lucide-react';
import {
  getStages,
  listObjectRecordsUnified,
  updateObjectRecordUnified,
  type ObjectSchema,
  type PipelineStage,
  type UniformRecord,
} from '../../lib/api';
import { usePermissions } from '../../lib/auth';
import { EmptyState, SpinnerBlock } from '@/components/ui';

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
    return <SpinnerBlock label="Loading board..." />;
  }
  if (stages.length === 0) {
    return (
      <EmptyState
        icon={LayoutGrid}
        title="No pipeline stages yet. Create them in Settings → Pipeline to use the board."
      />
    );
  }

  return (
    <DndContext sensors={sensors} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
      {moveError && (
        <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{moveError}</div>
      )}
      <div className="flex gap-4 overflow-x-auto pb-2">
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
          <div className="rotate-2">
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
    <div className="w-[260px] min-w-[260px] shrink-0">
      <div className="mb-2 flex items-center gap-2 px-1">
        {/* The dot takes the stage's own configured color — user data, not chrome. */}
        <span
          aria-hidden
          className={`h-2.5 w-2.5 rounded-full ${stage.color ? '' : 'bg-muted-foreground'}`}
          style={stage.color ? { background: stage.color } : undefined}
        />
        <span className="text-sm font-semibold text-foreground">{stage.name}</span>
        <span className="text-xs text-muted-foreground">{count}</span>
      </div>
      <div
        ref={setNodeRef}
        className={`flex min-h-[120px] flex-col gap-2 rounded-xl border p-2 transition-colors ${
          isOver ? 'border-dashed border-primary bg-primary/10' : 'border-border bg-muted/50'
        }`}
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
      className={`rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
        isDragging ? 'opacity-40' : ''
      } ${disabled ? 'cursor-pointer' : 'cursor-grab'}`}
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
  const hasSubtitle = subtitle != null && subtitle !== '';
  return (
    <div className="rounded-lg border border-border bg-card p-3 shadow-sm transition-shadow hover:shadow">
      <div className={`text-sm font-semibold text-card-foreground ${hasSubtitle ? 'mb-1' : ''}`}>
        {record.display || 'Untitled'}
      </div>
      {hasSubtitle && (
        <div className="text-xs text-muted-foreground">
          {subtitleField?.label}: {String(subtitle)}
        </div>
      )}
    </div>
  );
}
