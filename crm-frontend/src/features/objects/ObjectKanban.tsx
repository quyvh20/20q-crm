import { useEffect, useMemo, useState } from 'react';
import {
  DndContext,
  DragOverlay,
  KeyboardCode,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  useDraggable,
  useDroppable,
  type Announcements,
  type ClientRect,
  type DragStartEvent,
  type DragEndEvent,
  type KeyboardCoordinateGetter,
  type ScreenReaderInstructions,
  type UniqueIdentifier,
} from '@dnd-kit/core';
import { GripVertical, LayoutGrid } from 'lucide-react';
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
  /**
   * Which pipeline's board to render (R9.3). Undefined means the org has no
   * pipelines resolved yet; the board waits rather than merging every board's
   * ladder into one set of columns.
   */
  pipelineId?: string;
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

// Keyboard dragging (R7.6). `sortableKeyboardCoordinates` — the getter the
// EmailBuilder uses and the one every dnd-kit guide reaches for — is a NO-OP on
// this board: it bails unless `droppableContainers.get(active.id)` resolves, i.e.
// unless the dragged item is itself a droppable (which is true in a sortable list
// and false here, where the cards are draggables and only the COLUMNS are
// droppables). Registering it would have shipped a sensor that never moves
// anything. This getter is written for the board's actual shape instead.
//
// The unit of movement is a column, not 25px: Left/Right jump the card to the
// centre of the adjacent column. Centring both axes is deliberate — columns are
// content-height, so a card dragged from a long column at y=800 would otherwise
// keep its y and intersect NOTHING over a short column, leaving `over` null and
// the drop silently doing nothing.
const stageKeyboardCoordinates: KeyboardCoordinateGetter = (event, { context }) => {
  const direction =
    event.code === KeyboardCode.Right ? 1 : event.code === KeyboardCode.Left ? -1 : 0;
  if (direction === 0) return;
  event.preventDefault();

  const { collisionRect, droppableRects, droppableContainers } = context;
  if (!collisionRect) return;

  const columns: { id: UniqueIdentifier; rect: ClientRect }[] = [];
  for (const container of droppableContainers.getEnabled()) {
    if (!container || container.disabled) continue;
    const rect = droppableRects.get(container.id);
    if (rect) columns.push({ id: container.id, rect });
  }
  // Left-to-right, as the board reads. droppableContainers is keyed by id, and
  // its iteration order is registration order — not board order.
  columns.sort((a, b) => a.rect.left - b.rect.left);
  if (columns.length === 0) return;

  const centre = collisionRect.left + collisionRect.width / 2;
  let at = 0;
  let closest = Number.POSITIVE_INFINITY;
  columns.forEach((column, i) => {
    const distance = Math.abs(column.rect.left + column.rect.width / 2 - centre);
    if (distance < closest) {
      closest = distance;
      at = i;
    }
  });

  const next = columns[at + direction];
  if (!next) return; // already at an end of the board: stay put

  return {
    x: next.rect.left + (next.rect.width - collisionRect.width) / 2,
    // Never above the column's own top, however tall the card is.
    y: next.rect.top + Math.max(0, (next.rect.height - collisionRect.height) / 2),
  };
};

