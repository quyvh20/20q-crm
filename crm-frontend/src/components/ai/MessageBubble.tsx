import React, { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { vscDarkPlus } from 'react-syntax-highlighter/dist/esm/styles/prism';
import { Check, Copy } from 'lucide-react';
import type { ChatMessage } from './chatTypes';

interface Props {
  message: ChatMessage;
}

export default function MessageBubble({ message }: Props) {
  const isUser = message.role === 'user';

  if (isUser) {
    return (
      <div style={styles.userRow}>
        <div style={styles.userBubble}>
          {message.content}
        </div>
        <style>{bubbleCSS}</style>
      </div>
    );
  }

  return (
    <div style={styles.assistantRow}>
      <div style={styles.avatarDot}>✦</div>
      <div style={styles.assistantBubble} className="ai-markdown-content">
        {message.content ? (
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
                  <code className={className} style={styles.inlineCode} {...props}>
                    {children}
                  </code>
                );
              }
            }}
          >
            {message.content}
          </ReactMarkdown>
        ) : (
          <span style={styles.placeholder}>Thinking…</span>
        )}
      </div>
      <style>{bubbleCSS}</style>
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
    <div style={styles.codeBlockWrapper}>
      <div style={styles.codeBlockHeader}>
        <span style={styles.codeLanguage}>{language}</span>
        <button onClick={handleCopy} style={styles.copyButton} title="Copy code">
          {copied ? <Check size={13} color="#10b981" /> : <Copy size={13} color="#a1a1aa" />}
          {copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <SyntaxHighlighter
        style={vscDarkPlus}
        language={language}
        PreTag="div"
        customStyle={{ margin: 0, borderRadius: '0 0 6px 6px', fontSize: 13, background: '#1e1e1e' }}
      >
        {value}
      </SyntaxHighlighter>
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  userRow: {
    display: 'flex',
    justifyContent: 'flex-end',
    marginBottom: 6,
  },
  userBubble: {
    maxWidth: '80%',
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    color: '#fff',
    borderRadius: '18px 18px 4px 18px',
    padding: '8px 14px',
    fontSize: 13,
    fontWeight: 500,
    lineHeight: 1.5,
    wordBreak: 'break-word',
  },
  assistantRow: {
    display: 'flex',
    alignItems: 'flex-start',
    gap: 8,
    marginBottom: 6,
  },
  avatarDot: {
    width: 22,
    height: 22,
    borderRadius: '50%',
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    fontSize: 10,
    color: '#fff',
    flexShrink: 0,
    marginTop: 2,
  },
  assistantBubble: {
    flex: 1,
    background: 'var(--bubble-bg, rgba(0,0,0,0.04))',
    borderRadius: '4px 18px 18px 18px',
    padding: '10px 14px',
    fontSize: 14,
    color: 'var(--foreground)',
    lineHeight: 1.6,
    wordBreak: 'break-word',
    overflowX: 'auto',
  },
  placeholder: {
    color: 'var(--muted-foreground)',
    fontStyle: 'italic',
  },
  inlineCode: {
    background: 'rgba(115, 115, 115, 0.15)',
    padding: '2px 4px',
    borderRadius: 4,
    fontFamily: 'monospace',
    fontSize: '0.9em',
    color: '#ef4444',
  },
  codeBlockWrapper: {
    margin: '12px 0',
    borderRadius: 6,
    overflow: 'hidden',
    border: '1px solid #3f3f46',
  },
  codeBlockHeader: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    background: '#2d2d2d',
    padding: '6px 12px',
  },
  codeLanguage: {
    color: '#a1a1aa',
    fontSize: 11,
    textTransform: 'uppercase',
    fontWeight: 600,
    letterSpacing: '0.05em',
  },
  copyButton: {
    display: 'flex',
    alignItems: 'center',
    gap: 4,
    background: 'transparent',
    border: 'none',
    color: '#a1a1aa',
    fontSize: 11,
    cursor: 'pointer',
  },
};

const bubbleCSS = `
  @keyframes fadeSlide {
    from { opacity: 0; transform: translateY(6px); }
    to   { opacity: 1; transform: translateY(0); }
  }
  
  .ai-markdown-content p { margin-top: 0; margin-bottom: 10px; }
  .ai-markdown-content p:last-child { margin-bottom: 0; }
  .ai-markdown-content ul, .ai-markdown-content ol { padding-left: 20px; margin-bottom: 10px; }
  .ai-markdown-content li { margin-bottom: 2px; }
  .ai-markdown-content h1, .ai-markdown-content h2, .ai-markdown-content h3, .ai-markdown-content h4 { 
    font-weight: 700; margin-top: 16px; margin-bottom: 8px; color: var(--foreground); 
  }
  .ai-markdown-content h1 { font-size: 1.3rem; }
  .ai-markdown-content h2 { font-size: 1.15rem; }
  .ai-markdown-content h3 { font-size: 1.05rem; }
  .ai-markdown-content table { 
    border-collapse: collapse; width: 100%; margin-bottom: 12px; 
    border-radius: 6px; overflow: hidden; border: 1px solid var(--border);
  }
  .ai-markdown-content th, .ai-markdown-content td { 
    border: 1px solid var(--border); padding: 6px 10px; font-size: 13px; text-align: left;
  }
  .ai-markdown-content th { background: rgba(0,0,0,0.03); font-weight: 600; }
  .ai-markdown-content a { color: #f59e0b; text-decoration: none; font-weight: 500; }
  .ai-markdown-content a:hover { text-decoration: underline; }
`;
