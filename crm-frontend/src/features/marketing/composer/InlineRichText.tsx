import React, { useEffect, useRef, useState } from 'react';
import { useEditor, EditorContent, type Editor } from '@tiptap/react';
import StarterKit from '@tiptap/starter-kit';
import {
  Bold, Italic, Underline as UnderlineIcon, Strikethrough, List, ListOrdered, Link2, Link2Off,
} from 'lucide-react';
import { MergeTag } from './mergeTag';
import { serializeMergeTags, deserializeMergeTags } from './mergeTagHtml';
import { MergeTagMenu } from './MergeTagMenu';
import type { VariableGroup } from './mergeScope';

interface Props {
  html: string;                     // wire-format: bare {{path|fallback}} tokens
  variableGroups: VariableGroup[];
  onChange: (html: string) => void; // wire-format out on every edit
  active: boolean;                  // block is selected → show the format toolbar
  variant: 'text' | 'heading';
  level?: number;                   // heading level 1-3
  nested?: boolean;                 // inside a column (compiler renders 18px headings there)
  align?: 'left' | 'center' | 'right';
  readOnly?: boolean;               // mobile preview: contenteditable off
  overrideSize?: number;            // block-level font-size override (px)
  overrideColor?: string;           // block-level text color override (#hex)
}

/** headingSizePx mirrors compile.go: level 2→20px, 3→17px, else 24px; columns→18px. */
function headingSizePx(level: number | undefined, nested: boolean | undefined): number {
  if (nested) return 18;
  switch (level) {
    case 2: return 20;
    case 3: return 17;
    default: return 24;
  }
}

/** InlineRichText is the on-canvas authoring surface for text/heading blocks. It
 *  renders WITH the email's own typography (Arial #111827, compile.go constants)
 *  so what you type is what recipients get, and keeps state wire-format: chips
 *  serialize to bare tokens on every update, deserialize on load. */
export const InlineRichText: React.FC<Props> = ({ html, variableGroups, onChange, active, variant, level, nested, align, readOnly, overrideSize, overrideColor }) => {
  // Track the last html WE emitted so an external change (undo/redo) can be told
  // apart from the echo of our own onUpdate.
  const lastEmitted = useRef(html);

  const editor = useEditor({
    extensions: [
      StarterKit.configure({
        // openOnClick would navigate away mid-edit; authors follow links from
        // the preview instead.
        link: { openOnClick: false },
        // Kill the markdown input rules for content the pipeline can't carry:
        // headings are a dedicated BLOCK type here, and hr/code/codeBlock are
        // stripped by the backend sanitizer — letting authors type `---` or
        // ``` would render on canvas and silently vanish from the sent email.
        heading: false,
        horizontalRule: false,
        code: false,
        codeBlock: false,
      }),
      MergeTag,
    ],
    editable: !readOnly,
    content: deserializeMergeTags(html || ''),
    onUpdate: ({ editor }) => {
      const out = serializeMergeTags(editor.getHTML());
      lastEmitted.current = out;
      onChange(out);
    },
    editorProps: { attributes: { class: 'focus:outline-none' } },
  });

  // External content change (undo/redo, AI edits): reload the editor. Skip our
  // own echoes, or every keystroke would reset the cursor. emitUpdate false is
  // load-bearing: TipTap v3 fires onUpdate from setContent by default, and that
  // echo would push phantom history and wipe the redo stack right after undo.
  useEffect(() => {
    if (!editor || html === lastEmitted.current) return;
    lastEmitted.current = html;
    editor.commands.setContent(deserializeMergeTags(html || ''), { emitUpdate: false });
  }, [editor, html]);

  useEffect(() => {
    editor?.setEditable(!readOnly);
  }, [editor, readOnly]);

  const basePx = variant === 'heading' ? headingSizePx(level, nested) : 15;
  const px = overrideSize && overrideSize >= 10 && overrideSize <= 60 ? overrideSize : basePx;

  return (
    <div className="relative">
      {active && !readOnly && editor && <FormatToolbar editor={editor} variant={variant} variableGroups={variableGroups} />}
      <div
        className={`email-rte ${variant === 'heading' ? 'email-rte-heading' : ''}`}
        style={{ fontSize: `${px}px`, textAlign: align ?? 'left', color: overrideColor || undefined }}
      >
        {editor ? <EditorContent editor={editor} /> : <div style={{ minHeight: '1.5em' }} />}
      </div>
    </div>
  );
};

