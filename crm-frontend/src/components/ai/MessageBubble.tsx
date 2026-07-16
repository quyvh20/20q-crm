import { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { Check, Copy, Sparkles } from 'lucide-react';
import type { ChatMessage } from './chatTypes';

interface Props {
  message: ChatMessage;
}

// Markdown descendant styling for assistant messages. ReactMarkdown renders raw
// tags we can't put classes on directly, so the parent styles them through
// arbitrary variants (token colors only — renders in both themes).
const markdownClasses = [
  '[&_p]:mt-0 [&_p]:mb-2.5 [&_p:last-child]:mb-0',
  '[&_ul]:mb-2.5 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:mb-2.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:mb-0.5',
  '[&_:is(h1,h2,h3,h4)]:mb-2 [&_:is(h1,h2,h3,h4)]:mt-4 [&_:is(h1,h2,h3,h4)]:font-bold [&_:is(h1,h2,h3,h4)]:text-foreground',
  '[&_h1]:text-[1.3rem] [&_h2]:text-[1.15rem] [&_h3]:text-[1.05rem]',
  '[&_table]:mb-3 [&_table]:w-full [&_table]:border-collapse [&_table]:overflow-hidden [&_table]:rounded-md [&_table]:border [&_table]:border-border',
  '[&_th]:border [&_th]:border-border [&_th]:bg-muted/50 [&_th]:px-2.5 [&_th]:py-1.5 [&_th]:text-left [&_th]:text-[13px] [&_th]:font-semibold',
  '[&_td]:border [&_td]:border-border [&_td]:px-2.5 [&_td]:py-1.5 [&_td]:text-left [&_td]:text-[13px]',
  '[&_a]:font-medium [&_a]:text-primary [&_a]:no-underline hover:[&_a]:underline',
].join(' ');

export default function MessageBubble({ message }: Props) {
  const isUser = message.role === 'user';

  if (isUser) {
    return (
      <div className="mb-1.5 flex justify-end">
        <div className="max-w-[80%] break-words rounded-2xl rounded-br-sm bg-primary px-3.5 py-2 text-[13px] font-medium leading-normal text-primary-foreground">
          {message.content}
        </div>
      </div>
    );
  }

  // Don't render empty assistant messages (streaming dots handle the loading state)
  if (!message.content) return null;

  return (
    <div className="mb-1.5 flex items-start gap-2">
      <div className="mt-0.5 flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full bg-primary text-primary-foreground">
        <Sparkles aria-hidden className="h-3 w-3" />
      </div>
      <div
        className={`ai-markdown-content min-w-0 flex-1 overflow-x-auto break-words rounded-2xl rounded-tl-sm bg-muted/60 px-3.5 py-2.5 text-sm leading-relaxed text-foreground ${markdownClasses}`}
      >
        <ReactMarkdown
          remarkPlugins={[remarkGfm]}
          components={{
            code({ node, inline, className, children, ...props }: any) {
              const match = /language-(\w+)/.exec(className || '');
              const language = match ? match[1] : '';
              if (!inline && match) {
                return <CodeBlock language={language} value={String(children).replace(/\n$/, '')} />;
              }
              return (
                <code className={`rounded bg-muted px-1 py-0.5 font-mono text-[0.9em] text-destructive ${className ?? ''}`} {...props}>
                  {children}
                </code>
              );
            }
          }}
        >
          {message.content}
        </ReactMarkdown>
      </div>
    </div>
  );
}

function CodeBlock({ language, value }: { language: string, value: string }) {
  const [copied, setCopied] = useState(false);

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch (e) {
      console.error('Copy failed', e);
    }
  };

  return (
    <div className="my-3 overflow-hidden rounded-lg border border-border">
      {/* The header commits to the highlighter's dark theme, so its text uses
          theme-neutral white opacities rather than palette hues. */}
      <div className="flex items-center justify-between bg-black/90 px-3 py-1.5">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-white/60">{language}</span>
        <button
          onClick={handleCopy}
          className="flex items-center gap-1 text-[11px] text-white/70 transition-colors hover:text-white"
          title="Copy code"
        >
          {copied ? <Check aria-hidden className="h-[13px] w-[13px] text-emerald-500" /> : <Copy aria-hidden className="h-[13px] w-[13px]" />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <SyntaxHighlighter
        style={vscDarkPlus}
        language={language}
        PreTag="div"
        // Structural resets the highlighter only accepts via its style API.
        customStyle={{ margin: 0, borderRadius: 0, fontSize: 13 }}
      >
        {value}
      </SyntaxHighlighter>
    </div>
  );
}
