import React, { useState } from 'react';
import { useSortable, SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { useDroppable } from '@dnd-kit/core';
import { CSS } from '@dnd-kit/utilities';
import { ChevronDown, ChevronUp, Copy, GripVertical, Image as ImageIcon, Trash2 } from 'lucide-react';
import type { Block } from './blocks';
import type { BlockAddress } from './blockUtils';
import { useBuilderStore } from './builderStore';
import { InlineRichText } from './InlineRichText';
import type { VariableGroup } from './mergeScope';

// The canvas mirrors the compiled email's real metrics (compile.go + MJML
// defaults): every root block is its own <mj-section> (20px 0) and components
// carry MJML's default 10px 25px padding — so canvas spacing ≈ inbox spacing.
const SECTION_PAD = '20px 0';
const COMPONENT_PAD = '10px 25px';

export interface CanvasBlockProps {
  block: Block;
  parentId: string | null;
  colIndex: number;
  index: number;
  listLength: number;
  variableGroups: VariableGroup[];
  dropHint: BlockAddress | null;
  readOnly?: boolean;
}

/** DropIndicator marks the gap a palette/layout drag would insert into. */
export const DropIndicator: React.FC = () => (
  <div className="pointer-events-none relative z-20 h-0">
    <div className="absolute inset-x-3 -top-0.5 h-1 rounded-full bg-primary shadow-[0_0_6px_1px] shadow-primary/40" />
  </div>
);

export const CanvasBlock: React.FC<CanvasBlockProps> = ({ block, parentId, colIndex, index, listLength, variableGroups, dropHint, readOnly }) => {
  const selectedId = useBuilderStore((s) => s.selectedId);
  const select = useBuilderStore((s) => s.select);
  const patchBlock = useBuilderStore((s) => s.patchBlock);
  const removeBlock = useBuilderStore((s) => s.removeBlock);
  const duplicateBlock = useBuilderStore((s) => s.duplicateBlock);
  const moveBlock = useBuilderStore((s) => s.moveBlock);

  const selected = selectedId === block.id;
  const nested = parentId !== null;

  // Explicit hover state with stopPropagation instead of Tailwind `group`:
  // blocks nest (columns > sub-blocks) and group-hover matches ANY ancestor
  // group, so hovering a sub-block would light up the parent's chrome too —
  // overlapping action bars right where the user is aiming.
  const [hovered, setHovered] = useState(false);

  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: block.id,
    data: { kind: 'block', parentId, colIndex, index, type: block.type },
    disabled: readOnly,
  });

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  const moveBy = (dir: -1 | 1) => {
    // moveTo compensates for same-list removal, so "down one" targets index+2.
    const target = dir === -1 ? index - 1 : index + 2;
    moveBlock(block.id, { parentId, colIndex, index: target });
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`relative ${isDragging ? 'opacity-40' : ''}`}
      onClick={(e) => {
        e.stopPropagation();
        select(block.id);
      }}
      // Selection is also driven by focus so keyboard users reach the format
      // toolbar and inspector without a mouse.
      onFocusCapture={() => { if (!readOnly && !selected) select(block.id); }}
      onMouseOver={(e) => { e.stopPropagation(); setHovered(true); }}
      onMouseOut={(e) => { e.stopPropagation(); setHovered(false); }}
    >
      {/* selection / hover ring */}
      <div
        className={`pointer-events-none absolute inset-0 z-10 rounded-sm ${
          selected ? 'ring-2 ring-primary' : hovered ? 'ring-1 ring-primary/40' : ''
        }`}
      />

      {!readOnly && (
        <>
          {/* type label */}
          <span
            className={`absolute -top-2.5 left-2 z-20 rounded bg-primary px-1.5 py-px text-[10px] font-semibold uppercase tracking-wide text-primary-foreground transition-opacity ${
              selected ? 'opacity-100' : 'opacity-0'
            }`}
          >
            {block.type}
          </span>

          {/* action bar */}
          <div
            className={`absolute -top-3.5 right-2 z-30 flex items-center gap-0.5 rounded-lg border border-border bg-card p-0.5 shadow-md transition-opacity ${
              selected || hovered ? 'opacity-100' : 'opacity-0 focus-within:opacity-100'
            }`}
            onClick={(e) => e.stopPropagation()}
          >
            <button
              type="button"
              title="Drag to move"
              aria-label={`Drag ${block.type} block`}
              className="flex h-6 w-6 cursor-grab items-center justify-center rounded text-muted-foreground hover:bg-accent hover:text-foreground active:cursor-grabbing"
              {...attributes}
              {...listeners}
            >
              <GripVertical className="h-3.5 w-3.5" />
            </button>
            <AB title="Move up" disabled={index === 0} onClick={() => moveBy(-1)}><ChevronUp className="h-3.5 w-3.5" /></AB>
            <AB title="Move down" disabled={index === listLength - 1} onClick={() => moveBy(1)}><ChevronDown className="h-3.5 w-3.5" /></AB>
            <AB title="Duplicate" onClick={() => duplicateBlock(block.id)}><Copy className="h-3.5 w-3.5" /></AB>
            <AB title="Delete" destructive onClick={() => removeBlock(block.id)}><Trash2 className="h-3.5 w-3.5" /></AB>
          </div>
        </>
      )}

      <BlockBody
        block={block}
        nested={nested}
        selected={selected && !readOnly}
        hovered={hovered}
        variableGroups={variableGroups}
        dropHint={dropHint}
        readOnly={readOnly}
        onText={(html) => patchBlock(block.id, { text: html }, `text:${block.id}`)}
      />
    </div>
  );
};

