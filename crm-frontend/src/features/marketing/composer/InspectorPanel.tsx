import React, { useEffect, useRef, useState } from 'react';
import {
  AlertCircle, AlignCenter, AlignLeft, AlignRight, BookmarkPlus, CheckCircle2, Clock, Copy, Filter, Images, Monitor, MousePointerClick, RefreshCw, Smartphone, Sparkles, Trash2, X,
} from 'lucide-react';
import { Button, Input, Select, Textarea } from '@/components/ui';
import { COND_OPS, FONT_OPTIONS, SOCIAL_NETWORKS, type Block, type BlockCondition, type LinkItem, type SocialLink } from './blocks';
import { ImagePicker } from './ImagePicker';
import Modal from '../../../components/common/Modal';
import { useCreateSavedBlock, useSavedBlocks, useSavedBlockUsage, useUpdateSavedBlockContent } from '../savedBlocksQueries';
import { useConfirm } from '../../../components/common/ConfirmDialog';
import { useToast } from '@/lib/useToast';
import { RecordFillPicker } from './RecordFillPicker';
import { useBuilderStore } from './builderStore';
import { MergeTagMenu } from './MergeTagMenu';
import { token } from './mergeTagHtml';
import { SELECTABLE_SCOPES, type VariableGroup } from './mergeScope';
import { findBlock } from './blockUtils';
import type { PreviewResult } from '../contentApi';

// ok: true = satisfied, false = failing, 'pending' = unknown until a preview lands.
export interface Check {
  label: string;
  ok: boolean | 'pending';
}

export interface InspectorMeta {
  preview: PreviewResult | null;
  previewErr: boolean;
  saveErrors: string[];
  checklist: Check[];
  /** Opens the AI copilot in rewrite mode for the selected block. */
  onRewriteWithAI?: () => void;
}

type Tab = 'block' | 'email' | 'review';

/** Block types whose WORDS the copilot can rewrite. MUST stay a subset of the
 *  backend's aiBlockTypes (content_ai.go): offering a rewrite for a type the
 *  copilot cannot emit — product, for one — is an action guaranteed to fail.
 *  Blocks with no authored copy (divider, spacer, columns, image) are excluded
 *  for the same reason. */
const AI_REWRITABLE: ReadonlySet<string> = new Set(['text', 'heading', 'button', 'quote']);

/** InspectorPanel is the right rail: Block (settings for the selected block),
 *  Email (subject/preheader/scope), Review (pre-send checklist). Panels stay
 *  mounted and toggle with `hidden` so input state survives tab switches (the
 *  BuilderSidePanel idiom). */
export const InspectorPanel: React.FC<{ variableGroups: VariableGroup[]; meta: InspectorMeta }> = ({ variableGroups, meta }) => {
  const selectedId = useBuilderStore((s) => s.selectedId);
  const [tab, setTab] = useState<Tab>('email');

  // Picking a block on the canvas is an intent to edit it — jump to its settings.
  useEffect(() => {
    if (selectedId) setTab('block');
  }, [selectedId]);

  const failing = meta.checklist.filter((c) => c.ok === false).length + (meta.saveErrors.length > 0 ? 1 : 0);

  // Announcing role=tablist obliges the ARIA tabs keyboard pattern: arrow keys
  // move between tabs (roving activation).
  const TABS: Tab[] = ['block', 'email', 'review'];
  const onTablistKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
    e.preventDefault();
    const cur = TABS.indexOf(tab);
    const next = TABS[(cur + (e.key === 'ArrowRight' ? 1 : TABS.length - 1)) % TABS.length];
    setTab(next);
    document.getElementById(`tab-${next}`)?.focus();
  };

  return (
    <aside className="flex w-[320px] shrink-0 flex-col border-l border-border bg-card" aria-label="Inspector">
      <div role="tablist" aria-label="Inspector sections" className="flex border-b border-border" onKeyDown={onTablistKeyDown}>
        <TabButton id="block" active={tab === 'block'} onClick={() => setTab('block')}>Block</TabButton>
        <TabButton id="email" active={tab === 'email'} onClick={() => setTab('email')}>Email</TabButton>
        <TabButton id="review" active={tab === 'review'} onClick={() => setTab('review')}>
          Review
          {failing > 0 && (
            <span className="ml-1.5 rounded-full bg-amber-500/15 px-1.5 text-[10px] font-semibold text-amber-600 dark:text-amber-400">{failing}</span>
          )}
        </TabButton>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div role="tabpanel" id="panel-block" aria-labelledby="tab-block" hidden={tab !== 'block'} className="p-4">
          <BlockTab variableGroups={variableGroups} onRewriteWithAI={meta.onRewriteWithAI} />
        </div>
        <div role="tabpanel" id="panel-email" aria-labelledby="tab-email" hidden={tab !== 'email'} className="p-4">
          <EmailTab variableGroups={variableGroups} />
        </div>
        <div role="tabpanel" id="panel-review" aria-labelledby="tab-review" hidden={tab !== 'review'} className="p-4">
          <ReviewTab meta={meta} />
        </div>
      </div>
    </aside>
  );
};

const TabButton: React.FC<{ id: string; active: boolean; onClick: () => void; children: React.ReactNode }> = ({ id, active, onClick, children }) => (
  <button
    type="button"
    role="tab"
    id={`tab-${id}`}
    aria-selected={active}
    aria-controls={`panel-${id}`}
    tabIndex={active ? 0 : -1}
    onClick={onClick}
    className={`flex flex-1 items-center justify-center gap-0.5 border-b-2 px-2 py-2.5 text-sm font-medium transition-colors ${
      active ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'
    }`}
  >
    {children}
  </button>
);

// ---------------------------------------------------------------------------
// Block tab
// ---------------------------------------------------------------------------

// Column-ratio presets (percentages mirror <mj-column width>). undefined =
// equal widths (the wire field is omitted).
const RATIOS_2: { label: string; w?: number[] }[] = [
  { label: '50 / 50' },
  { label: '33 / 67', w: [33, 67] },
  { label: '67 / 33', w: [67, 33] },
];
const RATIOS_3: { label: string; w?: number[] }[] = [
  { label: 'Equal' },
  { label: '25 / 50 / 25', w: [25, 50, 25] },
  { label: '50 / 25 / 25', w: [50, 25, 25] },
  { label: '25 / 25 / 50', w: [25, 25, 50] },
];

