import React, { useCallback, useRef, useState } from 'react';
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  pointerWithin,
  rectIntersection,
  useSensor,
  useSensors,
  type Active,
  type CollisionDetection,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
  type Over,
} from '@dnd-kit/core';
import { sortableKeyboardCoordinates } from '@dnd-kit/sortable';
import {
  Bookmark, CirclePlay, CodeXml, Columns2, GripVertical, Heading, Image as ImageIcon, LayoutTemplate,
  Menu as MenuIcon, Minus, MousePointerClick, MoveVertical, Quote as QuoteIcon, Share2, Type,
} from 'lucide-react';
import { makeBlock, type Block, type BlockType } from './blocks';
import { canPlaceIn, cloneWithNewIds, findBlock, LAYOUT_PRESETS, moveTo, type BlockAddress } from './blockUtils';
import { useBuilderStore } from './builderStore';
import { BlockPalette } from './BlockPalette';
import { BuilderCanvas } from './BuilderCanvas';
import { InspectorPanel, type InspectorMeta } from './InspectorPanel';
import type { VariableGroup } from './mergeScope';

// Keys MUST cover every PALETTE entry's icon name — the ?? Type fallback
// otherwise renders look-alike "T" tiles (the round-2 regression).
export const PALETTE_ICONS: Record<string, React.ComponentType<{ className?: string }>> = {
  Type, Heading, MousePointerClick, Image: ImageIcon, Columns2, Minus, MoveVertical, CodeXml,
  Share2, CirclePlay, Quote: QuoteIcon, Menu: MenuIcon,
};

type ActiveDrag =
  | { kind: 'block'; id: string; type: BlockType }
  | { kind: 'palette'; type: BlockType; label: string; icon: string }
  | { kind: 'layout'; key: string; label: string }
  | { kind: 'saved'; label: string; block: Block };

interface Props {
  variableGroups: VariableGroup[];
  inspector: InspectorMeta;
  readOnly?: boolean;
}

/** EmailBuilder is the three-pane editing area (palette / canvas / inspector) and
 *  the drag-and-drop brain. Two drag modes share one DndContext:
 *   - existing blocks reorder via sortable transforms (same list) and transient
 *     store mutations (cross container), committed as ONE undo step on drop;
 *   - palette tiles and layout presets show a drop-indicator line and insert on
 *     drop (nothing transient touches the document while they hover). */