// What a screen reader is told when a card takes focus. It replaces dnd-kit's
// default ("press space bar to pick up") because on this board Enter/Space mean
// two different things depending on WHERE focus is — see KanbanCard.
const boardInstructions: ScreenReaderInstructions = {
  draggable:
    'Press Enter or Space on a card to open the record. To move a card to another stage, ' +
    'move focus to its Move button and press Enter or Space to pick the card up, then use ' +
    'the left and right arrow keys to choose a stage, Enter or Space to drop, and Escape to cancel.',
};

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
export default function ObjectKanban({ schema, stageKey, pipelineId, onCardClick }: ObjectKanbanProps) {
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

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    // R7.6: the board was pointer-only, so a keyboard user could see the stages
    // and never change one. The getter is board-shaped (see above); the activator
    // lives on each card's Move button, not on the card.
    useSensor(KeyboardSensor, { coordinateGetter: stageKeyboardCoordinates }),
  );

  // Stages and records load INDEPENDENTLY, and each reports its own failure.
  // They used to be one Promise.all whose two .catch arms both returned [], so a
  // 500 on the stage list rendered "No pipeline stages yet — create them in
  // Settings" at a workspace whose stages exist and are fine. That is not an
  // empty state, it is wrong advice.
  useEffect(() => {
    let cancelled = false;
    setBoard(EMPTY_BOARD);

    // Scoped to the selected board (R9.3). Unscoped, a multi-pipeline org gets
    // every ladder merged into one row of columns.
    getStages(pipelineId).then(
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
          // The pipeline filter is spent on the RECORDS too, not just the
          // columns: the 500-record cap is shared, so an unfiltered fetch burns
          // it on other boards' deals and a large org sees a truncated, mostly
          // empty board.
          const page = await listObjectRecordsUnified(schema.slug, {
            limit: BOARD_PAGE_SIZE,
            cursor,
            filters: pipelineId ? { pipeline_id: pipelineId } : undefined,
          });
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
  }, [schema.slug, reloadKey, pipelineId]);

  const byStage = useMemo(() => {
    const map: Record<string, UniformRecord[]> = {};
    stages.forEach((s) => { map[s.id] = []; });
    records.forEach((r) => {
      const sid = String(r.fields[stageKey] ?? '');
      // R9.3: NO fallback to stages[0]. It used to sweep any unrecognised stage
      // into the first column, which was harmless when the column list was the
      // org's only ladder — but once columns are one board's, every foreign
      // record piles into column 1 and dragging it PATCHes it onto a stage it
      // does not belong to. Silently, with no error. A record whose stage is
      // not on this board simply is not on this board.
      if (sid && map[sid]) map[sid].push(r);
    });
    return map;
  }, [stages, records, stageKey]);

  const activeRecord = records.find((r) => r.id === activeId) || null;

  // A keyboard drag is invisible: no cursor, no card following a pointer. The
  // live region IS the feedback, so it has to name the card and the stage rather
  // than dnd-kit's default "draggable item was moved over droppable area s2".
  const announcements: Announcements = useMemo(() => {
    const cardName = (id: UniqueIdentifier) =>
      records.find((r) => r.id === id)?.display || 'Untitled';
    const stageIdOfCard = (id: UniqueIdentifier) =>
      String(records.find((r) => r.id === id)?.fields[stageKey] ?? '');
    const stageName = (id: UniqueIdentifier | undefined) =>
      stages.find((s) => s.id === id)?.name ?? 'no stage';

    return {
      onDragStart: ({ active }) =>
        `Picked up ${cardName(active.id)}, in ${stageName(stageIdOfCard(active.id))}. Use the left and right arrow keys to choose a stage.`,
      onDragOver: ({ active, over }) =>
        over
          ? `${cardName(active.id)} is over ${stageName(over.id)}.`
          : `${cardName(active.id)} is not over a stage.`,
      // "Moved to X" only when it actually moved: dropping a card back on the
      // column it came from is the normal outcome of an arrow key that had
      // nowhere to go, and announcing that as a move would be a lie.
      onDragEnd: ({ active, over }) =>
        over && String(over.id) !== stageIdOfCard(active.id)
          ? `${cardName(active.id)} was moved to ${stageName(over.id)}.`
          : `${cardName(active.id)} was dropped and stayed in ${stageName(stageIdOfCard(active.id))}.`,
      onDragCancel: ({ active }) =>
        `Move cancelled. ${cardName(active.id)} stayed in ${stageName(stageIdOfCard(active.id))}.`,
    };
  }, [records, stages, stageKey]);

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
    <DndContext
      sensors={sensors}
      accessibility={{ announcements, screenReaderInstructions: boardInstructions }}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
      // Escape cancels a keyboard drag, which fires onDragCancel and NOT
      // onDragEnd — without this the overlay card would be left on screen.
      onDragCancel={() => setActiveId(null)}
    >
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
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, isDragging } = useDraggable({ id: record.id, disabled });

  // THE ENTER COLLISION, resolved. dnd-kit already ships `role="button"` and
  // `tabIndex={0}` on every draggable, so cards were focusable before this
  // change — but nothing was bound to Enter, so a keyboard user could focus a
  // card and do precisely nothing with it. Both candidate bindings want the same
  // key: the KeyboardSensor's activator is Enter/Space, and "Enter opens the
  // record" is the obvious meaning of Enter on a focused card.
  //
  // Binding both to the card would make one of them unreachable (the sensor's
  // handler calls preventDefault and returns true, so it wins and the card can
  // never be opened from the keyboard). So they are bound to DIFFERENT elements,
  // which is the repo's existing answer to this exact clash — BlockPalette strips
  // dnd-kit's onKeyDown from its tiles for the same reason, keeping keyboard drag
  // on dedicated handles:
  //   - the CARD keeps Enter/Space = open the record (and the pointer activator,
  //     so mouse users still drag from anywhere on it);
  //   - a Move button carries the keyboard activator and is registered as the
  //     activator node, so the sensor only ever starts from there.
  //
  // dnd-kit types its listeners as `Record<string, Function>`, so the activator
  // needs a cast to be usable as a React handler.
  const { onKeyDown, ...pointerListeners } = listeners ?? {};
  const startKeyboardDrag = onKeyDown as React.KeyboardEventHandler<HTMLButtonElement> | undefined;

  // WHERE dnd-kit's `attributes` GO, and why not on the draggable node.
  //
  // `attributes` carries `role="button"` + `tabIndex={0}`. Spread on the node
  // `setNodeRef` points at — the obvious place, and dnd-kit's own examples —
  // it puts the Move <button> INSIDE an element with role="button". That is
  // invalid ARIA: role="button" takes no interactive content, and screen
  // readers that flatten such a container never expose the inner control. The
  // handle is the ONLY route into a keyboard drag, so the exact users R7.6 was
  // built for would be the ones who cannot reach it. jsdom enforces no nesting
  // rules, which is why the behavioural keyboard tests all passed over it.
  //
  // So the draggable node stays a PLAIN div and the button semantics move down
  // onto an inner wrapper around the card body; the handle is that wrapper's
  // SIBLING, not its child. Three things this keeps that moving the handle out
  // of the draggable node entirely would have cost:
  //   - the measured node is unchanged. dnd-kit measures `setNodeRef` for
  //     collision detection, and that element is still the whole card (the
  //     handle is absolutely positioned inside it), so the drop-target maths
  //     and the DragOverlay rect are byte-identical to before.
  //   - the pointer listeners stay on the whole card, so a mouse drag can still
  //     start anywhere on it — including on the grip, which is what a grip
  //     looks like it promises.
  //   - the card body keeps `aria-disabled`/`aria-roledescription` from
  //     `attributes`, so the read-only board still announces itself as one.
  return (
    <div
      ref={setNodeRef}
      {...pointerListeners}
      className={`group relative ${isDragging ? 'opacity-40' : ''} ${
        disabled ? 'cursor-pointer' : 'cursor-grab'
      }`}
    >
      <div
        {...attributes}
        onClick={onClick}
        onKeyDown={(e) => {
          // The handle is a sibling, so its Enter no longer bubbles through
          // here at all. Kept as a guard against a future child: Enter on a
          // control inside the card would mean that control, never "open".
          if (e.target !== e.currentTarget) return;
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onClick();
          }
        }}
        className="rounded-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <KanbanCardBody record={record} schema={schema} stageKey={stageKey} />
      </div>
      {!disabled && (
        <button
          ref={setActivatorNodeRef}
          type="button"
          onKeyDown={startKeyboardDrag}
          aria-label={`Move ${record.display || 'Untitled'} to another stage`}
          // dnd-kit's instructions element, so the how-to is announced on the
          // control that actually starts the drag.
          aria-describedby={attributes['aria-describedby']}
          // Hidden until the card is hovered or the handle is focused (the
          // BlockPalette idiom) — but `focus:` as well as `focus-visible:`, since
          // the handle is what a keyboard user Tabs to and it must appear then.
          className="absolute right-1 top-1 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground focus:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring group-hover:opacity-100"
        >
          <GripVertical aria-hidden className="h-3.5 w-3.5" />
        </button>
      )}
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
