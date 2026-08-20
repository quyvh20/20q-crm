import React, { useState } from 'react';
import { useDroppable } from '@dnd-kit/core';
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { LayoutTemplate, Plus, ShieldCheck } from 'lucide-react';
import { useBuilderStore } from './builderStore';
import { LAYOUT_PRESETS, type BlockAddress } from './blockUtils';
import { FONT_OPTIONS, PALETTE } from './blocks';
import { CanvasBlock, DropIndicator } from './CanvasBlock';
import { PALETTE_ICONS } from './EmailBuilder';
import type { VariableGroup } from './mergeScope';

interface Props {
  variableGroups: VariableGroup[];
  dropHint: BlockAddress | null;
  readOnly?: boolean;
}

/** BuilderCanvas is the WYSIWYG email surface: a fixed 600px white "email" frame
 *  (the width MJML compiles to) on a muted backdrop. Blocks render with the
 *  compiled email's typography and spacing; the compliance footer is shown but
 *  locked (it is compiler-owned and always appended server-side). Between-block
 *  gaps carry a hover "+" quick-insert so precise adding doesn't require a drag. */
export const BuilderCanvas: React.FC<Props> = ({ variableGroups, dropHint, readOnly }) => {
  const blocks = useBuilderStore((s) => s.blocks);
  const bodyBg = useBuilderStore((s) => s.bodyBg);
  const styles = useBuilderStore((s) => s.styles);
  const select = useBuilderStore((s) => s.select);
  const insertBlocks = useBuilderStore((s) => s.insertBlocks);

  // Which between-block gap has its quick-insert popover open (index = insert
  // position). Cleared on canvas click, insert, or toggling the same gap.
  const [openGap, setOpenGap] = useState<number | null>(null);

  const width = styles.width && styles.width >= 480 && styles.width <= 800 ? styles.width : 600;
  const fontStack = FONT_OPTIONS.find((f) => f.key === styles.fontFamily)?.stack ?? FONT_OPTIONS[0].stack;
  const lineHeight = styles.lineHeight && styles.lineHeight >= 1 && styles.lineHeight <= 2.5 ? styles.lineHeight : 1.5;
  const footerColor = styles.footerColor || '#6b7280';

  const { setNodeRef, isOver } = useDroppable({
    id: 'root',
    data: { kind: 'container', parentId: null, colIndex: 0 },
    disabled: readOnly,
  });

  const rootHint = dropHint && dropHint.parentId === null ? dropHint : null;

  return (
    <div
      // overflow-x-auto + fixed 600px frame: at narrow app widths the email
      // pans instead of shrinking — a squeezed frame would re-wrap every line
      // and quietly stop matching what MJML compiles.
      className="min-h-0 flex-1 overflow-y-auto overflow-x-auto bg-muted/50 px-6 py-8"
      onClick={() => { select(null); setOpenGap(null); }}
      data-testid="builder-canvas"
    >
      <div
        ref={setNodeRef}
        className="email-canvas mx-auto shrink-0 rounded-lg shadow-md ring-1 ring-black/5"
        // mj-body background — sections without their own bg sit on it, same as
        // the real render. Width/font/text color/link color/line-height mirror
        // the Styles panel exactly as buildMJML applies them.
        style={{
          background: bodyBg || '#ffffff',
          width,
          fontFamily: fontStack,
          lineHeight,
          color: styles.textColor || '#111827',
          ['--email-link-color' as string]: styles.linkColor || '#2563eb',
        } as React.CSSProperties}
      >
        <SortableContext items={blocks.map((b) => b.id)} strategy={verticalListSortingStrategy}>
          {blocks.length === 0 ? (
            <EmptyState isOver={isOver} readOnly={readOnly} onPick={(make) => insertBlocks(make(), { parentId: null, colIndex: 0, index: 0 })} />
          ) : (
            <>
              {blocks.map((b, i) => (
                <React.Fragment key={b.id}>
                  {rootHint && rootHint.index === i && <DropIndicator />}
                  <QuickInsertGap index={i} open={openGap === i} setOpen={setOpenGap} readOnly={readOnly} />
                  <CanvasBlock
                    block={b}
                    parentId={null}
                    colIndex={0}
                    index={i}
                    listLength={blocks.length}
                    variableGroups={variableGroups}
                    dropHint={dropHint}
                    readOnly={readOnly}
                  />
                </React.Fragment>
              ))}
              {rootHint && rootHint.index === blocks.length && blocks.length > 0 && <DropIndicator />}
              <QuickInsertGap index={blocks.length} open={openGap === blocks.length} setOpen={setOpenGap} readOnly={readOnly} />
            </>
          )}
        </SortableContext>

        {/* Compliance footer — always appended by the compiler; styleable via
            the Email tab (bg/color/extra text) but never removable */}
        <div className="group/footer relative" onClick={(e) => e.stopPropagation()} style={{ background: styles.footerBg || undefined }}>
          <div style={{ padding: '24px 0 0' }}>
            <div style={{ padding: '0 25px 12px' }}>
              <div style={{ borderTop: '1px solid #e5e7eb' }} />
            </div>
            {styles.footerText?.trim() && (
              <div
                style={{ padding: '10px 25px 0', textAlign: 'center', fontSize: '12px', color: footerColor, lineHeight: 1.5 }}
                // Styled like the compiled output; the backend sanitizes this
                // HTML on save — here it renders as plain text to stay safe.
              >
                {styles.footerText.replace(/<[^>]+>/g, ' ').trim()}
              </div>
            )}
            <div style={{ padding: '10px 25px 20px', textAlign: 'center', fontSize: '12px', color: footerColor, lineHeight: 1.5 }}>
              Your workspace name
              <br />
              Your postal address
              <br />
              <span style={{ color: footerColor, textDecoration: 'underline' }}>Unsubscribe</span>
            </div>
          </div>
          <div className="pointer-events-none absolute inset-0 flex items-center justify-center opacity-0 transition-opacity group-hover/footer:opacity-100">
            <span className="flex items-center gap-1.5 rounded-full border border-border bg-card px-2.5 py-1 text-[11px] font-medium text-muted-foreground shadow-sm">
              <ShieldCheck className="h-3.5 w-3.5" />
              Required unsubscribe footer — style it in the Email tab
            </span>
          </div>
        </div>
      </div>
    </div>
  );
};