const BlockTab: React.FC<{ variableGroups: VariableGroup[]; onRewriteWithAI?: () => void }> = ({ variableGroups, onRewriteWithAI }) => {
  const blocks = useBuilderStore((s) => s.blocks);
  const selectedId = useBuilderStore((s) => s.selectedId);
  const patchBlock = useBuilderStore((s) => s.patchBlock);
  const removeBlock = useBuilderStore((s) => s.removeBlock);
  const duplicateBlock = useBuilderStore((s) => s.duplicateBlock);
  const setColumnCount = useBuilderStore((s) => s.setColumnCount);
  const setColumnWidths = useBuilderStore((s) => s.setColumnWidths);
  const pickerFor = useBuilderStore((s) => s.pickerFor);
  const clearPickerRequest = useBuilderStore((s) => s.clearPickerRequest);
  const htmlRef = useRef<HTMLTextAreaElement>(null);
  // Which field the ImagePicker fills: the block's media src, or its section
  // background image. A canvas placeholder click asks for the library through the
  // store, so "open" is DERIVED from that request rather than synced into local
  // state by an effect (setState in an effect cascades a render for nothing).
  const [localPicker, setLocalPicker] = useState<{ open: boolean; target: 'src' | 'bg' }>({ open: false, target: 'src' });
  const pickerRequested = pickerFor != null && pickerFor === selectedId;
  const pickerOpen = localPicker.open || pickerRequested;
  const pickerTarget: 'src' | 'bg' = localPicker.open ? localPicker.target : 'src';
  const openPicker = (target: 'src' | 'bg') => setLocalPicker({ open: true, target });
  const closePicker = () => {
    setLocalPicker({ open: false, target: 'src' });
    // Clear the store request too, or the derived state reopens immediately.
    if (pickerRequested) clearPickerRequest();
  };

  const found = selectedId ? findBlock(blocks, selectedId) : null;
  if (!found) {
    return (
      <div className="flex flex-col items-center gap-2 py-10 text-center text-muted-foreground">
        <MousePointerClick className="h-6 w-6" />
        <p className="text-sm">Select a block on the canvas,<br />or drag one in from the left.</p>
      </div>
    );
  }

  const b = found.block;
  const patch = (p: Partial<Block>, key?: string) => patchBlock(b.id, p, key);

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{b.type} settings</p>
        <div className="flex gap-1">
          {onRewriteWithAI && AI_REWRITABLE.has(b.type) && (
            <button type="button" title="Rewrite with AI" aria-label="Rewrite this block with AI" onClick={onRewriteWithAI}
              className="rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"><Sparkles className="h-4 w-4" /></button>
          )}
          <SaveToLibrary block={b} />
          <button type="button" title="Duplicate block" aria-label="Duplicate block" onClick={() => duplicateBlock(b.id)}
            className="rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"><Copy className="h-4 w-4" /></button>
          <button type="button" title="Delete block" aria-label="Delete block" onClick={() => removeBlock(b.id)}
            className="rounded p-1.5 text-destructive hover:bg-destructive/10"><Trash2 className="h-4 w-4" /></button>
        </div>
      </div>

      {b.type === 'text' && (
        <>
          <Field label="Alignment">
            <AlignPicker value={b.align} onChange={(align) => patch({ align })} />
          </Field>
          <Field label="Font size (px, blank = default 15)">
            <NumField value={b.size} min={10} max={60} onChange={(size) => patch({ size })} />
          </Field>
          <Field label="Text color">
            <ColorField label="Text color" value={b.color} onChange={(color) => patch({ color })} />
          </Field>
          <Field label="Font (blank = email font)">
            <FontField value={b.font} onChange={(font) => patch({ font })} />
          </Field>
        </>
      )}

      {b.type === 'heading' && (
        <>
          <Field label="Level">
            <Select value={String(b.level ?? 2)} onChange={(e) => patch({ level: Number(e.target.value) })}>
              <option value="1">Large (24px)</option>
              <option value="2">Medium (20px)</option>
              <option value="3">Small (17px)</option>
            </Select>
          </Field>
          <Field label="Custom size (px, blank = level default)">
            <NumField value={b.size} min={10} max={60} onChange={(size) => patch({ size })} />
          </Field>
          <Field label="Alignment">
            <AlignPicker value={b.align} onChange={(align) => patch({ align })} />
          </Field>
          <Field label="Text color">
            <ColorField label="Text color" value={b.color} onChange={(color) => patch({ color })} />
          </Field>
          <Field label="Font (blank = email font)">
            <FontField value={b.font} onChange={(font) => patch({ font })} />
          </Field>
          {found.parentId && !b.size && (
            <p className="text-[11px] text-muted-foreground">Inside columns, headings render at a fixed 18px unless you set a custom size.</p>
          )}
        </>
      )}

      {b.type === 'button' && (
        <>
          <Field label="Label">
            <Input value={b.label ?? ''} onChange={(e) => patch({ label: e.target.value }, `label:${b.id}`)} placeholder="Shop now" />
          </Field>
          <Field label="Link URL">
            <Input value={b.href ?? ''} onChange={(e) => patch({ href: e.target.value }, `href:${b.id}`)} placeholder="https://…" />
          </Field>
          <UrlHint value={b.href} />
          <Field label="Alignment">
            <AlignPicker value={b.align ?? 'center'} onChange={(align) => patch({ align })} />
          </Field>
          <Field label="Button color">
            <ColorField label="Button color" value={b.btn_bg} onChange={(btn_bg) => patch({ btn_bg })} />
          </Field>
          <Field label="Button text color">
            <ColorField label="Button text color" value={b.btn_color} onChange={(btn_color) => patch({ btn_color })} />
          </Field>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Corner radius (px)">
              <NumField value={b.radius} min={0} max={40} onChange={(radius) => patch({ radius })} />
            </Field>
            <Field label="Font size (px)">
              <NumField value={b.size} min={10} max={40} onChange={(size) => patch({ size })} />
            </Field>
          </div>
          <Field label="Font (blank = email font)">
            <FontField value={b.font} onChange={(font) => patch({ font })} />
          </Field>
        </>
      )}

      {b.type === 'social' && (
        <>
          <Field label="Alignment">
            <AlignPicker value={b.align ?? 'center'} onChange={(align) => patch({ align })} />
          </Field>
          <Field label="Icon size (px, blank = 24)">
            <NumField value={b.size} min={12} max={48} onChange={(size) => patch({ size })} />
          </Field>
          <SocialEditor value={b.social ?? []} onChange={(social) => patch({ social })} />
        </>
      )}

      {b.type === 'video' && (
        <>
          <Field label="Thumbnail image">
            <div className="flex gap-1.5">
              <Button variant="outline" size="sm" className="shrink-0" onClick={() => openPicker('src')}>
                <Images className="h-4 w-4" /> Library
              </Button>
              <Input value={b.src ?? ''} onChange={(e) => patch({ src: e.target.value }, `src:${b.id}`)} placeholder="or paste a URL…" />
            </div>
          </Field>
          <UrlHint value={b.src} />
          <Field label="Video link">
            <Input value={b.href ?? ''} onChange={(e) => patch({ href: e.target.value }, `href:${b.id}`)} placeholder="https://youtube.com/…" />
          </Field>
          <UrlHint value={b.href} />
          <Field label="Alt text">
            <Input value={b.alt ?? ''} onChange={(e) => patch({ alt: e.target.value }, `alt:${b.id}`)} placeholder="Describe the video" />
          </Field>
          {!b.alt?.trim() && (
            <p className="text-[11px] text-amber-600 dark:text-amber-400">Alt text helps accessibility and deliverability.</p>
          )}
          <Field label="Button label">
            <Input value={b.label ?? ''} onChange={(e) => patch({ label: e.target.value }, `label:${b.id}`)} placeholder="▶ Watch video" />
          </Field>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Width (px)">
              <NumField value={b.width_px} min={40} max={800} onChange={(width_px) => patch({ width_px })} />
            </Field>
            <Field label="Corner radius (px)">
              <NumField value={b.radius} min={0} max={100} onChange={(radius) => patch({ radius })} />
            </Field>
          </div>
          <p className="text-[11px] text-muted-foreground">Email clients can’t play video inline — this sends a linked thumbnail plus a watch button.</p>
        </>
      )}

      {b.type === 'quote' && (
        <>
          <Field label="Attribution">
            <Input value={b.label ?? ''} onChange={(e) => patch({ label: e.target.value }, `label:${b.id}`)} placeholder="Jane Doe, Acme Inc." />
          </Field>
          <Field label="Accent color">
            <ColorField label="Accent color" value={b.color} onChange={(color) => patch({ color })} />
          </Field>
          <Field label="Font (blank = email font)">
            <FontField value={b.font} onChange={(font) => patch({ font })} />
          </Field>
          <p className="text-[11px] text-muted-foreground">Edit the quote text directly on the canvas.</p>
        </>
      )}

      {b.type === 'menu' && (
        <MenuEditor value={b.items ?? []} onChange={(items) => patch({ items })} />
      )}

      {b.type === 'product' && (
        <>
          <RecordFillPicker onFill={(p) => patch(p)} />
          <Field label="Title">
            <Input value={b.title ?? ''} onChange={(e) => patch({ title: e.target.value }, `title:${b.id}`)} placeholder="Product name" />
          </Field>
          <Field label="Price (free text)">
            <Input value={b.price ?? ''} onChange={(e) => patch({ price: e.target.value }, `price:${b.id}`)} placeholder="$49 / month" />
          </Field>
          <Field label="Image">
            <div className="flex gap-1.5">
              <Button variant="outline" size="sm" className="shrink-0" onClick={() => openPicker('src')}>
                <Images className="h-4 w-4" /> Library
              </Button>
              <Input value={b.src ?? ''} onChange={(e) => patch({ src: e.target.value }, `src:${b.id}`)} placeholder="or paste a URL…" />
            </div>
          </Field>
          <UrlHint value={b.src} />
          <Field label="Button label">
            <Input value={b.label ?? ''} onChange={(e) => patch({ label: e.target.value }, `label:${b.id}`)} placeholder="View product" />
          </Field>
          <Field label="Button link">
            <Input value={b.href ?? ''} onChange={(e) => patch({ href: e.target.value }, `href:${b.id}`)} placeholder="https://…" />
          </Field>
          <UrlHint value={b.href} />
          <Field label="Alignment">
            <AlignPicker value={b.align} onChange={(align) => patch({ align })} />
          </Field>
          <Field label="Title color">
            <ColorField label="Title color" value={b.color} onChange={(color) => patch({ color })} />
          </Field>
          <Field label="Button color">
            <ColorField label="Button color" value={b.btn_bg} onChange={(btn_bg) => patch({ btn_bg })} />
          </Field>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Image width (px)">
              <NumField value={b.width_px} min={40} max={800} onChange={(width_px) => patch({ width_px })} />
            </Field>
            <Field label="Corner radius (px)">
              <NumField value={b.radius} min={0} max={100} onChange={(radius) => patch({ radius })} />
            </Field>
          </div>
          <p className="text-[11px] text-muted-foreground">Edit the description directly on the canvas.</p>
        </>
      )}

      {b.type === 'image' && (
        <>
          <Field label="Image">
            <div className="flex gap-1.5">
              <Button variant="outline" size="sm" className="shrink-0" onClick={() => openPicker('src')}>
                <Images className="h-4 w-4" /> Library
              </Button>
              <Input value={b.src ?? ''} onChange={(e) => patch({ src: e.target.value }, `src:${b.id}`)} placeholder="or paste a URL…" />
            </div>
          </Field>
          <UrlHint value={b.src} />
          <Field label="Alt text">
            <Input value={b.alt ?? ''} onChange={(e) => patch({ alt: e.target.value }, `alt:${b.id}`)} placeholder="Describe the image" />
          </Field>
          {!b.alt?.trim() && (
            <p className="text-[11px] text-amber-600 dark:text-amber-400">Alt text helps accessibility and deliverability.</p>
          )}
          <Field label="Link (optional)">
            <Input value={b.href ?? ''} onChange={(e) => patch({ href: e.target.value }, `href:${b.id}`)} placeholder="https://…" />
          </Field>
          <Field label="Alignment">
            <AlignPicker value={b.align ?? 'center'} onChange={(align) => patch({ align })} />
          </Field>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Width (px, blank = full)">
              <NumField value={b.width_px} min={40} max={800} onChange={(width_px) => patch({ width_px })} />
            </Field>
            <Field label="Corner radius (px)">
              <NumField value={b.radius} min={0} max={100} onChange={(radius) => patch({ radius })} />
            </Field>
          </div>
        </>
      )}

      {b.type === 'divider' && (
        <>
          <Field label="Line color">
            <ColorField label="Line color" value={b.color} onChange={(color) => patch({ color })} />
          </Field>
          <Field label="Thickness (px, blank = 1)">
            <NumField value={b.thickness} min={1} max={10} onChange={(thickness) => patch({ thickness })} />
          </Field>
        </>
      )}

      {b.type === 'html' && (
        <>
          <div>
            <div className="mb-1 flex items-center justify-between">
              <span className="text-[11px] font-medium text-muted-foreground">HTML</span>
              <MergeTagMenu
                variableGroups={variableGroups}
                onInsert={(p, f) => {
                  // Insert at the caret when we can, else append.
                  const ta = htmlRef.current;
                  const tok = token(p, f);
                  const cur = b.text ?? '';
                  if (ta && ta.selectionStart != null) {
                    const s = ta.selectionStart;
                    const e = ta.selectionEnd ?? s;
                    patch({ text: cur.slice(0, s) + tok + cur.slice(e) });
                  } else {
                    patch({ text: cur + tok });
                  }
                }}
              />
            </div>
            <Textarea
              ref={htmlRef}
              value={b.text ?? ''}
              onChange={(e) => patch({ text: e.target.value }, `html:${b.id}`)}
              rows={12}
              spellCheck={false}
              className="font-mono text-xs leading-relaxed"
              aria-label="Custom HTML"
              // The starter markup is a HINT, never content — an untouched html
              // block ships nothing.
              placeholder={'<table role="presentation" width="100%" cellpadding="0" cellspacing="0">\n  <tr><td align="center" style="padding:16px 25px;">\n    <!-- Your custom HTML -->\n  </td></tr>\n</table>'}
            />
          </div>
          <p className="text-[11px] text-muted-foreground">
            Tables, inline styles and images are kept; scripts and anything unsafe are stripped when you save.
            Send yourself a test — custom HTML renders as-is in the inbox.
          </p>
        </>
      )}

      {b.type === 'spacer' && (
        <Field label={`Height — ${b.height ?? 24}px`}>
          <div className="flex items-center gap-3">
            <input
              type="range"
              min={4}
              max={200}
              step={4}
              value={b.height ?? 24}
              onChange={(e) => patch({ height: Number(e.target.value) }, `height:${b.id}`)}
              className="flex-1 accent-[hsl(var(--primary))]"
              aria-label="Spacer height"
            />
            <div className="w-20">
              <NumField value={b.height ?? 24} min={4} max={200} onChange={(height) => patch({ height }, `height:${b.id}`)} />
            </div>
          </div>
        </Field>
      )}

      {b.type === 'columns' && (
        <>
          <Field label="Columns">
            <div className="flex gap-2">
              {[2, 3, 4].map((n) => (
                <button
                  key={n}
                  type="button"
                  onClick={() => setColumnCount(b.id, n)}
                  className={`flex-1 rounded-lg border px-3 py-2 text-sm font-medium transition-colors ${
                    (b.columns?.length ?? 2) === n
                      ? 'border-primary bg-primary/10 text-primary'
                      : 'border-border text-muted-foreground hover:border-ring hover:text-foreground'
                  }`}
                >
                  {n}
                </button>
              ))}
            </div>
          </Field>
          {(b.columns?.length ?? 2) <= 3 && (
            <Field label="Column widths">
              <div className="flex gap-1.5">
                {((b.columns?.length ?? 2) === 2 ? RATIOS_2 : RATIOS_3).map((r) => {
                  const active = JSON.stringify(b.col_widths ?? null) === JSON.stringify(r.w ?? null);
                  return (
                    <button
                      key={r.label}
                      type="button"
                      onClick={() => setColumnWidths(b.id, r.w)}
                      className={`flex-1 rounded-lg border px-1 py-1.5 text-xs font-medium transition-colors ${
                        active
                          ? 'border-primary bg-primary/10 text-primary'
                          : 'border-border text-muted-foreground hover:border-ring hover:text-foreground'
                      }`}
                    >
                      {r.label}
                    </button>
                  );
                })}
              </div>
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm text-foreground">
            <input
              type="checkbox"
              checked={!!b.keep_columns}
              onChange={(e) => patch({ keep_columns: e.target.checked || undefined })}
            />
            Keep side by side on mobile (don’t stack)
          </label>
          <p className="text-[11px] text-muted-foreground">
            Drag content into each column. A column must be empty before it can be removed.
          </p>
        </>
      )}

      {b.ref && <SyncedPanel block={b} />}

      <Field label="Show on">
        <VisibilityPicker block={b} onChange={(p) => patch(p)} />
      </Field>

      <ImagePicker
        open={pickerOpen}
        onClose={closePicker}
        onSelect={(url) => patch(pickerTarget === 'bg' ? { bg_url: url } : { src: url })}
      />

      {/* Root blocks compile to their own section — background + padding are
          stylable. Nested blocks share their columns block's section. */}
      {found.parentId === null ? (
        <>
          <Field label="Block background">
            <ColorField label="Block background" value={b.bg} onChange={(bg) => patch({ bg })} />
          </Field>
          <Field label="Background image (URL)">
            <div className="flex gap-1.5">
              <Button variant="outline" size="sm" className="shrink-0" onClick={() => openPicker('bg')}>
                <Images className="h-4 w-4" />
              </Button>
              <Input value={b.bg_url ?? ''} onChange={(e) => patch({ bg_url: e.target.value || undefined }, `bg_url:${b.id}`)} placeholder="https://…" />
            </div>
          </Field>
          <UrlHint value={b.bg_url} />
          {b.bg_url ? (
            <p className="text-[11px] text-muted-foreground">
              The image covers this section (centered). Outlook shows it via a fallback — keep the background color set too.
            </p>
          ) : null}
          <label className="flex items-center gap-2 text-sm text-foreground">
            <input
              type="checkbox"
              checked={!!b.full_width}
              onChange={(e) => patch({ full_width: e.target.checked || undefined })}
            />
            Full-width background (bleeds edge-to-edge in the inbox)
          </label>
          <div className="grid grid-cols-2 gap-2">
            <Field label="Vertical padding (px, blank = 20)">
              <NumField value={b.pad_y} min={0} max={80} onChange={(pad_y) => patch({ pad_y })} />
            </Field>
            <Field label="Side padding (px, blank = 0)">
              <NumField value={b.pad_x} min={0} max={60} onChange={(pad_x) => patch({ pad_x })} />
            </Field>
          </div>
          <Field label="Show only if (per recipient)">
            {/* keyed by block: the editor's custom-path mode is per-block state
                and must not follow the selection to another block */}
            <ConditionEditor key={b.id} cond={b.cond} variableGroups={variableGroups} onChange={(cond) => patch({ cond })} />
          </Field>
        </>
      ) : (
        <p className="text-[11px] text-muted-foreground">
          Backgrounds and padding are set on the whole columns block (select it on the canvas).
        </p>
      )}
    </div>
  );
};

/** ConditionEditor is the Brevo-style "show only if" rule: a merge field, an
 *  operator, and (for comparisons) a value — evaluated per recipient at send.
 *  The sample preview shows every conditional block; "view as contact" applies
 *  the rules for real. */
const ConditionEditor: React.FC<{
  cond?: BlockCondition;
  variableGroups: VariableGroup[];
  onChange: (cond: BlockCondition | undefined) => void;
}> = ({ cond, variableGroups, onChange }) => {
  // Custom-path mode: type a field like contact.custom_fields.plan_tier
  // directly (the backend validates it at save). Entered via the Select's
  // "Custom field…" row; also active for an out-of-catalog stored value.
  const [customMode, setCustomMode] = useState(false);
  if (!cond) {
    return (
      <button
        type="button"
        onClick={() => { setCustomMode(false); onChange({ field: variableGroups[0]?.fields[0]?.path ?? 'contact.email', op: 'exists' }); }}
        className="flex w-full items-center justify-center gap-1.5 rounded-lg border border-dashed border-border py-2 text-xs font-medium text-muted-foreground hover:border-ring hover:text-foreground"
      >
        <Filter className="h-3.5 w-3.5" /> Add condition
      </button>
    );
  }
  const inCatalog = variableGroups.some((g) => g.fields.some((f) => f.path === cond.field));
  const showCustom = customMode || !inCatalog;
  const opMeta = COND_OPS.find((o) => o.key === cond.op) ?? COND_OPS[0];
  const field = cond.field.trim();
  // A path left mid-typing ("contact.custom_fields.") resolves to nothing at
  // send, so the block would silently vanish for every recipient.
  const pathIncomplete = field === '' || field.endsWith('.') || !field.includes('.');
  const numericOp = cond.op === 'gt' || cond.op === 'lt';
  const numericValueOk = (cond.value ?? '').trim() !== '' && Number.isFinite(Number((cond.value ?? '').trim()));
  return (
    <div className="space-y-1.5 rounded-lg border border-border p-2">
      <div className="flex items-center gap-1.5">
        {showCustom ? (
          <Input
            value={cond.field}
            onChange={(e) => onChange({ ...cond, field: e.target.value })}
            placeholder="contact.custom_fields.plan_tier"
            className="min-w-0 flex-1 font-mono text-xs"
            aria-label="Condition field path"
          />
        ) : (
          <Select
            value={cond.field}
            onChange={(e) => {
              if (e.target.value === '__custom__') {
                setCustomMode(true);
                onChange({ ...cond, field: 'contact.custom_fields.' });
                return;
              }
              onChange({ ...cond, field: e.target.value });
            }}
            className="min-w-0 flex-1"
            aria-label="Condition field"
          >
            {variableGroups.map((g) => (
              <optgroup key={g.key} label={g.label}>
                {g.fields.map((f) => (
                  <option key={f.path} value={f.path}>{f.label}</option>
                ))}
              </optgroup>
            ))}
            <option value="__custom__">Custom field…</option>
          </Select>
        )}
        {showCustom && (
          <button type="button" title="Pick from the field list" aria-label="Pick from the field list"
            onClick={() => { setCustomMode(false); onChange({ ...cond, field: variableGroups[0]?.fields[0]?.path ?? 'contact.email' }); }}
            className="shrink-0 rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground">
            <Filter className="h-4 w-4" />
          </button>
        )}
        <button type="button" title="Remove condition" aria-label="Remove condition" onClick={() => { setCustomMode(false); onChange(undefined); }}
          className="shrink-0 rounded p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive">
          <X className="h-4 w-4" />
        </button>
      </div>
      <div className="flex items-center gap-1.5">
        <Select value={cond.op} onChange={(e) => onChange({ ...cond, op: e.target.value })} className="w-32 shrink-0" aria-label="Condition operator">
          {COND_OPS.map((o) => (
            <option key={o.key} value={o.key}>{o.label}</option>
          ))}
        </Select>
        {opMeta.needsValue && (
          <Input
            value={cond.value ?? ''}
            onChange={(e) => onChange({ ...cond, value: e.target.value })}
            placeholder={numericOp ? '100' : 'value…'}
            aria-label="Condition value"
          />
        )}
      </div>
      {/* Both of these save "successfully" but would hide the block from every
          recipient at send — surface them where the mistake is made. */}
      {pathIncomplete && (
        <p className="text-[11px] text-amber-600 dark:text-amber-400">
          Finish the field path (e.g. contact.custom_fields.plan_tier) — as written this matches nobody.
        </p>
      )}
      {numericOp && !numericValueOk && (
        <p className="text-[11px] text-amber-600 dark:text-amber-400">
          Greater/less than compares numbers — enter a plain number like 100 (no currency or thousands separators).
        </p>
      )}
      <p className="text-[11px] text-muted-foreground">
        Recipients who don’t match skip this block. Check it with “view as contact” in the preview.
      </p>
    </div>
  );
};

/** SaveToLibrary snapshots the selected block (nested content included — store
 *  state is always wire-format) into the org's saved-block library. */
const SaveToLibrary: React.FC<{ block: Block }> = ({ block }) => {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [synced, setSynced] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const create = useCreateSavedBlock();

  const openModal = () => {
    setName(`${block.type.charAt(0).toUpperCase()}${block.type.slice(1)} block`);
    setSynced(false);
    setError(null);
    setOpen(true);
  };

  const save = async () => {
    if (!name.trim()) return;
    try {
      await create.mutateAsync({ name: name.trim(), block, synced });
      setOpen(false);
    } catch (e) {
      setError((e as Error).message || 'Could not save the block');
    }
  };

  return (
    <>
      <button type="button" title="Save to library" aria-label="Save block to library" onClick={openModal}
        className="rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground">
        <BookmarkPlus className="h-4 w-4" />
      </button>
      <Modal open={open} onClose={() => setOpen(false)} title="Save block to library" size="sm">
        <div className="space-y-3">
          <Field label="Name">
            {/* no autoFocus — Modal's focus restore breaks with it */}
            <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={120}
              onKeyDown={(e) => { if (e.key === 'Enter') save(); }} placeholder="e.g. Footer CTA" />
          </Field>
          <label className="flex items-start gap-2 text-sm text-foreground">
            <input type="checkbox" className="mt-0.5" checked={synced} onChange={(e) => setSynced(e.target.checked)} />
            <span>
              Keep in sync
              <span className="block text-[11px] font-normal text-muted-foreground">
                Editing it later updates every email that uses it — for headers, footers and legal text.
              </span>
            </span>
          </label>
          <p className="text-[11px] text-muted-foreground">
            Saved blocks appear in the left rail for every email.
            {synced ? ' A synced block stays linked wherever you place it.' : ' Inserting one adds an independent copy.'}
          </p>
          {error && <p className="text-xs text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setOpen(false)}>Cancel</Button>
            <Button size="sm" onClick={save} disabled={create.isPending || !name.trim()}>
              {create.isPending ? 'Saving…' : 'Save block'}
            </Button>
          </div>
        </div>
      </Modal>
    </>
  );
};

/** SyncedPanel is the control surface for a block linked to a synced library
 *  entry. Two deliberate choices, both from what practitioners complain about
 *  elsewhere: the instance stays fully EDITABLE (fragments you cannot override
 *  per campaign are the top complaint about reusable content), and the usage
 *  count is shown BEFORE the update button, because "is it safe to change this?"
 *  is the question that decides whether you press it. */
const SyncedPanel: React.FC<{ block: Block }> = ({ block }) => {
  const unlinkBlock = useBuilderStore((s) => s.unlinkBlock);
  const syncLocalInstances = useBuilderStore((s) => s.syncLocalInstances);
  const usage = useSavedBlockUsage(block.ref ?? null);
  const update = useUpdateSavedBlockContent();
  const { confirm, dialog } = useConfirm();
  const toast = useToast();

  // The count comes from SAVED documents, so this email is only in it once it
  // has been saved with the instance. Never derive "other emails" by
  // subtracting one — that undercounts, and on a just-dropped instance it turns
  // a real "2 emails" into a reassuring "no other email uses it".
  const emails = usage.data?.email_count ?? 0;
  const known = usage.isSuccess;
  // The library row can be deleted while an instance still points at it. Offering
  // "update everywhere" then just 404s, so say what happened instead.
  const library = useSavedBlocks();
  const orphaned = library.isSuccess && !library.data.some((r) => r.id === block.ref);

  const impact = () => {
    if (!known) {
      return 'The number of emails using this block could not be checked, so this may change more emails than you expect.';
    }
    if (emails === 0) return 'This updates the saved block in your library. No saved email uses it yet.';
    return `This replaces the block in ${emails} saved email${emails === 1 ? '' : 's'} and recompiles ${emails === 1 ? 'it' : 'them'}. Their next send uses the new version.`;
  };

  const push = async () => {
    if (!block.ref) return;
    const ok = await confirm({
      title: 'Update this block everywhere?',
      body: impact(),
      confirmLabel: 'Update everywhere',
      tone: known ? undefined : 'danger',
    });
    if (!ok) return;
    try {
      const res = await update.mutateAsync({ id: block.ref, block });
      // The server rewrote every stored instance; bring this document's other
      // copies in line so the next save can't write the old content back.
      syncLocalInstances(block.ref, block);
      const n = res.updated.length;
      toast.show(n > 0 ? `Updated ${n} email${n === 1 ? '' : 's'}` : 'Saved block updated');
      if (res.skipped.length > 0) {
        toast.error(`Not updated: ${res.skipped.join(', ')}`);
      }
    } catch (e) {
      toast.error((e as Error).message || 'Could not update the synced block');
    }
  };

  return (
    <div className="space-y-2 rounded-lg border border-primary/30 bg-primary/5 p-2.5">
      {dialog}
      <p className="flex items-center gap-1.5 text-xs font-semibold text-primary">
        <RefreshCw className="h-3.5 w-3.5" /> Synced block
      </p>
      <p className="text-[11px] text-muted-foreground">
        {usage.isLoading
          ? 'Checking where this block is used…'
          : orphaned
            ? 'The library block this was linked to no longer exists. The content here is unaffected — unlink it to tidy up.'
            : usage.isError
              ? 'Could not check where this block is used.'
              : `Used in ${emails} saved email${emails === 1 ? '' : 's'}. Your edits here stay in this email until you update everywhere.`}
      </p>
      <div className="flex gap-1.5">
        {!orphaned && (
          <Button size="sm" onClick={push} disabled={update.isPending || usage.isLoading}>
            {update.isPending ? 'Updating…' : 'Update everywhere'}
          </Button>
        )}
        <Button variant="outline" size="sm" onClick={() => unlinkBlock(block.id)}>Unlink</Button>
      </div>
      <p className="text-[11px] text-muted-foreground">
        Unlinking keeps this copy exactly as it is and stops it receiving library updates.
      </p>
    </div>
  );
};

/** VisibilityPicker is the Brevo-style "Content visibility" control: show the
 *  block on all devices, desktop only, or mobile only. Clients without
 *  media-query support (old Outlook) show everything — industry caveat. */
const VisibilityPicker: React.FC<{ block: Block; onChange: (p: Partial<Block>) => void }> = ({ block, onChange }) => {
  const cur = block.hide_mobile ? 'desktop' : block.hide_desktop ? 'mobile' : 'all';
  const opts = [
    { v: 'all' as const, label: 'All devices', icon: null },
    { v: 'desktop' as const, label: 'Desktop only', icon: Monitor },
    { v: 'mobile' as const, label: 'Mobile only', icon: Smartphone },
  ];
  const apply = (v: 'all' | 'desktop' | 'mobile') => {
    if (v === cur) return;
    onChange({
      hide_mobile: v === 'desktop' ? true : undefined,
      hide_desktop: v === 'mobile' ? true : undefined,
    });
  };
  return (
    <div className="flex gap-1">
      {opts.map(({ v, label, icon: Icon }) => (
        <button
          key={v}
          type="button"
          title={label}
          aria-label={label}
          aria-pressed={cur === v}
          onClick={() => apply(v)}
          className={`flex h-8 flex-1 items-center justify-center gap-1 rounded-lg border text-xs font-medium transition-colors ${
            cur === v ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:border-ring hover:text-foreground'
          }`}
        >
          {Icon ? <Icon className="h-3.5 w-3.5" /> : null}
          {v === 'all' ? 'All' : null}
        </button>
      ))}
    </div>
  );
};

/** FontField picks a per-block font from the same allowlisted stacks as the
 *  document font (blank = inherit the email font). */
const FontField: React.FC<{ value?: string; onChange: (v: string | undefined) => void }> = ({ value, onChange }) => (
  <Select value={value ?? ''} onChange={(e) => onChange(e.target.value || undefined)}>
    <option value="">Email default</option>
    {FONT_OPTIONS.map((f) => (
      <option key={f.key} value={f.key}>{f.label}</option>
    ))}
  </Select>
);

/** NumField is an optional bounded number input: blank = "use the default". */
const NumField: React.FC<{ value?: number; min: number; max: number; onChange: (v: number | undefined) => void }> = ({ value, min, max, onChange }) => (
  <Input
    type="number"
    min={min}
    max={max}
    value={value != null ? String(value) : ''}
    onChange={(e) => {
      const raw = e.target.value.trim();
      if (raw === '') {
        onChange(undefined);
        return;
      }
      const n = Number(raw);
      if (Number.isFinite(n)) onChange(Math.min(Math.max(Math.round(n), min), max));
    }}
    placeholder="—"
  />
);

/** SocialEditor manages the social block's network/link rows. */
const SocialEditor: React.FC<{ value: SocialLink[]; onChange: (v: SocialLink[]) => void }> = ({ value, onChange }) => {
  const setRow = (i: number, patch: Partial<SocialLink>) =>
    onChange(value.map((row, j) => (j === i ? { ...row, ...patch } : row)));
  return (
    <div className="space-y-2">
      <p className="text-[11px] font-medium text-muted-foreground">Networks</p>
      {value.map((row, i) => (
        <div key={i}>
          <div className="flex items-center gap-1.5">
            <Select value={row.network} onChange={(e) => setRow(i, { network: e.target.value })} className="w-32 shrink-0">
              {SOCIAL_NETWORKS.map((n) => (
                <option key={n.key} value={n.key}>{n.label}</option>
              ))}
            </Select>
            <Input value={row.href} onChange={(e) => setRow(i, { href: e.target.value })} placeholder="https://…" />
            <button type="button" title="Remove network" aria-label="Remove network"
              onClick={() => onChange(value.filter((_, j) => j !== i))}
              className="shrink-0 rounded p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive">
              <Trash2 className="h-4 w-4" />
            </button>
          </div>
          <UrlHint value={row.href} />
        </div>
      ))}
      {value.length < 8 && (
        <button type="button" onClick={() => onChange([...value, { network: 'facebook', href: '' }])}
          className="w-full rounded-lg border border-dashed border-border py-1.5 text-xs font-medium text-muted-foreground hover:border-ring hover:text-foreground">
          + Add network
        </button>
      )}
      <p className="text-[11px] text-muted-foreground">Icons without a link are left out of the sent email.</p>
    </div>
  );
};

/** MenuEditor manages the menu block's label/link rows. */
const MenuEditor: React.FC<{ value: LinkItem[]; onChange: (v: LinkItem[]) => void }> = ({ value, onChange }) => {
  const setRow = (i: number, patch: Partial<LinkItem>) =>
    onChange(value.map((row, j) => (j === i ? { ...row, ...patch } : row)));
  return (
    <div className="space-y-2">
      <p className="text-[11px] font-medium text-muted-foreground">Links</p>
      {value.map((row, i) => (
        <div key={i}>
          <div className="flex items-center gap-1.5">
            <Input value={row.label} onChange={(e) => setRow(i, { label: e.target.value })} placeholder="Label" className="w-28 shrink-0" />
            <Input value={row.href} onChange={(e) => setRow(i, { href: e.target.value })} placeholder="https://…" />
            <button type="button" title="Remove link" aria-label="Remove link"
              onClick={() => onChange(value.filter((_, j) => j !== i))}
              className="shrink-0 rounded p-1.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive">
              <Trash2 className="h-4 w-4" />
            </button>
          </div>
          <UrlHint value={row.href} />
        </div>
      ))}
      {value.length < 6 && (
        <button type="button" onClick={() => onChange([...value, { label: '', href: 'https://' }])}
          className="w-full rounded-lg border border-dashed border-border py-1.5 text-xs font-medium text-muted-foreground hover:border-ring hover:text-foreground">
          + Add link
        </button>
      )}
    </div>
  );
};

/** hexColorOk mirrors the backend's hexColorRe: 3/6/8-digit #hex only. Anything
 *  else renders on the canvas as raw CSS but is silently DROPPED by the
 *  compiler — the exact WYSIWYG lie this validation exists to prevent. */
const hexColorOk = (v: string) => /^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/.test(v);

/** expandHex widens #abc to #aabbcc so the native swatch can display it. */
const expandHex = (v: string): string | null => {
  if (/^#[0-9a-fA-F]{6}/.test(v)) return v.slice(0, 7);
  if (/^#[0-9a-fA-F]{3}$/.test(v)) return `#${v[1]}${v[1]}${v[2]}${v[2]}${v[3]}${v[3]}`;
  return null;
};

/** documentColors harvests the hex colors already used in this document — a
 *  zero-setup "recently used" strip that nudges palette consistency. */
function documentColors(): string[] {
  const s = useBuilderStore.getState();
  const out: string[] = [];
  const push = (v?: string) => {
    if (v && hexColorOk(v) && !out.includes(v)) out.push(v);
  };
  const walk = (bs: Block[]) => {
    for (const b of bs) {
      push(b.color); push(b.bg); push(b.btn_bg); push(b.btn_color);
      for (const col of b.columns ?? []) walk(col);
    }
  };
  walk(s.blocks);
  push(s.bodyBg);
  push(s.styles.textColor); push(s.styles.linkColor); push(s.styles.footerBg); push(s.styles.footerColor);
  return out.slice(0, 8);
}

/** ColorField pairs a native color swatch with a hex input, a clear button and
 *  a document-colors strip. Only #hex values the compiler accepts are treated
 *  as valid; anything else gets an inline warning instead of a silent drop. */
const ColorField: React.FC<{ label: string; value?: string; onChange: (v: string) => void }> = ({ label, value, onChange }) => {
  const invalid = !!value && !hexColorOk(value);
  const swatch = value ? expandHex(value) : null;
  const recents = documentColors().filter((c) => c !== value);
  return (
    <div>
      <div className="flex items-center gap-2">
        <input
          type="color"
          value={swatch ?? '#ffffff'}
          onChange={(e) => onChange(e.target.value)}
          aria-label={`${label} color picker`}
          className="h-9 w-10 cursor-pointer rounded-lg border border-border bg-background p-1"
        />
        <Input
          value={value ?? ''}
          onChange={(e) => onChange(e.target.value.trim())}
          placeholder="none"
          className="w-28 font-mono text-xs"
          aria-label={`${label} hex value`}
        />
        {value && (
          <button
            type="button"
            title="Clear color"
            aria-label={`Clear ${label}`}
            onClick={() => onChange('')}
            className="flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        )}
      </div>
      {invalid && (
        <p className="mt-1 text-[11px] text-amber-600 dark:text-amber-400">
          Not a #hex color — the sent email would ignore it. Use e.g. #2563eb.
        </p>
      )}
      {recents.length > 0 && (
        <div className="mt-1.5 flex items-center gap-1">
          {recents.map((c) => (
            <button
              key={c}
              type="button"
              title={c}
              aria-label={`Use color ${c}`}
              onClick={() => onChange(c)}
              className="h-5 w-5 rounded border border-border"
              style={{ background: expandHex(c) ?? c }}
            />
          ))}
        </div>
      )}
    </div>
  );
};

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => (
  <label className="block">
    <span className="mb-1 block text-[11px] font-medium text-muted-foreground">{label}</span>
    {children}
  </label>
);

/** UrlHint warns about hrefs the backend sanitizer/compiler would reject or
 *  mangle (only http/https/mailto or a merge token survive) — and about the
 *  seeded 'https://' placeholder, which would otherwise ship as a dead link. */
const UrlHint: React.FC<{ value?: string }> = ({ value }) => {
  const v = (value ?? '').trim();
  if (!v) return null;
  if (v === 'https://') {
    return <p className="text-[11px] text-amber-600 dark:text-amber-400">Still the placeholder — add a real link URL.</p>;
  }
  if (/^(https?:\/\/|mailto:)/i.test(v) || /\{\{[^{}]*\}\}/.test(v)) return null;
  return <p className="text-[11px] text-amber-600 dark:text-amber-400">Use a full URL starting with https:// (or mailto:).</p>;
};

const AlignPicker: React.FC<{ value?: string; onChange: (v: 'left' | 'center' | 'right') => void }> = ({ value, onChange }) => {
  const opts = [
    { v: 'left' as const, icon: AlignLeft, label: 'Align left' },
    { v: 'center' as const, icon: AlignCenter, label: 'Align center' },
    { v: 'right' as const, icon: AlignRight, label: 'Align right' },
  ];
  const cur = value ?? 'left';
  return (
    <div className="flex gap-1">
      {opts.map(({ v, icon: Icon, label }) => (
        <button
          key={v}
          type="button"
          title={label}
          aria-label={label}
          aria-pressed={cur === v}
          onClick={() => { if (cur !== v) onChange(v); }}
          className={`flex h-8 flex-1 items-center justify-center rounded-lg border transition-colors ${
            cur === v ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:border-ring hover:text-foreground'
          }`}
        >
          <Icon className="h-4 w-4" />
        </button>
      ))}
    </div>
  );
};

// ---------------------------------------------------------------------------
// Email tab
// ---------------------------------------------------------------------------

const EmailTab: React.FC<{ variableGroups: VariableGroup[] }> = ({ variableGroups }) => {
  const subject = useBuilderStore((s) => s.subject);
  const preheader = useBuilderStore((s) => s.preheader);
  const scope = useBuilderStore((s) => s.scope);
  const bodyBg = useBuilderStore((s) => s.bodyBg);
  const styles = useBuilderStore((s) => s.styles);
  const setSubject = useBuilderStore((s) => s.setSubject);
  const setPreheader = useBuilderStore((s) => s.setPreheader);
  const setBodyBg = useBuilderStore((s) => s.setBodyBg);
  const patchStyles = useBuilderStore((s) => s.patchStyles);
  const toggleScope = useBuilderStore((s) => s.toggleScope);
  const subjectRef = useRef<HTMLInputElement>(null);
  const preheaderRef = useRef<HTMLInputElement>(null);

  // Merge tags land at the CARET (or replace the selection), not at the end of
  // the string — the html-block textarea's idiom, applied to both inbox fields.
  // Called from the insert callback, never during render: handing a ref to a
  // function the render invokes reads as ref-access-during-render.
  const insertAtCaret = (
    ref: React.RefObject<HTMLInputElement | null>,
    value: string,
    apply: (v: string) => void,
    p: string,
    f: string,
  ) => {
    const tok = token(p, f);
    const el = ref.current;
    if (el && el.selectionStart != null) {
      const s = el.selectionStart;
      const e = el.selectionEnd ?? s;
      apply(value.slice(0, s) + tok + value.slice(e));
    } else {
      apply(value + tok);
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <div className="mb-1 flex items-center justify-between">
          <label htmlFor="insp-subject" className="text-xs font-medium text-muted-foreground">Subject line</label>
          <MergeTagMenu variableGroups={variableGroups} onInsert={(p, f) => insertAtCaret(subjectRef, subject, setSubject, p, f)} />
        </div>
        <Input ref={subjectRef} id="insp-subject" value={subject} onChange={(e) => setSubject(e.target.value)} placeholder="Your subject…" maxLength={998} />
        <p className={`mt-1 text-[11px] ${subject.length > 60 ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'}`}>
          {subject.length} characters{subject.length > 60 ? ' — long subjects get cut off on mobile' : ''}
        </p>
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between">
          <label htmlFor="insp-preheader" className="text-xs font-medium text-muted-foreground">Preheader</label>
          <MergeTagMenu variableGroups={variableGroups} onInsert={(p, f) => insertAtCaret(preheaderRef, preheader, setPreheader, p, f)} />
        </div>
        <Input ref={preheaderRef} id="insp-preheader" value={preheader} onChange={(e) => setPreheader(e.target.value)} placeholder="Short summary shown in the inbox…" maxLength={255} />
        <p className="mt-1 text-[11px] text-muted-foreground">{preheader.length}/255 — the inbox preview text after the subject.</p>
      </div>

      <div className="border-t border-border pt-4">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Styles</p>
        <div className="space-y-3">
          <Field label="Font">
            <Select value={styles.fontFamily ?? 'arial'} onChange={(e) => patchStyles({ fontFamily: e.target.value })}>
              {FONT_OPTIONS.map((f) => (
                <option key={f.key} value={f.key}>{f.label}</option>
              ))}
            </Select>
          </Field>
          <Field label="Content width (px, 480–800, blank = 600)">
            <NumField value={styles.width} min={480} max={800} onChange={(width) => patchStyles({ width })} />
          </Field>
          <Field label="Line height">
            <Select
              value={styles.lineHeight != null ? String(styles.lineHeight) : ''}
              onChange={(e) => patchStyles({ lineHeight: e.target.value ? Number(e.target.value) : undefined })}
            >
              <option value="">Default (1.5)</option>
              {['1.2', '1.3', '1.4', '1.6', '1.8', '2'].map((v) => (
                <option key={v} value={v}>{v}</option>
              ))}
            </Select>
          </Field>
          <Field label="Text color">
            <ColorField label="Text color" value={styles.textColor} onChange={(textColor) => patchStyles({ textColor })} />
          </Field>
          <Field label="Link color">
            <ColorField label="Link color" value={styles.linkColor} onChange={(linkColor) => patchStyles({ linkColor })} />
          </Field>
          <Field label="Email background">
            <ColorField label="Email background" value={bodyBg} onChange={setBodyBg} />
          </Field>
        </div>
      </div>

      <div className="border-t border-border pt-4">
        <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Footer</p>
        <div className="space-y-3">
          <Field label="Footer background">
            <ColorField label="Footer background" value={styles.footerBg} onChange={(footerBg) => patchStyles({ footerBg })} />
          </Field>
          <Field label="Footer text color">
            <ColorField label="Footer text color" value={styles.footerColor} onChange={(footerColor) => patchStyles({ footerColor })} />
          </Field>
          <Field label="Extra footer text (above the unsubscribe lines)">
            <Textarea
              value={styles.footerText ?? ''}
              onChange={(e) => patchStyles({ footerText: e.target.value })}
              rows={3}
              placeholder="e.g. Follow us for product updates."
            />
          </Field>
          <p className="text-[11px] text-muted-foreground">
            The unsubscribe link and postal address are required by anti-spam law (CAN-SPAM/GDPR) and always render — style them, don’t fight them.
          </p>
        </div>
      </div>

      <div className="border-t border-border pt-4">
        <p className="mb-1.5 text-xs font-medium text-muted-foreground">Merge fields available</p>
        <div className="space-y-1.5">
          {SELECTABLE_SCOPES.map((s) => (
            <label key={s.root} className="flex items-center gap-2 text-sm text-foreground">
              <input type="checkbox" checked={scope.includes(s.root)} disabled={s.fixed} onChange={() => toggleScope(s.root)} />
              {s.label}
            </label>
          ))}
        </div>
        <p className="mt-1.5 text-[11px] text-muted-foreground">
          Enabling Company lets you use company.* fields for contacts linked to a company.
        </p>
      </div>
    </div>
  );
};

// ---------------------------------------------------------------------------
// Review tab
// ---------------------------------------------------------------------------

const ReviewTab: React.FC<{ meta: InspectorMeta }> = ({ meta }) => {
  const { preview, checklist, saveErrors } = meta;

  return (
    <div className="space-y-4">
      {preview?.size_bytes != null && (
        <p className={`text-sm ${preview.too_large ? 'font-medium text-destructive' : 'text-muted-foreground'}`}>
          Compiled size: {Math.round(preview.size_bytes / 1024)} KB{preview.too_large ? ' — over the 100KB Gmail clip limit!' : ''}
        </p>
      )}

      <ul className="space-y-1.5 text-sm">
        {checklist.map((c, i) => (
          <li key={i} className={`flex items-start gap-2 ${c.ok === true ? 'text-foreground' : c.ok === 'pending' ? 'text-muted-foreground' : 'text-amber-600 dark:text-amber-400'}`}>
            {c.ok === true ? <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
              : c.ok === 'pending' ? <Clock className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
              : <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />}
            <span>{c.label}</span>
          </li>
        ))}
      </ul>

      {saveErrors.length > 0 && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-2.5 text-xs text-destructive">
          <p className="mb-1 font-medium">Merge-tag problems (blocking save):</p>
          <ul className="list-inside list-disc space-y-0.5">{saveErrors.map((e, i) => <li key={i}>{e}</li>)}</ul>
        </div>
      )}
    </div>
  );
};

export default InspectorPanel;