const FormatToolbar: React.FC<{ editor: Editor; variant: 'text' | 'heading'; variableGroups: VariableGroup[] }> = ({ editor, variant, variableGroups }) => {
  const [linkOpen, setLinkOpen] = useState(false);
  const [linkUrl, setLinkUrl] = useState('');
  const linkHasToken = /\{\{/.test(linkUrl);

  const applyLink = () => {
    let url = linkUrl.trim();
    if (!url || linkHasToken) return;
    // The backend sanitizer only keeps http/https/mailto hrefs — anything else is
    // stripped silently, so normalize/refuse here instead.
    if (!/^(https?:\/\/|mailto:)/i.test(url)) url = `https://${url}`;
    editor.chain().focus().extendMarkRange('link').setLink({ href: url }).run();
    setLinkOpen(false);
    setLinkUrl('');
  };

  return (
    <div
      className="absolute -top-12 left-0 z-30 flex items-center gap-0.5 rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-lg"
      // Keep clicks from bubbling to the canvas (deselect) or stealing the
      // editor's selection before the command runs.
      onClick={(e) => e.stopPropagation()}
      onMouseDown={(e) => { if ((e.target as HTMLElement).tagName !== 'INPUT') e.preventDefault(); }}
    >
      <TB active={editor.isActive('bold')} title="Bold" onClick={() => editor.chain().focus().toggleBold().run()}><Bold className="h-3.5 w-3.5" /></TB>
      <TB active={editor.isActive('italic')} title="Italic" onClick={() => editor.chain().focus().toggleItalic().run()}><Italic className="h-3.5 w-3.5" /></TB>
      <TB active={editor.isActive('underline')} title="Underline" onClick={() => editor.chain().focus().toggleUnderline().run()}><UnderlineIcon className="h-3.5 w-3.5" /></TB>
      <TB active={editor.isActive('strike')} title="Strikethrough" onClick={() => editor.chain().focus().toggleStrike().run()}><Strikethrough className="h-3.5 w-3.5" /></TB>
      {variant === 'text' && (
        <>
          <div className="mx-0.5 h-4 w-px bg-border" />
          <TB active={editor.isActive('bulletList')} title="Bullet list" onClick={() => editor.chain().focus().toggleBulletList().run()}><List className="h-3.5 w-3.5" /></TB>
          <TB active={editor.isActive('orderedList')} title="Numbered list" onClick={() => editor.chain().focus().toggleOrderedList().run()}><ListOrdered className="h-3.5 w-3.5" /></TB>
        </>
      )}
      <div className="mx-0.5 h-4 w-px bg-border" />
      {editor.isActive('link') ? (
        <TB active title="Remove link" onClick={() => editor.chain().focus().unsetLink().run()}><Link2Off className="h-3.5 w-3.5" /></TB>
      ) : (
        <TB active={linkOpen} title="Add link" onClick={() => setLinkOpen((v) => !v)}><Link2 className="h-3.5 w-3.5" /></TB>
      )}
      <div className="mx-0.5 h-4 w-px bg-border" />
      <MergeTagMenu
        variableGroups={variableGroups}
        onInsert={(path, fallback) => {
          editor.chain().focus().insertContent({ type: 'mergeTag', attrs: { path, fallback } }).insertContent(' ').run();
        }}
      />
      {linkOpen && (
        <div className="absolute left-0 top-full z-40 mt-1 rounded-lg border border-border bg-popover p-1.5 shadow-lg">
          <div className="flex items-center gap-1">
            <input
              autoFocus
              value={linkUrl}
              onChange={(e) => setLinkUrl(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') applyLink(); if (e.key === 'Escape') setLinkOpen(false); }}
              placeholder="https://example.com"
              className="w-56 rounded-md border border-border/60 bg-background px-2 py-1 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
            />
            <button type="button" onClick={applyLink} disabled={linkHasToken}
              className="rounded-md bg-primary px-2 py-1 text-xs font-medium text-primary-foreground disabled:opacity-50">Set</button>
          </div>
          {linkHasToken && (
            // A {{token}} inside an href corrupts on editor reload (the chip
            // deserializer is attribute-blind) — dynamic links belong on buttons.
            <p className="mt-1 max-w-64 text-[11px] text-destructive">
              Merge tags can’t be used in text links — use a Button block’s link instead.
            </p>
          )}
        </div>
      )}
    </div>
  );
};

const TB: React.FC<{ active?: boolean; title: string; onClick: () => void; children: React.ReactNode }> = ({ active, title, onClick, children }) => (
  <button type="button" title={title} aria-label={title} aria-pressed={!!active} onClick={onClick}
    className={`flex h-7 w-7 items-center justify-center rounded transition-colors ${active ? 'bg-primary/15 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-foreground'}`}>
    {children}
  </button>
);

export default InlineRichText;
