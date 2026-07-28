import React from 'react';
import { CodeXml } from 'lucide-react';
import { useBuilderStore } from './builderStore';

/** CodeView is the full-height HTML source editor. It edits the document's
 *  single html block directly (the page guarantees that shape before mounting
 *  this view). The snippet becomes the email body; the compliance footer and
 *  Styles-panel settings still apply around it. */
export const CodeView: React.FC = () => {
  const blocks = useBuilderStore((s) => s.blocks);
  const patchBlock = useBuilderStore((s) => s.patchBlock);

  const blk = blocks.length === 1 && blocks[0].type === 'html' ? blocks[0] : null;
  if (!blk) return null;

  const text = blk.text ?? '';
  const lines = text === '' ? 0 : text.split('\n').length;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b border-border bg-muted/40 px-4 py-1.5 text-[11px] text-muted-foreground">
        <CodeXml className="h-3.5 w-3.5 shrink-0" />
        Raw HTML for the email body. Tables, inline styles and images are kept on save; scripts are stripped.
        The footer and the Email tab’s styles still apply around this.
        <span className="ml-auto shrink-0 tabular-nums">{lines} lines · {text.length} chars</span>
      </div>
      <textarea
        value={text}
        onChange={(e) => patchBlock(blk.id, { text: e.target.value }, `html:${blk.id}`)}
        spellCheck={false}
        aria-label="Email HTML source"
        className="min-h-0 flex-1 resize-none bg-background p-4 font-mono text-xs leading-relaxed text-foreground focus:outline-none"
      />
    </div>
  );
};

export default CodeView;
