import React from 'react';
import { renderMarkdown, type ChatMessage } from './chatTypes';

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
      <div style={styles.assistantBubble}>
        {message.content
          ? renderMarkdown(message.content)
          : <span style={styles.placeholder}>Thinking…</span>}
      </div>
      <style>{bubbleCSS}</style>
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
    padding: '8px 14px',
    fontSize: 13,
    color: 'var(--foreground)',
    lineHeight: 1.55,
    wordBreak: 'break-word',
  },
  placeholder: {
    color: 'var(--muted-foreground)',
    fontStyle: 'italic',
  },
};

const bubbleCSS = `
  @keyframes fadeSlide {
    from { opacity: 0; transform: translateY(6px); }
    to   { opacity: 1; transform: translateY(0); }
  }
`;