const AB: React.FC<{ title: string; onClick: () => void; disabled?: boolean; destructive?: boolean; children: React.ReactNode }> = ({ title, onClick, disabled, destructive, children }) => (
  <button
    type="button"
    title={title}
    aria-label={title}
    disabled={disabled}
    onClick={onClick}
    className={`flex h-6 w-6 items-center justify-center rounded disabled:opacity-30 ${
      destructive ? 'text-destructive hover:bg-destructive/10' : 'text-muted-foreground hover:bg-accent hover:text-foreground'
    }`}
  >
    {children}
  </button>
);

const BlockBody: React.FC<{
  block: Block;
  nested: boolean;
  selected: boolean;
  hovered: boolean;
  variableGroups: VariableGroup[];
  dropHint: BlockAddress | null;
  readOnly?: boolean;
  onText: (html: string) => void;
}> = ({ block, nested, selected, hovered, variableGroups, dropHint, readOnly, onText }) => {
  const sectionPad = nested ? undefined : SECTION_PAD;

  switch (block.type) {
    case 'text':
      return (
        <div style={{ padding: sectionPad }}>
          <div style={{ padding: COMPONENT_PAD }}>
            <InlineRichText
              html={block.text ?? ''}
              variableGroups={variableGroups}
              onChange={onText}
              active={selected}
              variant="text"
              align={block.align}
              readOnly={readOnly}
            />
          </div>
        </div>
      );

    case 'heading':
      return (
        <div style={{ padding: sectionPad }}>
          <div style={{ padding: COMPONENT_PAD }}>
            <InlineRichText
              html={block.text ?? ''}
              variableGroups={variableGroups}
              onChange={onText}
              active={selected}
              variant="heading"
              level={block.level}
              nested={nested}
              align={block.align}
              readOnly={readOnly}
            />
          </div>
        </div>
      );

    case 'button':
      return (
        <div style={{ padding: sectionPad }}>
          {/* mj-button: centered container, #414141 pill — compiler ignores align */}
          <div style={{ padding: COMPONENT_PAD, textAlign: 'center' }}>
            <span className="email-button-preview">{block.label || 'Button'}</span>
          </div>
        </div>
      );

    case 'image':
      return (
        <div style={{ padding: sectionPad }}>
          <div style={{ padding: COMPONENT_PAD, textAlign: 'center' }}>
            {block.src ? (
              <img
                src={block.src}
                alt={block.alt ?? ''}
                style={{ maxWidth: '100%', display: 'inline-block' }}
                draggable={false}
              />
            ) : (
              // email-hint: fixed grays — theme tokens flip in dark mode and
              // wash out on this always-white surface.
              <div className="email-hint flex flex-col items-center gap-1 rounded border border-dashed py-8">
                <ImageIcon className="h-6 w-6" />
                <span className="text-xs">Set an image URL in the inspector</span>
              </div>
            )}
          </div>
        </div>
      );

    case 'divider':
      return (
        <div style={{ padding: sectionPad }}>
          <div style={{ padding: COMPONENT_PAD }}>
            <div style={{ borderTop: '1px solid #e5e7eb' }} />
          </div>
        </div>
      );

    case 'spacer': {
      const h = block.height && block.height > 0 ? block.height : 20;
      return (
        <div style={{ padding: sectionPad }}>
          <div className="email-spacer-hatch relative" style={{ height: `${h}px` }}>
            <span
              className={`email-hint absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2 rounded px-1.5 py-px text-[10px] transition-opacity ${
                selected || hovered ? 'opacity-100' : 'opacity-0'
              }`}
              style={{ background: '#f3f4f6' }}
            >
              {h}px
            </span>
          </div>
        </div>
      );
    }

    case 'columns': {
      const cols = block.columns ?? [];
      return (
        <div style={{ padding: sectionPad }}>
          <div className="flex items-stretch">
            {cols.map((col, ci) => (
              <Column
                key={ci}
                parentBlock={block}
                colIndex={ci}
                subBlocks={col}
                variableGroups={variableGroups}
                dropHint={dropHint}
                readOnly={readOnly}
              />
            ))}
          </div>
        </div>
      );
    }

    default:
      return null;
  }
};

