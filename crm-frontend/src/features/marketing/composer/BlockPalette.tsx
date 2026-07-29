import React from 'react';
import { useDraggable } from '@dnd-kit/core';
import { Bookmark, LayoutTemplate, Trash2 } from 'lucide-react';
import { PALETTE, type BlockTypeMeta } from './blocks';
import { cloneWithNewIds, LAYOUT_PRESETS, type LayoutPreset } from './blockUtils';
import { useBuilderStore } from './builderStore';
import { PALETTE_ICONS } from './EmailBuilder';
import { useConfirm } from '../../../components/common/ConfirmDialog';
import { useRemoveSavedBlock, useSavedBlocks } from '../savedBlocksQueries';
import type { SavedBlockRow } from '../savedBlocksApi';

/** BlockPalette is the left rail: draggable tiles for single blocks and
 *  multi-block layout presets. Everything is also clickable (appends to the end
 *  of the email) so keyboard/assistive users aren't locked out of adding blocks. */
export const BlockPalette: React.FC = () => {
  const saved = useSavedBlocks();

  return (
    <aside className="flex w-60 shrink-0 flex-col overflow-y-auto border-r border-border bg-card" aria-label="Block palette">
      <div className="p-3">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Blocks</p>
        <div className="grid grid-cols-2 gap-2">
          {PALETTE.map((m) => (
            <PaletteTile key={m.type} meta={m} />
          ))}
        </div>
      </div>
      {(saved.data?.length ?? 0) > 0 && (
        <div className="border-t border-border p-3">
          <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Saved blocks</p>
          <div className="space-y-2">
            {saved.data!.map((row) => (
              <SavedRow key={row.id} row={row} />
            ))}
          </div>
        </div>
      )}
      <div className="border-t border-border p-3">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Layouts</p>
        <div className="space-y-2">
          {LAYOUT_PRESETS.map((p) => (
            <LayoutRow key={p.key} preset={p} />
          ))}
        </div>
      </div>
      <p className="mt-auto p-3 text-[11px] leading-relaxed text-muted-foreground">
        Drag onto the canvas to place precisely, or click to add at the end.
        Save any block from its settings to reuse it here.
      </p>
    </aside>
  );
};

/** SavedRow is one library entry: drag in (or click to append) a DEEP COPY;
 *  deleting never touches content built from earlier inserts. */
const SavedRow: React.FC<{ row: SavedBlockRow }> = ({ row }) => {
  const insertBlocks = useBuilderStore((s) => s.insertBlocks);
  const remove = useRemoveSavedBlock();
  const { confirm, dialog } = useConfirm();
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `saved:${row.id}`,
    data: { kind: 'saved', label: row.name, block: row.block },
  });

  const onDelete = async (e: React.MouseEvent) => {
    e.stopPropagation();
    const ok = await confirm({
      title: 'Delete saved block?',
      body: `"${row.name}" is removed from the library. Emails already using copies of it are unaffected.`,
      confirmLabel: 'Delete',
      tone: 'danger',
    });
    if (ok) remove.mutate(row.id);
  };

  return (
    <div className="group relative">
      {dialog}
      <button
        ref={setNodeRef}
        type="button"
        onClick={() => {
          const s = useBuilderStore.getState();
          insertBlocks([cloneWithNewIds(row.block)], { parentId: null, colIndex: 0, index: s.blocks.length });
        }}
        className={`flex w-full cursor-grab items-start gap-2 rounded-lg border border-border/60 px-2.5 py-2 text-left transition-colors hover:border-ring hover:bg-accent active:cursor-grabbing ${
          isDragging ? 'opacity-40' : ''
        }`}
        {...attributes}
        {...pointerOnly(listeners)}
      >
        <Bookmark className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0">
          <span className="block truncate text-xs font-medium text-foreground">{row.name}</span>
          <span className="block truncate text-[11px] uppercase text-muted-foreground">{row.block.type}</span>
        </span>
      </button>
      <button
        type="button"
        title="Delete saved block"
        aria-label={`Delete saved block ${row.name}`}
        onClick={onDelete}
        className="absolute right-1.5 top-1.5 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-destructive/10 hover:text-destructive group-hover:opacity-100"
      >
        <Trash2 className="h-3.5 w-3.5" />
      </button>
    </div>
  );
};

/** pointerOnly strips dnd-kit's keyboard activator from a draggable's listeners:
 *  the KeyboardSensor preventDefaults Enter/Space, which would swallow the
 *  tile's native click — locking keyboard users out of the advertised
 *  click-to-add path. Keyboard drag stays available on canvas drag handles. */
function pointerOnly(listeners: Record<string, unknown> | undefined) {
  if (!listeners) return {};
  const { onKeyDown: _kb, ...rest } = listeners;
  return rest;
}

const PaletteTile: React.FC<{ meta: BlockTypeMeta }> = ({ meta }) => {
  const addBlock = useBuilderStore((s) => s.addBlock);
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `palette:${meta.type}`,
    data: { kind: 'palette', type: meta.type, label: meta.label, icon: meta.icon },
  });
  const Icon = PALETTE_ICONS[meta.icon] ?? PALETTE_ICONS.Type;

  return (
    <button
      ref={setNodeRef}
      type="button"
      onClick={() => addBlock(meta.type)}
      className={`flex cursor-grab flex-col items-center gap-1.5 rounded-lg border border-border/60 px-2 py-3 text-xs font-medium text-foreground transition-colors hover:border-ring hover:bg-accent active:cursor-grabbing ${
        isDragging ? 'opacity-40' : ''
      }`}
      {...attributes}
      {...pointerOnly(listeners)}
    >
      <Icon className="h-4 w-4 text-muted-foreground" />
      {meta.label}
    </button>
  );
};

const LayoutRow: React.FC<{ preset: LayoutPreset }> = ({ preset }) => {
  const insertBlocks = useBuilderStore((s) => s.insertBlocks);
  const { attributes, listeners, setNodeRef, isDragging } = useDraggable({
    id: `layout:${preset.key}`,
    data: { kind: 'layout', key: preset.key, label: preset.label },
  });

  return (
    <button
      ref={setNodeRef}
      type="button"
      onClick={() => {
        const s = useBuilderStore.getState();
        insertBlocks(preset.make(), { parentId: null, colIndex: 0, index: s.blocks.length });
      }}
      className={`flex w-full cursor-grab items-start gap-2 rounded-lg border border-border/60 px-2.5 py-2 text-left transition-colors hover:border-ring hover:bg-accent active:cursor-grabbing ${
        isDragging ? 'opacity-40' : ''
      }`}
      {...attributes}
      {...pointerOnly(listeners)}
    >
      <LayoutTemplate className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
      <span className="min-w-0">
        <span className="block text-xs font-medium text-foreground">{preset.label}</span>
        <span className="block truncate text-[11px] text-muted-foreground">{preset.description}</span>
      </span>
    </button>
  );
};

export default BlockPalette;
