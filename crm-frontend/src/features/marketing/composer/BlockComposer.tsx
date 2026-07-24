import React, { useState } from 'react';
import {
  Type, Heading, MousePointerClick, Image as ImageIcon, Columns2, Minus, MoveVertical,
  Plus, Trash2, ChevronUp, ChevronDown, ShieldCheck,
} from 'lucide-react';
import { Input, Select } from '@/components/ui';
import { RichTextEditor } from './RichTextEditor';
import { PALETTE, makeBlock, type Block, type BlockType } from './blocks';
import type { VariableGroup } from './mergeScope';

const ICONS: Record<string, React.ComponentType<{ className?: string }>> = {
  Type, Heading, MousePointerClick, Image: ImageIcon, Columns2, Minus, MoveVertical,
};

interface Props {
  blocks: Block[];
  variableGroups: VariableGroup[];
  onChange: (blocks: Block[]) => void;
}

/** BlockComposer edits the ordered block document. Reorder via up/down; the
 *  compliance footer is compiler-owned (not an editable block) and shown as a
 *  locked note so the author knows it is always appended. */
export const BlockComposer: React.FC<Props> = ({ blocks, variableGroups, onChange }) => {
  const [adding, setAdding] = useState(false);

  const patch = (id: string, p: Partial<Block>) => onChange(blocks.map((b) => (b.id === id ? { ...b, ...p } : b)));
  const remove = (id: string) => onChange(blocks.filter((b) => b.id !== id));
  const move = (idx: number, dir: -1 | 1) => {
    const j = idx + dir;
    if (j < 0 || j >= blocks.length) return;
    const next = blocks.slice();
    [next[idx], next[j]] = [next[j], next[idx]];
    onChange(next);
  };
  const add = (t: BlockType) => { onChange([...blocks, makeBlock(t)]); setAdding(false); };

  return (
    <div className="space-y-3">
      {blocks.length === 0 && (
        <p className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
          No blocks yet — add one below to start building your email.
        </p>
      )}

      {blocks.map((b, idx) => (
        <div key={b.id} className="rounded-xl border border-border bg-card">
          <div className="flex items-center justify-between border-b border-border px-3 py-1.5">
            <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{b.type}</span>
            <div className="flex items-center gap-1">
              <button type="button" title="Move up" disabled={idx === 0} onClick={() => move(idx, -1)} className="rounded p-1 text-muted-foreground hover:text-foreground disabled:opacity-30"><ChevronUp className="h-4 w-4" /></button>
              <button type="button" title="Move down" disabled={idx === blocks.length - 1} onClick={() => move(idx, 1)} className="rounded p-1 text-muted-foreground hover:text-foreground disabled:opacity-30"><ChevronDown className="h-4 w-4" /></button>
              <button type="button" title="Delete block" onClick={() => remove(b.id)} className="rounded p-1 text-destructive hover:bg-destructive/10"><Trash2 className="h-4 w-4" /></button>
            </div>
          </div>
          <div className="p-3">
            <BlockEditor block={b} variableGroups={variableGroups} patch={(p) => patch(b.id, p)} />
          </div>
        </div>
      ))}

      {/* Compiler-owned footer */}
      <div className="flex items-center gap-2 rounded-xl border border-dashed border-border bg-muted/30 px-3 py-2.5 text-xs text-muted-foreground">
        <ShieldCheck className="h-4 w-4 shrink-0" />
        A compliance footer (your workspace address + one-click unsubscribe) is added automatically to every email and can’t be removed.
      </div>

      {/* Add block */}
      <div className="relative">
        {adding ? (
          <div className="grid grid-cols-2 gap-2 rounded-xl border border-border bg-card p-2 sm:grid-cols-4">
            {PALETTE.map((m) => {
              const Icon = ICONS[m.icon] ?? Type;
              return (
                <button key={m.type} type="button" onClick={() => add(m.type)}
                  className="flex flex-col items-center gap-1 rounded-lg border border-border/60 px-2 py-3 text-xs text-foreground transition-colors hover:border-ring hover:bg-accent">
                  <Icon className="h-4 w-4 text-muted-foreground" />{m.label}
                </button>
              );
            })}
          </div>
        ) : (
          <button type="button" onClick={() => setAdding(true)}
            className="flex w-full items-center justify-center gap-2 rounded-xl border border-dashed border-border py-2.5 text-sm font-medium text-muted-foreground transition-colors hover:border-ring hover:text-foreground">
            <Plus className="h-4 w-4" /> Add block
          </button>
        )}
      </div>
    </div>
  );
};

