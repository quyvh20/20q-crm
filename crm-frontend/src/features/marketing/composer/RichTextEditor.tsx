import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useEditor, EditorContent } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { Bold, Italic, List, ListOrdered, Braces, Search } from 'lucide-react';
import { MergeTag } from './mergeTag';
import { serializeMergeTags, deserializeMergeTags } from './mergeTagHtml';
import { isGuaranteed, type VariableGroup } from './mergeScope';

interface Props {
  initialHtml: string;                 // block text (bare {{tokens}})
  variableGroups: VariableGroup[];     // pickable merge fields for the declared scope
  onChange: (html: string) => void;    // emits clean {{token}} HTML on every edit
  minHeight?: string;
}

/** RichTextEditor is the composer's text authoring surface: StarterKit formatting +
 *  the merge-tag chip node with mandatory-fallback capture. Chips round-trip via
 *  deserialize (load) / serialize (emit) of the bare {{path|fallback}} tokens. */
export const RichTextEditor: React.FC<Props> = ({ initialHtml, variableGroups, onChange, minHeight = '6rem' }) => {
  const editor = useEditor({
    extensions: [StarterKit, MergeTag],
    content: deserializeMergeTags(initialHtml || ''),
    onUpdate: ({ editor }) => onChange(serializeMergeTags(editor.getHTML())),
    editorProps: { attributes: { class: 'focus:outline-none', style: `min-height:${minHeight}` } },
  });

  if (!editor) {
    return <div className="rounded-lg border border-border bg-background" style={{ minHeight }} />;
  }

  return (
    <div className="et-editor overflow-visible rounded-lg border border-border bg-background">
      <div className="flex items-center gap-1 border-b border-border bg-muted/40 px-2 py-1.5">
        <TB active={editor.isActive('bold')} title="Bold" onClick={() => editor.chain().focus().toggleBold().run()}><Bold className="h-3.5 w-3.5" /></TB>
        <TB active={editor.isActive('italic')} title="Italic" onClick={() => editor.chain().focus().toggleItalic().run()}><Italic className="h-3.5 w-3.5" /></TB>
        <TB active={editor.isActive('bulletList')} title="Bullet list" onClick={() => editor.chain().focus().toggleBulletList().run()}><List className="h-3.5 w-3.5" /></TB>
        <TB active={editor.isActive('orderedList')} title="Numbered list" onClick={() => editor.chain().focus().toggleOrderedList().run()}><ListOrdered className="h-3.5 w-3.5" /></TB>
        <div className="mx-1 h-4 w-px bg-border" />
        <MergeTagMenu variableGroups={variableGroups} onInsert={(path, fallback) => {
          editor.chain().focus().insertContent({ type: 'mergeTag', attrs: { path, fallback } }).insertContent(' ').run();
        }} />
      </div>
      <div className="px-3 py-2 text-sm">
        <EditorContent editor={editor} />
      </div>
    </div>
  );
};

const TB: React.FC<{ active: boolean; title: string; onClick: () => void; children: React.ReactNode }> = ({ active, title, onClick, children }) => (
  <button type="button" title={title} onClick={onClick}
    className={`flex h-7 w-7 items-center justify-center rounded transition-colors ${active ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'}`}>
    {children}
  </button>
);

export const MergeTagMenu: React.FC<{ variableGroups: VariableGroup[]; onInsert: (path: string, fallback: string) => void }> = ({ variableGroups, onInsert }) => {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const [pending, setPending] = useState<{ path: string; label: string } | null>(null);
  const [fallback, setFallback] = useState('');
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => { if (ref.current && !ref.current.contains(e.target as Node)) reset(); };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const reset = () => { setOpen(false); setSearch(''); setPending(null); setFallback(''); };

  const groups = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return variableGroups;
    return variableGroups
      .map((g) => ({ ...g, fields: g.fields.filter((f) => f.label.toLowerCase().includes(q) || f.path.toLowerCase().includes(q)) }))
      .filter((g) => g.fields.length > 0);
  }, [variableGroups, search]);

  const guaranteed = pending ? isGuaranteed(pending.path) : false;
  // Braces and the pipe can't appear in a fallback — the {{path|fallback}} grammar
  // (FE + backend) uses [^}] for the fallback, so a '}' or '|' silently corrupts the
  // token (renders literally, bypasses validation). Reject at authoring time.
  const fallbackInvalid = /[{}|]/.test(fallback);
  const canInsert = !!pending && !fallbackInvalid && (guaranteed || fallback.trim() !== '');

  const doInsert = () => {
    if (!pending || !canInsert) return;
    onInsert(pending.path, fallback.trim());
    reset();
  };

  return (
    <div className="relative" ref={ref}>
      <button type="button" onClick={() => (open ? reset() : setOpen(true))} title="Insert merge tag"
        className={`flex items-center gap-1 rounded px-2 py-1 text-xs font-medium transition-colors ${open ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'}`}>
        <Braces className="h-3.5 w-3.5" /> Insert variable
      </button>
      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 flex max-h-80 w-72 flex-col overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-2xl">
          {!pending ? (
            <>
              <div className="border-b border-border px-2 py-2">
                <div className="relative">
                  <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                  <input autoFocus value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search fields…"
                    className="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-2 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring" />
                </div>
              </div>
              <div className="flex-1 overflow-y-auto py-1">
                {groups.length === 0 ? (
                  <p className="px-3 py-3 text-center text-xs text-muted-foreground">No matching fields</p>
                ) : groups.map((g) => (
                  <div key={g.key}>
                    <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{g.label}</div>
                    {g.fields.map((f) => (
                      <button key={f.path} type="button" onClick={() => { setPending(f); setFallback(''); }}
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors hover:bg-accent hover:text-accent-foreground">
                        <span className="flex-1 truncate text-xs">{f.label}</span>
                        <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{`{{${f.path}}}`}</code>
                      </button>
                    ))}
                  </div>
                ))}
              </div>
            </>
          ) : (
            <div className="p-3">
              <p className="mb-1 text-xs font-medium text-foreground">{pending.label}</p>
              <label className="mb-1 block text-[11px] text-muted-foreground">
                Fallback {guaranteed ? '(optional)' : '(required — shown when the value is empty)'}
              </label>
              <input autoFocus value={fallback} onChange={(e) => setFallback(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter') doInsert(); }}
                placeholder="e.g. there"
                className="w-full rounded-lg border border-border/60 bg-background px-2 py-1.5 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring" />
              {fallbackInvalid && <p className="mt-1 text-[11px] text-destructive">A fallback can’t contain {'{'}, {'}'} or |.</p>}
              <div className="mt-2 flex justify-end gap-2">
                <button type="button" onClick={() => setPending(null)} className="rounded-lg px-2 py-1 text-xs text-muted-foreground hover:text-foreground">Back</button>
                <button type="button" disabled={!canInsert} onClick={doInsert}
                  className="rounded-lg bg-primary px-2.5 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50">Insert</button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
};

export default RichTextEditor;