export const EmailBuilder: React.FC<Props> = ({ variableGroups, inspector, readOnly }) => {
  const store = useBuilderStore;
  const [activeDrag, setActiveDrag] = useState<ActiveDrag | null>(null);
  const [dropHint, setDropHintRaw] = useState<BlockAddress | null>(null);
  const hintRef = useRef<BlockAddress | null>(null);

  const setDropHint = useCallback((next: BlockAddress | null) => {
    const cur = hintRef.current;
    if (
      cur === next ||
      (cur && next && cur.parentId === next.parentId && cur.colIndex === next.colIndex && cur.index === next.index)
    ) {
      return;
    }
    hintRef.current = next;
    setDropHintRaw(next);
  }, []);

  const sensors = useSensors(
    // distance 8 keeps plain clicks (select, cursor placement) from starting a
    // drag — the Kanban's proven constraint.
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  // Priority: leaf block items > column droppables > everything else. A column
  // droppable always sits geometrically inside its parent columns BLOCK (itself
  // a droppable via useSortable), so a naive "prefer blocks" filter would make
  // empty columns unreachable — the parent would swallow every drop.
  const collisionDetection: CollisionDetection = useCallback((args) => {
    const within = pointerWithin(args);
    const collisions = within.length > 0 ? within : rectIntersection(args);
    const dataOf = (id: string | number) =>
      args.droppableContainers.find((dc) => dc.id === id)?.data.current as Record<string, unknown> | undefined;
    const leaves = collisions.filter((c) => {
      const d = dataOf(c.id);
      return d?.kind === 'block' && d?.type !== 'columns';
    });
    if (leaves.length > 0) return leaves;
    const columns = collisions.filter((c) => {
      const d = dataOf(c.id);
      return d?.kind === 'container' && d?.parentId != null;
    });
    if (columns.length > 0) return columns;
    return collisions;
  }, []);

  const onDragStart = (e: DragStartEvent) => {
    const d = e.active.data.current as Record<string, unknown> | undefined;
    if (!d) return;
    if (d.kind === 'block') {
      store.getState().beginDrag();
      setActiveDrag({ kind: 'block', id: String(e.active.id), type: d.type as BlockType });
    } else if (d.kind === 'palette') {
      setActiveDrag({ kind: 'palette', type: d.type as BlockType, label: String(d.label), icon: String(d.icon) });
    } else if (d.kind === 'layout') {
      setActiveDrag({ kind: 'layout', key: String(d.key), label: String(d.label) });
    } else if (d.kind === 'saved') {
      setActiveDrag({ kind: 'saved', label: String(d.label), block: d.block as Block });
    }
  };

  /** resolveTarget maps an `over` droppable to a container + insertion index. */
  const resolveTarget = (active: Active, over: Over): BlockAddress | null => {
    const d = over.data.current as Record<string, unknown> | undefined;
    if (!d) return null;
    if (d.kind === 'container') {
      const parentId = (d.parentId as string | null) ?? null;
      const colIndex = (d.colIndex as number) ?? 0;
      const blocks = store.getState().blocks;
      const list = parentId === null
        ? blocks
        : blocks.find((b) => b.id === parentId)?.columns?.[colIndex] ?? [];
      return { parentId, colIndex, index: list.length };
    }
    if (d.kind === 'block') {
      const parentId = (d.parentId as string | null) ?? null;
      const colIndex = (d.colIndex as number) ?? 0;
      // Recompute the index from the live document — data.current captures it at
      // render time and can lag one dragover behind.
      const found = findBlock(store.getState().blocks, String(over.id));
      const index = found ? found.index : (d.index as number);
      return { parentId, colIndex, index: index + (isBelow(active, over) ? 1 : 0) };
    }
    return null;
  };

  /** rootFallback retargets an in-column address to the root slot beside the
   *  enclosing columns block — so a drag whose payload is illegal inside columns
   *  (spacer, columns, layout presets) stays live over their interior instead of
   *  dead-zoning. */
  const rootFallback = (target: BlockAddress, active: Active, over: Over): BlockAddress | null => {
    if (target.parentId === null) return null;
    const blocks = store.getState().blocks;
    const qIndex = blocks.findIndex((b) => b.id === target.parentId);
    if (qIndex < 0) return null;
    return { parentId: null, colIndex: 0, index: qIndex + (isBelow(active, over) ? 1 : 0) };
  };

  const onDragOver = (e: DragOverEvent) => {
    const { active, over } = e;
    if (!activeDrag) return;

    if (activeDrag.kind !== 'block') {
      // Palette/layout: indicator only.
      if (!over) {
        setDropHint(null);
        return;
      }
      const target = resolveTarget(active, over);
      if (!target) {
        setDropHint(null);
        return;
      }
      const legal = activeDrag.kind === 'layout'
        ? target.parentId === null // presets may contain columns/spacers — root only
        : canPlaceIn(target.parentId, activeDrag.kind === 'saved' ? activeDrag.block.type : activeDrag.type);
      setDropHint(legal ? target : rootFallback(target, active, over));
      return;
    }

    // Existing block: transient cross-container move (same-container ordering is
    // handled visually by sortable transforms and committed on drop).
    if (!over || String(over.id) === activeDrag.id) return;
    const blocks = store.getState().blocks;
    const current = findBlock(blocks, activeDrag.id);
    let target = resolveTarget(active, over);
    if (!current || !target) return;
    if (target.parentId === activeDrag.id) return;
    if (!canPlaceIn(target.parentId, activeDrag.type)) {
      const fb = rootFallback(target, active, over);
      if (!fb) return;
      target = fb;
    }
    if (current.parentId === target.parentId && current.colIndex === target.colIndex) return;
    store.getState().setBlocksDuringDrag(moveTo(blocks, activeDrag.id, target));
  };

  const onDragEnd = (e: DragEndEvent) => {
    const { active, over } = e;
    const drag = activeDrag;
    setActiveDrag(null);

    if (!drag) return;

    if (drag.kind === 'palette' || drag.kind === 'layout' || drag.kind === 'saved') {
      const hint = hintRef.current;
      setDropHint(null);
      if (!hint || !over) return;
      const blocks = drag.kind === 'palette'
        ? [makeBlock(drag.type)]
        : drag.kind === 'saved'
          ? [cloneWithNewIds(drag.block)]
          : LAYOUT_PRESETS.find((p) => p.key === drag.key)?.make() ?? [];
      store.getState().insertBlocks(blocks, hint);
      return;
    }

    // Existing block drop.
    setDropHint(null);
    if (!over) {
      store.getState().endDrag(false);
      return;
    }
    const blocks = store.getState().blocks;
    const current = findBlock(blocks, drag.id);
    let target = resolveTarget(active, over);
    if (target && !canPlaceIn(target.parentId, drag.type)) target = rootFallback(target, active, over);
    if (current && target && target.parentId !== drag.id) {
      const sameList = current.parentId === target.parentId && current.colIndex === target.colIndex;
      if (sameList) {
        const overData = over.data.current as Record<string, unknown> | undefined;
        const overFound = String(over.id) === drag.id ? null : findBlock(blocks, String(over.id));
        if (overFound) {
          // Same-list drop on an item: land exactly where sortable previewed it
          // (arrayMove semantics — moveTo compensates for the removal itself).
          const idx = overFound.index + (current.index < overFound.index ? 1 : 0);
          store.getState().setBlocksDuringDrag(moveTo(blocks, drag.id, { ...target, index: idx }));
        } else if (overData?.kind === 'container') {
          // Released over the list's trailing space (below the last block):
          // append instead of silently snapping back.
          store.getState().setBlocksDuringDrag(moveTo(blocks, drag.id, target));
        }
      }
      // Cross-list positioning already happened transiently in onDragOver.
    }
    store.getState().endDrag(true);
  };

  const onDragCancel = () => {
    if (activeDrag?.kind === 'block') store.getState().endDrag(false);
    setActiveDrag(null);
    setDropHint(null);
  };

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={collisionDetection}
      onDragStart={onDragStart}
      onDragOver={onDragOver}
      onDragEnd={onDragEnd}
      onDragCancel={onDragCancel}
    >
      <div className="flex min-h-0 flex-1">
        {!readOnly && <BlockPalette />}
        <BuilderCanvas variableGroups={variableGroups} dropHint={dropHint} readOnly={readOnly} />
        {!readOnly && <InspectorPanel variableGroups={variableGroups} meta={inspector} />}
      </div>

      <DragOverlay dropAnimation={null}>
        {activeDrag?.kind === 'palette' && (
          <div className="flex w-40 items-center gap-2 rounded-lg border border-primary/50 bg-card px-3 py-2 text-sm font-medium text-foreground shadow-lg">
            <PaletteIcon name={activeDrag.icon} />
            {activeDrag.label}
          </div>
        )}
        {activeDrag?.kind === 'layout' && (
          <div className="flex w-48 items-center gap-2 rounded-lg border border-primary/50 bg-card px-3 py-2 text-sm font-medium text-foreground shadow-lg">
            <LayoutTemplate className="h-4 w-4 text-muted-foreground" />
            {activeDrag.label}
          </div>
        )}
        {activeDrag?.kind === 'saved' && (
          <div className="flex w-48 items-center gap-2 rounded-lg border border-primary/50 bg-card px-3 py-2 text-sm font-medium text-foreground shadow-lg">
            <Bookmark className="h-4 w-4 text-muted-foreground" />
            {activeDrag.label}
          </div>
        )}
        {activeDrag?.kind === 'block' && (
          <div className="flex items-center gap-2 rounded-lg border border-primary/50 bg-card px-3 py-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground shadow-lg">
            <GripVertical className="h-3.5 w-3.5" />
            {activeDrag.type}
          </div>
        )}
      </DragOverlay>
    </DndContext>
  );
};

const PaletteIcon: React.FC<{ name: string }> = ({ name }) => {
  const Icon = PALETTE_ICONS[name] ?? Type;
  return <Icon className="h-4 w-4 text-muted-foreground" />;
};

function isBelow(active: Active, over: Over): boolean {
  const t = active.rect.current.translated;
  if (!t) return false;
  return t.top > over.rect.top + over.rect.height / 2;
}

export default EmailBuilder;
