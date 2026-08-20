import React, { useRef, useState } from 'react';
import { useSortable, SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { useDroppable } from '@dnd-kit/core';
import { CSS } from '@dnd-kit/utilities';
import { ChevronDown, ChevronUp, CirclePlay, CodeXml, Copy, Filter, GripVertical, Image as ImageIcon, Monitor, MoveHorizontal, RefreshCw, Share2, Smartphone, Trash2 } from 'lucide-react';
import { FONT_OPTIONS, type Block } from './blocks';
import { displayImageSrc } from '../assetsApi';
import { validColWidths, type BlockAddress } from './blockUtils';
import { useBuilderStore } from './builderStore';
import { InlineRichText } from './InlineRichText';
import type { VariableGroup } from './mergeScope';

// The canvas mirrors the compiled email's real metrics (compile.go + MJML
// defaults): every root block is its own <mj-section> (20px 0) and components
// carry MJML's default 10px 25px padding — so canvas spacing ≈ inbox spacing.
const SECTION_PAD = '20px 0';
const COMPONENT_PAD = '10px 25px';

/** Blocks with no text-editing surface are draggable by their whole body (the
 *  grip stays for keyboard users). Text-bearing blocks would fight the caret. */
const DRAG_BY_BODY: ReadonlySet<Block['type']> = new Set(['image', 'divider', 'spacer', 'social', 'video', 'menu', 'html']);

/** pointerListeners strips dnd-kit's keyboard activator from a listeners map —
 *  Enter/Space body-wide would swallow clicks on inner controls; keyboard drag
 *  stays available on the grip button. */
function pointerListeners(listeners: Record<string, unknown> | undefined) {
  if (!listeners) return {};
  const { onKeyDown: _kb, ...rest } = listeners;
  return rest;
}

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

  // Blocks without an editing surface can be dragged by their whole body —
  // grabbing an image or divider anywhere just works. Text-bearing blocks keep
  // the grip only (their body is a text cursor / edit surface).
  const dragByBody = !readOnly && DRAG_BY_BODY.has(block.type);

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
      className={`relative ${isDragging ? 'opacity-40' : ''} ${dragByBody ? 'cursor-grab active:cursor-grabbing' : ''}`}
      onClick={(e) => {
        e.stopPropagation();
        select(block.id);
      }}
      // Selection is also driven by focus so keyboard users reach the format
      // toolbar and inspector without a mouse.
      onFocusCapture={() => { if (!readOnly && !selected) select(block.id); }}
      onMouseOver={(e) => { e.stopPropagation(); setHovered(true); }}
      onMouseOut={(e) => { e.stopPropagation(); setHovered(false); }}
      // Whole-body drag for media/layout blocks. PointerSensor's 8px activation
      // distance keeps plain clicks selecting instead of dragging; the keyboard
      // activator stays on the grip button only (Enter/Space there must not be
      // swallowed body-wide).
      {...(dragByBody ? pointerListeners(listeners) : {})}
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

          {/* persistent targeting badges — a hidden-on-X, conditional or
              full-width block must never be a surprise at send time */}
          {(block.hide_mobile !== block.hide_desktop || block.cond || block.full_width || block.ref) && (
            <span className="absolute left-2 bottom-1 z-20 flex items-center gap-1">
              {block.hide_mobile !== block.hide_desktop && (
                <span
                  title={block.hide_mobile ? 'Shown on desktop only' : 'Shown on mobile only'}
                  className="flex items-center gap-0.5 rounded bg-card/90 px-1 py-px text-[9px] font-medium text-muted-foreground shadow-sm ring-1 ring-border"
                >
                  {block.hide_mobile ? <Monitor className="h-2.5 w-2.5" /> : <Smartphone className="h-2.5 w-2.5" />}
                  {block.hide_mobile ? 'Desktop only' : 'Mobile only'}
                </span>
              )}
              {block.cond && (
                <span
                  title={`Shown only if ${block.cond.field} ${block.cond.op}${block.cond.value ? ` "${block.cond.value}"` : ''}`}
                  className="flex items-center gap-0.5 rounded bg-card/90 px-1 py-px text-[9px] font-medium text-muted-foreground shadow-sm ring-1 ring-border"
                >
                  <Filter className="h-2.5 w-2.5" />
                  Conditional
                </span>
              )}
              {block.ref && (
                <span
                  title="Synced with the block library — updating it here can update every email that uses it"
                  className="flex items-center gap-0.5 rounded bg-card/90 px-1 py-px text-[9px] font-medium text-primary shadow-sm ring-1 ring-border"
                >
                  <RefreshCw className="h-2.5 w-2.5" />
                  Synced
                </span>
              )}
              {block.full_width && (
                <span
                  title="This section's background bleeds edge-to-edge in the inbox (wider than the content area)"
                  className="flex items-center gap-0.5 rounded bg-card/90 px-1 py-px text-[9px] font-medium text-muted-foreground shadow-sm ring-1 ring-border"
                >
                  <MoveHorizontal className="h-2.5 w-2.5" />
                  Full width
                </span>
              )}
            </span>
          )}

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
  const patchBlock = useBuilderStore((s) => s.patchBlock);
  const requestImagePicker = useBuilderStore((s) => s.requestImagePicker);
  const onAlign = readOnly ? undefined : (align: 'left' | 'center' | 'right') => patchBlock(block.id, { align });

  // A per-block font override must SHOW on the canvas — an inspector control
  // whose effect is invisible until the preview reads as broken.
  const fontStack = block.font ? FONT_OPTIONS.find((f) => f.key === block.font)?.stack : undefined;

  const padY = block.pad_y != null ? Math.min(Math.max(block.pad_y, 0), 80) : null;
  const padX = block.pad_x != null ? Math.min(Math.max(block.pad_x, 0), 60) : null;
  const sectionPad = nested ? undefined : padY != null || padX != null ? `${padY ?? 20}px ${padX ?? 0}px` : SECTION_PAD;
  // Root blocks compile to their own mj-section, so bg paints the full section
  // area (padding included) — exactly what the recipient sees. backgroundColor
  // (not the background shorthand) so a bg_url image can compose with it.
  const sectionStyle: React.CSSProperties = {
    ...(nested
      ? {}
      : {
          padding: sectionPad,
          backgroundColor: block.bg || undefined,
          ...(block.bg_url && /^https?:\/\//i.test(block.bg_url)
            ? {
                // cover/center/no-repeat mirror the compiler's explicit bg attrs.
                backgroundImage: `url(${displayImageSrc(block.bg_url)})`,
                backgroundSize: 'cover',
                backgroundRepeat: 'no-repeat',
                backgroundPosition: 'center center',
              }
            : {}),
        }),
    ...(fontStack ? { fontFamily: fontStack } : {}),
  };

  switch (block.type) {
    case 'text':
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD }}>
            <InlineRichText
              html={block.text ?? ''}
              variableGroups={variableGroups}
              onChange={onText}
              active={selected}
              variant="text"
              align={block.align}
              onAlign={onAlign}
              readOnly={readOnly}
              overrideSize={block.size}
              overrideColor={block.color}
            />
          </div>
        </div>
      );

    case 'heading':
      return (
        <div style={sectionStyle}>
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
              onAlign={onAlign}
              readOnly={readOnly}
              overrideSize={block.size}
              overrideColor={block.color}
            />
          </div>
        </div>
      );

    case 'button': {
      const btnStyle: React.CSSProperties = {
        background: block.btn_bg || undefined,
        color: block.btn_color || undefined,
        borderRadius: block.radius ? `${block.radius}px` : undefined,
        fontSize: block.size && block.size >= 10 && block.size <= 40 ? `${block.size}px` : undefined,
      };
      const btnAlign = block.align === 'left' || block.align === 'right' ? block.align : 'center';
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD, textAlign: btnAlign }}>
            <span className="email-button-preview" style={btnStyle}>{block.label || 'Button'}</span>
          </div>
        </div>
      );
    }

    case 'image': {
      const imgAlign = block.align === 'left' || block.align === 'right' ? block.align : 'center';
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD, textAlign: imgAlign }}>
            {block.src ? (
              <img
                src={displayImageSrc(block.src)}
                alt={block.alt ?? ''}
                style={{
                  maxWidth: '100%',
                  display: 'inline-block',
                  width: block.width_px ? `${block.width_px}px` : undefined,
                  borderRadius: block.radius ? `${block.radius}px` : undefined,
                }}
                draggable={false}
              />
            ) : (
              // email-hint: fixed grays — theme tokens flip in dark mode and
              // wash out on this always-white surface. Clicking goes straight
              // to the image library — no inspector round-trip.
              <button
                type="button"
                className="email-hint flex w-full flex-col items-center gap-1 rounded border border-dashed py-8"
                onClick={(e) => { e.stopPropagation(); if (!readOnly) requestImagePicker(block.id); }}
              >
                <ImageIcon className="h-6 w-6" />
                <span className="text-xs">Click to choose an image</span>
              </button>
            )}
          </div>
        </div>
      );
    }

    case 'divider':
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD }}>
            <div style={{ borderTop: `${block.thickness && block.thickness >= 1 && block.thickness <= 10 ? block.thickness : 1}px solid ${block.color || '#e5e7eb'}` }} />
          </div>
        </div>
      );

    case 'social': {
      const size = block.size && block.size >= 12 && block.size <= 48 ? block.size : 24;
      const align = block.align === 'left' || block.align === 'right' ? block.align : 'center';
      const configured = (block.social ?? []).filter((s) => s.network);
      const anyHref = configured.some((s) => s.href.trim() !== '');
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD, textAlign: align }}>
            {configured.length === 0 ? (
              <div className="email-hint flex flex-col items-center gap-1 rounded border border-dashed py-6">
                <Share2 className="h-5 w-5" />
                <span className="text-xs">Add social links in the inspector</span>
              </div>
            ) : (
              <>
                {configured.map((s, i) => (
                  // The exact icon assets mj-social ships — true WYSIWYG.
                  <img
                    key={i}
                    src={`https://www.mailjet.com/images/theme/v1/icons/ico-social/${s.network}.png`}
                    alt={s.network}
                    style={{ width: size, height: size, display: 'inline-block', margin: '0 4px', opacity: s.href.trim() ? 1 : 0.35 }}
                    draggable={false}
                  />
                ))}
                {!anyHref && <div className="email-hint mt-1 text-[11px]">Icons without a link won’t be sent — add URLs in the inspector</div>}
              </>
            )}
          </div>
        </div>
      );
    }

    case 'video':
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD, textAlign: 'center' }}>
            {block.src ? (
              <div style={{ position: 'relative', display: 'inline-block', maxWidth: '100%' }}>
                <img
                  src={displayImageSrc(block.src)}
                  alt={block.alt ?? ''}
                  style={{ maxWidth: '100%', display: 'block', width: block.width_px ? `${block.width_px}px` : undefined, borderRadius: block.radius ? `${block.radius}px` : undefined }}
                  draggable={false}
                />
                <CirclePlay
                  className="absolute left-1/2 top-1/2 h-12 w-12 -translate-x-1/2 -translate-y-1/2"
                  style={{ color: '#ffffff', filter: 'drop-shadow(0 1px 4px rgba(0,0,0,0.5))' }}
                />
              </div>
            ) : (
              <button
                type="button"
                className="email-hint flex w-full flex-col items-center gap-1 rounded border border-dashed py-8"
                onClick={(e) => { e.stopPropagation(); if (!readOnly) requestImagePicker(block.id); }}
              >
                <CirclePlay className="h-6 w-6" />
                <span className="text-xs">Click to choose a thumbnail — set the video link in the inspector</span>
              </button>
            )}
          </div>
          <div style={{ padding: COMPONENT_PAD, paddingTop: 0, textAlign: 'center' }}>
            <span className="email-button-preview">{block.label?.trim() || '▶ Watch video'}</span>
          </div>
        </div>
      );

    case 'quote':
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD }}>
            <div style={{ borderLeft: `3px solid ${block.color || '#d1d5db'}`, padding: '4px 0 4px 16px', fontStyle: 'italic' }}>
              <InlineRichText
                html={block.text ?? ''}
                variableGroups={variableGroups}
                onChange={onText}
                active={selected}
                variant="text"
                align={block.align}
                onAlign={onAlign}
                readOnly={readOnly}
              />
            </div>
            {block.label?.trim() && (
              <p style={{ margin: '8px 0 0', color: '#6b7280', fontSize: 13 }}>— {block.label}</p>
            )}
          </div>
        </div>
      );

    case 'menu': {
      const items = (block.items ?? []).filter((it) => it.label.trim() !== '');
      return (
        <div style={sectionStyle}>
          <div style={{ padding: COMPONENT_PAD, textAlign: 'center' }}>
            {items.length === 0 ? (
              <div className="email-hint rounded border border-dashed py-4 text-xs">Add menu links in the inspector</div>
            ) : (
              items.map((it, i) => (
                <React.Fragment key={i}>
                  {i > 0 && <span style={{ margin: '0 8px', color: '#9ca3af' }}>•</span>}
                  <span style={{ color: 'var(--email-link-color, #2563eb)', fontWeight: 500 }}>{it.label}</span>
                </React.Fragment>
              ))
            )}
          </div>
        </div>
      );
    }

    case 'spacer': {
      const h = block.height && block.height > 0 ? block.height : 20;
      return (
        <div style={sectionStyle}>
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

    case 'html':
      return (
        <div style={sectionStyle}>
          {block.text?.trim() ? (
            <RawHTMLPreview html={block.text} />
          ) : (
            <div className="email-hint m-2 flex flex-col items-center gap-1 rounded border border-dashed py-6">
              <CodeXml className="h-5 w-5" />
              <span className="text-xs">Write your HTML in the inspector</span>
            </div>
          )}
        </div>
      );

    case 'product': {
      const align = block.align ?? 'left';
      const showButton = (block.href ?? '').trim() !== '' || (block.label ?? '').trim() !== '';
      const btnStyle: React.CSSProperties = {
        background: block.btn_bg || undefined,
        color: block.btn_color || undefined,
        borderRadius: block.radius ? `${block.radius}px` : undefined,
      };
      return (
        <div style={sectionStyle}>
          {block.src ? (
            <div style={{ padding: COMPONENT_PAD, textAlign: 'center' }}>
              <img
                src={displayImageSrc(block.src)}
                alt={block.alt ?? ''}
                style={{ maxWidth: '100%', display: 'inline-block', width: block.width_px ? `${block.width_px}px` : undefined, borderRadius: block.radius ? `${block.radius}px` : undefined }}
                draggable={false}
              />
            </div>
          ) : (
            <div style={{ padding: COMPONENT_PAD }}>
              <button
                type="button"
                className="email-hint flex w-full flex-col items-center gap-1 rounded border border-dashed py-6"
                onClick={(e) => { e.stopPropagation(); if (!readOnly) requestImagePicker(block.id); }}
              >
                <ImageIcon className="h-5 w-5" />
                <span className="text-xs">Click to choose an image — or fill the card from a CRM record in the inspector</span>
              </button>
            </div>
          )}
          <div style={{ padding: '10px 25px 0', textAlign: align, fontSize: 18, fontWeight: 700, color: block.color || undefined }}>
            {block.title?.trim() || 'Product name'}
          </div>
          <div style={{ padding: '6px 25px 0' }}>
            <InlineRichText
              html={block.text ?? ''}
              variableGroups={variableGroups}
              onChange={onText}
              active={selected}
              variant="text"
              align={block.align}
              onAlign={onAlign}
              readOnly={readOnly}
            />
          </div>
          {block.price?.trim() && (
            <div style={{ padding: '6px 25px 0', textAlign: align, fontSize: 16, fontWeight: 700 }}>{block.price}</div>
          )}
          {showButton && (
            <div style={{ padding: COMPONENT_PAD, textAlign: block.align === 'left' || block.align === 'right' ? block.align : 'center' }}>
              <span className="email-button-preview" style={btnStyle}>{block.label?.trim() || 'View product'}</span>
            </div>
          )}
        </div>
      );
    }

    case 'columns': {
      const cols = block.columns ?? [];
      const widths = validColWidths(block);
      return (
        <div style={sectionStyle}>
          <div className="flex items-stretch">
            {cols.map((col, ci) => (
              <Column
                key={ci}
                parentBlock={block}
                colIndex={ci}
                subBlocks={col}
                widthPct={widths ? widths[ci] : undefined}
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

/** RawHTMLPreview renders an html block's snippet in a script-dead iframe.
 *  sandbox has allow-same-origin (so we can measure content height for
 *  autosizing) but NOT allow-scripts — author <script>/handlers are inert in
 *  the editor regardless of what the backend sanitizer will strip on save.
 *  pointer-events:none keeps clicks selecting the block. */
const RawHTMLPreview: React.FC<{ html: string }> = ({ html }) => {
  const ref = useRef<HTMLIFrameElement>(null);
  const [height, setHeight] = useState(48);
  const doc = `<!doctype html><html><head><style>body{margin:0;font-family:Arial,Helvetica,sans-serif;color:#111827;font-size:15px;line-height:1.5}</style></head><body>${html}</body></html>`;
  return (
    <iframe
      ref={ref}
      title="Custom HTML preview"
      sandbox="allow-same-origin"
      srcDoc={doc}
      style={{ width: '100%', height, border: 0, pointerEvents: 'none', display: 'block' }}
      onLoad={() => {
        const h = ref.current?.contentDocument?.body?.scrollHeight;
        if (h && Math.abs(h - height) > 2) setHeight(Math.min(Math.max(h, 24), 1200));
      }}
    />
  );
};

const Column: React.FC<{
  parentBlock: Block;
  colIndex: number;
  subBlocks: Block[];
  widthPct?: number;
  variableGroups: VariableGroup[];
  dropHint: BlockAddress | null;
  readOnly?: boolean;
}> = ({ parentBlock, colIndex, subBlocks, widthPct, variableGroups, dropHint, readOnly }) => {
  const { setNodeRef, isOver } = useDroppable({
    id: `col:${parentBlock.id}:${colIndex}`,
    data: { kind: 'container', parentId: parentBlock.id, colIndex },
    disabled: readOnly,
  });

  const hintHere = dropHint && dropHint.parentId === parentBlock.id && dropHint.colIndex === colIndex;

  return (
    <div
      ref={setNodeRef}
      className={`min-w-0 rounded-sm ${widthPct == null ? 'flex-1' : ''} ${isOver ? 'bg-primary/5' : ''}`}
      // Ratio columns mirror the compiler's <mj-column width="N%">.
      style={widthPct != null ? { width: `${widthPct}%`, flex: 'none' } : undefined}
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