/** EmptyState is the blank-document starting point: layout-preset starter cards
 *  (click to scaffold) instead of a bare "drag here" void. */
const EmptyState: React.FC<{
  isOver: boolean;
  readOnly?: boolean;
  onPick: (make: () => ReturnType<(typeof LAYOUT_PRESETS)[number]['make']>) => void;
}> = ({ isOver, readOnly, onPick }) => (
  <div
    className={`m-6 rounded-lg border-2 border-dashed p-6 ${isOver ? 'border-primary' : 'border-[#e5e7eb]'}`}
    onClick={(e) => e.stopPropagation()}
  >
    <p className="email-hint mb-1 text-center text-sm font-medium">Start your email</p>
    <p className="email-hint mb-4 text-center text-xs">
      Pick a starting layout, or drag blocks in from the left.
    </p>
    {!readOnly && (
      <div className="grid grid-cols-2 gap-2">
        {LAYOUT_PRESETS.map((p) => (
          <button
            key={p.key}
            type="button"
            onClick={() => onPick(p.make)}
            className="flex items-start gap-2 rounded-lg border border-[#e5e7eb] bg-white px-2.5 py-2 text-left transition-colors hover:border-[#2563eb]"
          >
            <LayoutTemplate className="mt-0.5 h-4 w-4 shrink-0 text-[#9ca3af]" />
            <span className="min-w-0">
              <span className="block text-xs font-medium text-[#111827]">{p.label}</span>
              <span className="block truncate text-[11px] text-[#6b7280]">{p.description}</span>
            </span>
          </button>
        ))}
      </div>
    )}
  </div>
);

/** QuickInsertGap is the industry-standard hover "+" between blocks: a zero-height
 *  zone that reveals an inserter, opening a compact block popover that inserts
 *  exactly at that position — no drag required. */
const QuickInsertGap: React.FC<{
  index: number;
  open: boolean;
  setOpen: (i: number | null) => void;
  readOnly?: boolean;
}> = ({ index, open, setOpen, readOnly }) => {
  const addBlock = useBuilderStore((s) => s.addBlock);
  if (readOnly) return null;
  return (
    <div className="relative z-20 h-0" onClick={(e) => e.stopPropagation()}>
      {/* The strip spans the full width for layout only and is click-through —
          a hit-testable band here would steal clicks (and hover chrome) from
          the top edge of every block. Only the centered target is interactive. */}
      <div className="pointer-events-none absolute inset-x-0 -top-3 flex h-6 items-center justify-center">
        <div className="group/gap pointer-events-auto flex h-6 w-28 items-center justify-center">
          <button
            type="button"
            title="Add block here"
            aria-label={`Add block at position ${index + 1}`}
            onClick={() => setOpen(open ? null : index)}
            className={`flex h-5 w-5 items-center justify-center rounded-full border border-primary/50 bg-card text-primary shadow-sm transition-opacity hover:bg-primary hover:text-primary-foreground ${
              open ? 'opacity-100' : 'opacity-0 focus-visible:opacity-100 group-hover/gap:opacity-100'
            }`}
          >
            <Plus className="h-3.5 w-3.5" />
          </button>
        </div>
      </div>
      {open && (
        <div className="absolute left-1/2 top-2 z-40 w-72 -translate-x-1/2 rounded-xl border border-border bg-popover p-2 text-popover-foreground shadow-2xl">
          <div className="grid grid-cols-4 gap-1">
            {PALETTE.map((m) => {
              const Icon = PALETTE_ICONS[m.icon] ?? PALETTE_ICONS.Type;
              return (
                <button
                  key={m.type}
                  type="button"
                  onClick={() => {
                    addBlock(m.type, { parentId: null, colIndex: 0, index });
                    setOpen(null);
                  }}
                  className="flex flex-col items-center gap-1 rounded-lg px-1 py-2 text-[10px] font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
                >
                  <Icon className="h-4 w-4" />
                  {m.label}
                </button>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
};

export default BuilderCanvas;
