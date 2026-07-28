import React, { useEffect, useState } from 'react';
import {
  AlertCircle, AlignCenter, AlignLeft, AlignRight, CheckCircle2, Clock, Copy, MousePointerClick, Trash2,
} from 'lucide-react';
import { Input, Select } from '@/components/ui';
import type { Block } from './blocks';
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
}

type Tab = 'block' | 'email' | 'review';

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
          <BlockTab />
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

const BlockTab: React.FC = () => {
  const blocks = useBuilderStore((s) => s.blocks);
  const selectedId = useBuilderStore((s) => s.selectedId);
  const patchBlock = useBuilderStore((s) => s.patchBlock);
  const removeBlock = useBuilderStore((s) => s.removeBlock);
  const duplicateBlock = useBuilderStore((s) => s.duplicateBlock);
  const setColumnCount = useBuilderStore((s) => s.setColumnCount);

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
          <button type="button" title="Duplicate block" aria-label="Duplicate block" onClick={() => duplicateBlock(b.id)}
            className="rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"><Copy className="h-4 w-4" /></button>
          <button type="button" title="Delete block" aria-label="Delete block" onClick={() => removeBlock(b.id)}
            className="rounded p-1.5 text-destructive hover:bg-destructive/10"><Trash2 className="h-4 w-4" /></button>
        </div>
      </div>

      {b.type === 'text' && (
        <Field label="Alignment">
          <AlignPicker value={b.align} onChange={(align) => patch({ align })} />
        </Field>
      )}

      {b.type === 'heading' && (
        <>
          <Field label="Size">
            <Select value={String(b.level ?? 2)} onChange={(e) => patch({ level: Number(e.target.value) })}>
              <option value="1">Large (24px)</option>
              <option value="2">Medium (20px)</option>
              <option value="3">Small (17px)</option>
            </Select>
          </Field>
          <Field label="Alignment">
            <AlignPicker value={b.align} onChange={(align) => patch({ align })} />
          </Field>
          {found.parentId && (
            <p className="text-[11px] text-muted-foreground">Inside columns, headings render at a fixed 18px.</p>
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
          <p className="text-[11px] text-muted-foreground">Buttons are centered by the email layout.</p>
        </>
      )}

      {b.type === 'image' && (
        <>
          <Field label="Image URL">
            <Input value={b.src ?? ''} onChange={(e) => patch({ src: e.target.value }, `src:${b.id}`)} placeholder="https://…/image.png" />
          </Field>
          <UrlHint value={b.src} />
          <Field label="Alt text">
            <Input value={b.alt ?? ''} onChange={(e) => patch({ alt: e.target.value }, `alt:${b.id}`)} placeholder="Describe the image" />
          </Field>
          {!b.alt?.trim() && (
            <p className="text-[11px] text-amber-600 dark:text-amber-400">Alt text helps accessibility and deliverability.</p>
          )}
          {found.parentId ? (
            // compile.go columnInner drops image href — offering a link here
            // would ship a silently non-clickable image.
            <p className="text-[11px] text-muted-foreground">Inside columns, images can’t be linked.</p>
          ) : (
            <Field label="Link (optional)">
              <Input value={b.href ?? ''} onChange={(e) => patch({ href: e.target.value }, `href:${b.id}`)} placeholder="https://…" />
            </Field>
          )}
        </>
      )}

      {b.type === 'divider' && <p className="text-sm text-muted-foreground">A horizontal divider line.</p>}

      {b.type === 'spacer' && (
        <Field label={`Height — ${b.height ?? 24}px`}>
          <div className="flex items-center gap-3">
            <input
              type="range"
              min={8}
              max={120}
              step={4}
              value={b.height ?? 24}
              onChange={(e) => patch({ height: Number(e.target.value) }, `height:${b.id}`)}
              className="flex-1 accent-[hsl(var(--primary))]"
              aria-label="Spacer height"
            />
            <Input
              type="number"
              min={4}
              max={200}
              value={String(b.height ?? 24)}
              onChange={(e) => patch({ height: Number(e.target.value) }, `height:${b.id}`)}
              className="w-20"
            />
          </div>
        </Field>
      )}

      {b.type === 'columns' && (
        <>
          <Field label="Columns">
            <div className="flex gap-2">
              {[2, 3].map((n) => (
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
          <p className="text-[11px] text-muted-foreground">
            Drag text, headings, buttons, images or dividers into each column. A column must be empty before it can be removed.
          </p>
        </>
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
  const setSubject = useBuilderStore((s) => s.setSubject);
  const setPreheader = useBuilderStore((s) => s.setPreheader);
  const toggleScope = useBuilderStore((s) => s.toggleScope);

  return (
    <div className="space-y-4">
      <div>
        <div className="mb-1 flex items-center justify-between">
          <label htmlFor="insp-subject" className="text-xs font-medium text-muted-foreground">Subject line</label>
          <MergeTagMenu variableGroups={variableGroups} onInsert={(p, f) => setSubject(subject + token(p, f))} />
        </div>
        <Input id="insp-subject" value={subject} onChange={(e) => setSubject(e.target.value)} placeholder="Your subject…" maxLength={998} />
        <p className={`mt-1 text-[11px] ${subject.length > 60 ? 'text-amber-600 dark:text-amber-400' : 'text-muted-foreground'}`}>
          {subject.length} characters{subject.length > 60 ? ' — long subjects get cut off on mobile' : ''}
        </p>
      </div>

      <div>
        <div className="mb-1 flex items-center justify-between">
          <label htmlFor="insp-preheader" className="text-xs font-medium text-muted-foreground">Preheader</label>
          <MergeTagMenu variableGroups={variableGroups} onInsert={(p, f) => setPreheader(preheader + token(p, f))} />
        </div>
        <Input id="insp-preheader" value={preheader} onChange={(e) => setPreheader(e.target.value)} placeholder="Short summary shown in the inbox…" maxLength={255} />
        <p className="mt-1 text-[11px] text-muted-foreground">{preheader.length}/255 — the inbox preview text after the subject.</p>
      </div>

      <div>
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