const Column: React.FC<{
  parentBlock: Block;
  colIndex: number;
  subBlocks: Block[];
  variableGroups: VariableGroup[];
  dropHint: BlockAddress | null;
  readOnly?: boolean;
}> = ({ parentBlock, colIndex, subBlocks, variableGroups, dropHint, readOnly }) => {
  const { setNodeRef, isOver } = useDroppable({
    id: `col:${parentBlock.id}:${colIndex}`,
    data: { kind: 'container', parentId: parentBlock.id, colIndex },
    disabled: readOnly,
  });

  const hintHere = dropHint && dropHint.parentId === parentBlock.id && dropHint.colIndex === colIndex;

  return (
    <div
      ref={setNodeRef}
      className={`min-w-0 flex-1 rounded-sm ${isOver ? 'bg-primary/5' : ''}`}
    >
      <SortableContext items={subBlocks.map((b) => b.id)} strategy={verticalListSortingStrategy}>
        {subBlocks.length === 0 ? (
          <div className={`m-2 flex min-h-16 items-center justify-center rounded border border-dashed text-xs ${isOver ? 'border-primary text-primary' : 'email-hint'}`}>
            Drop content here
          </div>
        ) : (
          subBlocks.map((sub, i) => (
            <React.Fragment key={sub.id}>
              {hintHere && dropHint!.index === i && <DropIndicator />}
              <CanvasBlock
                block={sub}
                parentId={parentBlock.id}
                colIndex={colIndex}
                index={i}
                listLength={subBlocks.length}
                variableGroups={variableGroups}
                dropHint={dropHint}
                readOnly={readOnly}
              />
            </React.Fragment>
          ))
        )}
        {hintHere && dropHint!.index === subBlocks.length && subBlocks.length > 0 && <DropIndicator />}
      </SortableContext>
    </div>
  );
};

export default CanvasBlock;
