import React from 'react';

export interface CommandEvent {
  type: 'thinking' | 'planning' | 'tool_result' | 'response' | 'confirm' | 'navigate' | 'form' | 'error' | 'done';
  message?: string;
  tool?: string;
  data?: unknown;
  done?: boolean;
}

export interface ChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  events?: CommandEvent[];
  confirmPending?: ConfirmPayload;
  formPending?: FormPayload;
  navigatePending?: NavigatePayload;
  timestamp: Date;
}

export interface ConfirmPayload {
  tool: string;
  args: Record<string, unknown>;
  summary: string;
}

export interface FormPayload {
  form_type: 'contact' | 'deal';
  // contact prefills
  prefill_name?: string;
  prefill_email?: string;
  // deal prefills
  prefill_title?: string;
  prefill_value?: number;
}

export interface NavigatePayload {
  path: string;
  label: string;
}

// ── Markdown renderer (simple, no deps) ─────────────────────────────────────

export function renderMarkdown(text: string): React.ReactNode {
  const lines = text.split('\n');
  const nodes: React.ReactNode[] = [];
  let i = 0;

  const bold = (s: string): React.ReactNode => {
    const parts = s.split(/\*\*(.*?)\*\*/g);
    return parts.map((p, j) => j % 2 === 1 ? <strong key={j}>{p}</strong> : p);
  };

  while (i < lines.length) {
    const line = lines[i];
    if (line.startsWith('### ')) {
      nodes.push(<h3 key={i} style={styles.h3}>{line.slice(4)}</h3>);
    } else if (line.startsWith('## ')) {
      nodes.push(<h2 key={i} style={styles.h2}>{line.slice(3)}</h2>);
    } else if (line.startsWith('# ')) {
      nodes.push(<h2 key={i} style={styles.h2}>{line.slice(2)}</h2>);
    } else if (line.startsWith('- ') || line.startsWith('• ')) {
      nodes.push(<li key={i} style={styles.li}>{bold(line.slice(2))}</li>);
    } else if (/^\d+\. /.test(line)) {
      nodes.push(<li key={i} style={styles.li}>{bold(line.replace(/^\d+\. /, ''))}</li>);
    } else if (line.trim() === '') {
      nodes.push(<br key={i} />);
    } else {
      nodes.push(<p key={i} style={styles.p}>{bold(line)}</p>);
    }
    i++;
  }
  return <>{nodes}</>;
}

const styles: Record<string, React.CSSProperties> = {
  h2: { fontSize: 15, fontWeight: 700, margin: '8px 0 4px', color: 'inherit' },
  h3: { fontSize: 13, fontWeight: 600, margin: '6px 0 2px', color: 'inherit' },
  p: { margin: '2px 0', lineHeight: 1.55 },
  li: { margin: '2px 0 2px 16px', lineHeight: 1.55 },
};