const BlockEditor: React.FC<{ block: Block; variableGroups: VariableGroup[]; patch: (p: Partial<Block>) => void }> = ({ block, variableGroups, patch }) => {
  switch (block.type) {
    case 'text':
      return (
        <div className="space-y-2">
          <RichTextEditor key={block.id} initialHtml={block.text ?? ''} variableGroups={variableGroups} onChange={(html) => patch({ text: html })} />
          <AlignPicker value={block.align} onChange={(align) => patch({ align })} />
        </div>
      );
    case 'heading':
      return (
        <div className="space-y-2">
          <RichTextEditor key={block.id} initialHtml={block.text ?? ''} variableGroups={variableGroups} onChange={(html) => patch({ text: html })} minHeight="3rem" />
          <div className="flex gap-2">
            <Select value={String(block.level ?? 2)} onChange={(e) => patch({ level: Number(e.target.value) })} className="w-32">
              <option value="1">Heading 1</option>
              <option value="2">Heading 2</option>
              <option value="3">Heading 3</option>
            </Select>
            <AlignPicker value={block.align} onChange={(align) => patch({ align })} />
          </div>
        </div>
      );
    case 'button':
      return (
        <div className="grid gap-2 sm:grid-cols-2">
          <Field label="Label"><Input value={block.label ?? ''} onChange={(e) => patch({ label: e.target.value })} placeholder="Shop now" /></Field>
          <Field label="Link (href)"><Input value={block.href ?? ''} onChange={(e) => patch({ href: e.target.value })} placeholder="https://…" /></Field>
        </div>
      );
    case 'image':
      return (
        <div className="grid gap-2 sm:grid-cols-3">
          <Field label="Image URL"><Input value={block.src ?? ''} onChange={(e) => patch({ src: e.target.value })} placeholder="https://…/img.png" /></Field>
          <Field label="Alt text"><Input value={block.alt ?? ''} onChange={(e) => patch({ alt: e.target.value })} placeholder="Describe the image" /></Field>
          <Field label="Link (optional)"><Input value={block.href ?? ''} onChange={(e) => patch({ href: e.target.value })} placeholder="https://…" /></Field>
        </div>
      );
    case 'divider':
      return <p className="text-xs text-muted-foreground">A horizontal divider line.</p>;
    case 'spacer':
      return (
        <Field label="Height (px)"><Input type="number" min={4} max={200} value={String(block.height ?? 24)} onChange={(e) => patch({ height: Number(e.target.value) })} className="w-32" /></Field>
      );
    case 'columns': {
      const cols = block.columns ?? [[], []];
      const setColText = (i: number, html: string) => {
        const next = cols.map((c) => c.slice());
        const sub = next[i][0] ?? makeBlock('text');
        next[i][0] = { ...sub, type: 'text', text: html };
        patch({ columns: next });
      };
      return (
        <div className="grid gap-3 sm:grid-cols-2">
          {[0, 1].map((i) => (
            <div key={i}>
              <p className="mb-1 text-[11px] font-medium text-muted-foreground">Column {i + 1}</p>
              <RichTextEditor key={`${block.id}-c${i}`} initialHtml={cols[i]?.[0]?.text ?? ''} variableGroups={variableGroups} onChange={(html) => setColText(i, html)} minHeight="4rem" />
            </div>
          ))}
        </div>
      );
    }
    default:
      return null;
  }
};

const Field: React.FC<{ label: string; children: React.ReactNode }> = ({ label, children }) => (
  <label className="block">
    <span className="mb-1 block text-[11px] font-medium text-muted-foreground">{label}</span>
    {children}
  </label>
);

const AlignPicker: React.FC<{ value?: string; onChange: (v: 'left' | 'center' | 'right') => void }> = ({ value, onChange }) => (
  <Select value={value ?? 'left'} onChange={(e) => onChange(e.target.value as 'left' | 'center' | 'right')} className="w-28">
    <option value="left">Left</option>
    <option value="center">Center</option>
    <option value="right">Right</option>
  </Select>
);

export default BlockComposer;
