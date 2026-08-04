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
import { EmptyState, ErrorState, SpinnerBlock } from '@/components/ui';

interface ObjectKanbanProps {
  schema: ObjectSchema;
  /** Relation field key the board groups by (e.g. "stage"). */
  stageKey: string;
  onCardClick: (record: UniformRecord) => void;
}

// The backend clamps any requested limit above 100 down to 100
// (usecase.maxRecordLimit), so a single request can never fill a board. The
// board pages instead, up to a hard ceiling: 5 round trips is the most we will
// spend before telling the user to narrow the set.
const BOARD_PAGE_SIZE = 100;
const BOARD_CAP = 500;
// A SECOND, independent stop condition. The record count alone is not enough to
// terminate the loop: duplicates are dropped, so a cursor that ever repeats a
// page would advance forever while the accumulator stands still. Counting
// requests can't be fooled that way.
const BOARD_MAX_PAGES = Math.ceil(BOARD_CAP / BOARD_PAGE_SIZE);

/**
 * Everything the board loads asynchronously, in ONE state object.
 *
 * Held together rather than as six useStates so the effect can reset all of it
 * with a single synchronous setState — six would be six cascading renders on
 * every slug switch, and stale halves would be visible in between (old stages
 * next to new records).
 */
interface BoardState {
  stages: PipelineStage[];
  records: UniformRecord[];
  stagesLoading: boolean;
  /** True until the FIRST page of records lands; later pages paint in place. */
  firstPageLoading: boolean;
  stagesError: unknown;
  recordsError: unknown;
  /** More records exist than BOARD_CAP; the board is a prefix of the object. */
  truncated: boolean;
}

const EMPTY_BOARD: BoardState = {
  stages: [],
  records: [],
  stagesLoading: true,
  firstPageLoading: true,
  stagesError: null,
  recordsError: null,
  truncated: false,
};

// ObjectKanban renders any stage-bearing object as a board, generically: columns
// come from the pipeline stages, cards from the uniform record list, and a drag
// applies the stage change through the uniform write path — which routes deals
// through ChangeStage (won/lost + automation) on the backend (P7). Today only Deals
// expose a "stage" field, but any future object with one gets a board for free.
export default function ObjectKanban({ schema, stageKey, onCardClick }: ObjectKanbanProps) {
  const [board, setBoard] = useState<BoardState>(EMPTY_BOARD);
  const { stages, records } = board;
  // Bumped by Retry; the loader effect depends on it, so a click re-runs it.
  const [reloadKey, setReloadKey] = useState(0);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [moveError, setMoveError] = useState('');
  // A card drag is a record edit: roles without the OLS edit bit get a
  // read-only board instead of drags that only 403-and-snap-back (U3). Fails
  // open while permissions load; the server enforces the write regardless.
  const { canAccess } = usePermissions();
  const canEdit = canAccess(schema.slug, 'edit');

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 8 } }));

  // Stages and records load INDEPENDENTLY, and each reports its own failure.
  // They used to be one Promise.all whose two .catch arms both returned [], so a
  // 500 on the stage list rendered "No pipeline stages yet — create them in
  // Settings" at a workspace whose stages exist and are fine. That is not an
  // empty state, it is wrong advice.
  useEffect(() => {
    let cancelled = false;
    setBoard(EMPTY_BOARD);

    getStages().then(
      (s) => { if (!cancelled) setBoard((b) => ({ ...b, stages: s, stagesLoading: false })); },
      (e) => { if (!cancelled) setBoard((b) => ({ ...b, stagesError: e, stagesLoading: false })); },
    );

    void (async () => {
      const seen = new Set<string>();
      const acc: UniformRecord[] = [];
      let cursor: string | undefined;
      let fetched = 0;
      try {
        do {
          const page = await listObjectRecordsUnified(schema.slug, { limit: BOARD_PAGE_SIZE, cursor });
          fetched += 1;
          // Re-checked after EVERY page: without this a slug switch keeps
          // paging and writes into an unmounted board.
          if (cancelled) return;
          for (const r of page.records) {
            // dnd-kit keys its draggable registry by id and React keys the
            // cards by id, so a duplicate from a misbehaving cursor would
            // corrupt both. Belt and braces: drop repeats.
            if (seen.has(r.id)) continue;
            seen.add(r.id);
            acc.push(r);
          }
          cursor = page.next_cursor;
          // Paint each page as it lands — serial paging to the cap is 5 round
          // trips, and one spinner held over all of them reads as a hang.
          setBoard((b) => ({ ...b, records: [...acc], firstPageLoading: false }));
        } while (cursor && acc.length < BOARD_CAP && fetched < BOARD_MAX_PAGES);
        if (!cancelled) setBoard((b) => ({ ...b, truncated: !!cursor }));
      } catch (e) {
        if (!cancelled) setBoard((b) => ({ ...b, recordsError: e, firstPageLoading: false }));
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [schema.slug, reloadKey]);

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
    //
    // The revert restores ONE field on ONE card, not a snapshot of the whole
    // list. Snapshotting `records` at drag start and assigning it back was a
    // lost update: the board pages in the background, so any page that landed
    // between the drag and the failed PATCH was silently thrown away.
    const previousStage = rec.fields[stageKey];
    const setStageOn = (id: string, value: unknown) =>
      setBoard((b) => ({
        ...b,
        records: b.records.map((r) => (r.id === id ? { ...r, fields: { ...r.fields, [stageKey]: value } } : r)),
      }));

    setMoveError('');
    setStageOn(rec.id, targetStage);
    updateObjectRecordUnified(schema.slug, rec.id, { [stageKey]: targetStage }).catch((err) => {
      setStageOn(rec.id, previousStage);
      setMoveError(err instanceof Error ? err.message : 'Failed to move record');
    });
  };

  const reload = () => setReloadKey((k) => k + 1);

  // The columns ARE the board, so a stage failure is fatal to it; a record
  // failure is not, and shows as a banner over whatever did load.
  //
  // The failure is checked BEFORE the spinner on purpose. Stages and records
  // load independently, so a stage request that rejects in 20ms used to sit
  // behind "Loading board..." until the (unrelated, still-pending) first record
  // page landed — up to a full round trip of showing a spinner for an outcome
  // already known to be an error.
  if (board.stagesError) {
    return (
      <ErrorState
        title="Couldn't load the pipeline stages."
        error={board.stagesError}
        onRetry={reload}
      />
    );
  }
  if (board.stagesLoading || board.firstPageLoading) {
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
      {/* Through the shared primitive, not a hand-rolled div: a failed MOVE is
          exactly as announceable as a failed LOAD, and only ErrorState carries
          role="alert". A silent snap-back is invisible to a screen reader. */}
      {moveError && (
        <ErrorState compact className="mb-3" title="Couldn't move the card." error={moveError} />
      )}
      {board.recordsError != null && (
        <ErrorState
          compact
          className="mb-3"
          title={`Couldn't load ${schema.label_plural.toLowerCase()}.`}
          error={board.recordsError}
          onRetry={reload}
        />
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
      {/* domain.RecordList carries no total, so there is no honest "of N" to
          print here — only that what you see is a prefix.
          The count is what is ON the board, not the cap: the loop also stops on
          BOARD_MAX_PAGES, and short pages or ids dropped by the dedupe leave
          fewer than BOARD_CAP cards. Printing the cap would overstate. */}
      {board.truncated && (
        <p className="mt-3 text-center text-xs text-muted-foreground">
          Showing the first {records.length} — refine with filters.
        </p>
      )}
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
