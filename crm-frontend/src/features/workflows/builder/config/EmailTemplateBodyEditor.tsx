import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useEditor, EditorContent } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import { Bold, Italic, Heading2, List, ListOrdered, Braces, Search } from 'lucide-react';
import { MergeTag } from './mergeTag';
import { serializeMergeTags } from './mergeTagHtml';

export interface VariableGroup {
  key: string;
  label: string;
  fields: { path: string; label: string }[];
}

interface Props {
  /** Initial body HTML (used when no body_json exists — e.g. a hand-edited template). */
  initialHtml: string;
  /** Initial TipTap doc (preferred — lossless re-edit of chips). */
  initialJson?: unknown;
  /** Merge-tag catalog for the insert dropdown, scoped to the template's object. */
  variableGroups: VariableGroup[];
  /** Emits (clean body_html with {{tags}}, TipTap doc json) on every edit. */
  onChange: (html: string, json: unknown) => void;
}

/** Rich-text body editor for email templates (A5.3): StarterKit formatting plus an
 *  inline merge-tag chip node. On every change it emits clean {{tag}} HTML for
 *  sending and the ProseMirror doc for lossless re-editing. */
export const EmailTemplateBodyEditor: React.FC<Props> = ({ initialHtml, initialJson, variableGroups, onChange }) => {
  const editor = useEditor({
    extensions: [StarterKit, MergeTag],
    content: (initialJson as object) ?? initialHtml ?? '',
    onUpdate: ({ editor }) => onChange(serializeMergeTags(editor.getHTML()), editor.getJSON()),
    editorProps: { attributes: { class: 'focus:outline-none' } },
  });

  if (!editor) {
    return <div className="h-64 rounded-lg border border-border bg-background" />;
  }

  return (
    <div className="et-editor overflow-hidden rounded-lg border border-border bg-background">
      <Toolbar editor={editor} variableGroups={variableGroups} />
      <EditorContent editor={editor} />
    </div>
  );
};

const btnCls = (active: boolean) =>
  `flex h-7 w-7 items-center justify-center rounded transition-colors ${
    active ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'
  }`;

type EditorLike = NonNullable<ReturnType<typeof useEditor>>;

const Toolbar: React.FC<{ editor: EditorLike; variableGroups: VariableGroup[] }> = ({ editor, variableGroups }) => (
  <div className="flex items-center gap-1 border-b border-border bg-muted/40 px-2 py-1.5">
    <button type="button" title="Bold" className={btnCls(editor.isActive('bold'))} onClick={() => editor.chain().focus().toggleBold().run()}>
      <Bold className="h-3.5 w-3.5" />
    </button>
    <button type="button" title="Italic" className={btnCls(editor.isActive('italic'))} onClick={() => editor.chain().focus().toggleItalic().run()}>
      <Italic className="h-3.5 w-3.5" />
    </button>
    <button type="button" title="Heading" className={btnCls(editor.isActive('heading', { level: 2 }))} onClick={() => editor.chain().focus().toggleHeading({ level: 2 }).run()}>
      <Heading2 className="h-3.5 w-3.5" />
    </button>
    <button type="button" title="Bullet list" className={btnCls(editor.isActive('bulletList'))} onClick={() => editor.chain().focus().toggleBulletList().run()}>
      <List className="h-3.5 w-3.5" />
    </button>
    <button type="button" title="Numbered list" className={btnCls(editor.isActive('orderedList'))} onClick={() => editor.chain().focus().toggleOrderedList().run()}>
      <ListOrdered className="h-3.5 w-3.5" />
    </button>
    <div className="mx-1 h-4 w-px bg-border" />
    <MergeTagMenu editor={editor} variableGroups={variableGroups} />
  </div>
);

const MergeTagMenu: React.FC<{ editor: EditorLike; variableGroups: VariableGroup[] }> = ({ editor, variableGroups }) => {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const handler = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', handler);
    return () => document.removeEventListener('mousedown', handler);
  }, [open]);

  const groups = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return variableGroups;
    return variableGroups
      .map((g) => ({ ...g, fields: g.fields.filter((f) => f.label.toLowerCase().includes(q) || f.path.toLowerCase().includes(q)) }))
      .filter((g) => g.fields.length > 0);
  }, [variableGroups, search]);

  const insert = (path: string) => {
    editor.chain().focus().insertContent({ type: 'mergeTag', attrs: { path } }).insertContent(' ').run();
    setOpen(false);
    setSearch('');
  };

  return (
    <div className="relative" ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        title="Insert merge tag"
        className={`flex items-center gap-1 rounded px-2 py-1 text-xs font-medium transition-colors ${
          open ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'
        }`}
      >
        <Braces className="h-3.5 w-3.5" /> Insert variable
      </button>
      {open && (
        <div className="absolute left-0 top-full z-50 mt-1 flex max-h-72 w-64 flex-col overflow-hidden rounded-xl border border-border bg-popover text-popover-foreground shadow-2xl">
          <div className="border-b border-border px-2 py-2">
            <div className="relative">
              <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
              <input
                autoFocus
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search fields…"
                className="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-2 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          </div>
          <div className="flex-1 overflow-y-auto py-1">
            {groups.length === 0 ? (
              <p className="px-3 py-3 text-center text-xs text-muted-foreground">No matching fields</p>
            ) : (
              groups.map((g) => (
                <div key={g.key}>
                  <div className="px-3 py-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{g.label}</div>
                  {g.fields.map((f) => (
                    <button
                      key={f.path}
                      type="button"
                      onClick={() => insert(f.path)}
                      className="flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors hover:bg-accent hover:text-accent-foreground"
                    >
                      <span className="flex-1 truncate text-xs">{f.label}</span>
                      <code className="shrink-0 rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">{`{{${f.path}}}`}</code>
                    </button>
                  ))}
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
};

export default EmailTemplateBodyEditor;
