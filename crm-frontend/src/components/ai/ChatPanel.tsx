import { useState, useEffect, useRef, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../../lib/auth';
import { sendCommand, endChatSession, type WorkspaceContext } from '../../lib/api';
import MessageBubble from './MessageBubble';
import ConfirmBanner from './ConfirmBanner';
import InlineForm from './InlineForm';
import type { ChatMessage, CommandEvent, ConfirmPayload, FormPayload, NavigatePayload } from './chatTypes';

// ── Session ID helpers ────────────────────────────────────────────────────────

function newSessionId(): string {
  return crypto.randomUUID();
}

function loadSession(): { sessionId: string; messages: ChatMessage[] } {
  try {
    const raw = sessionStorage.getItem('chat_session');
    if (raw) {
      const parsed = JSON.parse(raw);
      return {
        sessionId: parsed.sessionId || newSessionId(),
        messages: (parsed.messages || []).map((m: ChatMessage) => ({
          ...m,
          timestamp: new Date(m.timestamp),
        })),
      };
    }
  } catch { /* ignore */ }
  return { sessionId: newSessionId(), messages: [] };
}

function saveSession(sessionId: string, messages: ChatMessage[]) {
  try {
    sessionStorage.setItem('chat_session', JSON.stringify({ sessionId, messages }));
  } catch { /* quota exceeded, ignore */ }
}

// ── Role-based suggested prompts ─────────────────────────────────────────────

const ROLE_SUGGESTIONS: Record<string, { icon: string; label: string; command: string }[]> = {
  owner: [
    { icon: '📊', label: 'Org analytics', command: 'Show me this month\'s sales performance' },
    { icon: '🏆', label: 'Top performers', command: 'Who are the top performing sales reps?' },
    { icon: '💰', label: 'Revenue forecast', command: 'Give me the revenue forecast for next quarter' },
  ],
  admin: [
    { icon: '📊', label: 'Pipeline health', command: 'What is the current pipeline health?' },
    { icon: '🔥', label: 'Deals at risk', command: 'Which deals are at risk of being lost?' },
    { icon: '📈', label: 'Monthly summary', command: 'Give me a summary of this month\'s performance' },
  ],
  manager: [
    { icon: '👥', label: 'Team pipeline', command: 'Give me a pipeline summary for my team' },
    { icon: '⚠️', label: 'Stale deals', command: 'Find all deals with no activity in 7+ days' },
    { icon: '🎯', label: 'Coaching insights', command: 'Which deals need my attention this week?' },
  ],
  sales_rep: [
    { icon: '📋', label: 'My deals', command: 'Show me my active deals' },
    { icon: '📅', label: 'Tasks today', command: 'What tasks are due today?' },
    { icon: '📞', label: 'Follow-ups', command: 'Which of my contacts need a follow-up?' },
  ],
  viewer: [
    { icon: '📊', label: 'Pipeline overview', command: 'Show the pipeline overview' },
    { icon: '👤', label: 'Top contacts', command: 'Who are the highest value contacts?' },
  ],
};

// ── Main ChatPanel component ─────────────────────────────────────────────────

interface Props {
  open: boolean;
  onClose: () => void;
}

export default function ChatPanel({ open, onClose }: Props) {
  const { currentRole, workspaces } = useAuth();
  const navigate = useNavigate();

  // Build workspace context list for AI (enables context-switching hints)
  const workspaceContext: WorkspaceContext[] = (workspaces || []).map((w) => ({
    org_name: w.org_name,
    role: w.role,
  }));

  const [sessionId, setSessionId] = useState<string>(() => loadSession().sessionId);
  const [messages, setMessages] = useState<ChatMessage[]>(() => loadSession().messages);
  const [input, setInput] = useState('');
  const [streaming, setStreaming] = useState(false);
  const [pendingConfirm, setPendingConfirm] = useState<ConfirmPayload | null>(null);
  const [pendingForm, setPendingForm] = useState<FormPayload | null>(null);

  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  // Persist session to sessionStorage whenever it changes
  useEffect(() => {
    saveSession(sessionId, messages);
  }, [sessionId, messages]);

  // Scroll to bottom on new messages
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [messages, streaming]);

  // Focus input when panel opens
  useEffect(() => {
    if (open) {
      setTimeout(() => inputRef.current?.focus(), 150);
    }
  }, [open]);

  const addMessage = useCallback((msg: ChatMessage) => {
    setMessages(prev => [...prev, msg]);
  }, []);

  const updateLastAssistant = useCallback((updater: (prev: string) => string) => {
    setMessages(prev => {
      const copy = [...prev];
      for (let i = copy.length - 1; i >= 0; i--) {
        if (copy[i].role === 'assistant') {
          copy[i] = { ...copy[i], content: updater(copy[i].content) };
          return copy;
        }
      }
      return copy;
    });
  }, []);

  const sendMessage = useCallback((text?: string, isConfirmed?: boolean, confirmedPayload?: ConfirmPayload) => {
    const msg = (text || input).trim();
    if (!msg || streaming) return;
    if (!isConfirmed) setInput('');

    // Add user message to history
    const userMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: 'user',
      content: msg,
      timestamp: new Date(),
    };
    if (!isConfirmed) addMessage(userMsg);

    // Placeholder assistant message
    const assistantId = crypto.randomUUID();
    addMessage({ id: assistantId, role: 'assistant', content: '', timestamp: new Date() });

    setStreaming(true);
    setPendingConfirm(null);
    setPendingForm(null);

    // Build history (last 10 turns, excluding the placeholder we just added)
    const historyMessages = messages
      .filter(m => m.content) // exclude empty placeholder
      .slice(-10)
      .map(m => ({ role: m.role, content: m.content }));

    sendCommand(
      msg,
      sessionId,
      historyMessages,
      isConfirmed,
      confirmedPayload?.tool,
      confirmedPayload?.args,
      (event: CommandEvent) => {
        switch (event.type) {
          case 'response':
            updateLastAssistant(() => event.message || '');
            break;
          case 'navigate': {
            const navData = event.data as NavigatePayload;
            if (navData?.path) {
              updateLastAssistant(prev => prev + (prev ? '\n\n' : '') + `🔗 Navigating to **${navData.label || navData.path}**…`);
              setTimeout(() => navigate(navData.path), 800);
            }
            break;
          }
          case 'form':
            setPendingForm(event.data as FormPayload);
            break;
          case 'confirm':
            setPendingConfirm(event.data as ConfirmPayload);
            break;
          case 'error':
            updateLastAssistant(() => `⚠️ ${event.message || 'Something went wrong.'}`);
            break;
        }
      },
      () => setStreaming(false),
      (err: string) => {
        updateLastAssistant(() => `⚠️ ${err}`);
        setStreaming(false);
      },
      workspaceContext,
    );
  }, [input, streaming, messages, sessionId, addMessage, updateLastAssistant, navigate]);

  const handleConfirm = (payload: ConfirmPayload) => {
    setPendingConfirm(null);
    const confirmText = payload.summary || 'Proceed with the action';
    const userConfirmMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: 'user',
      content: `Yes, please proceed: ${confirmText}`,
      timestamp: new Date(),
    };
    addMessage(userConfirmMsg);
    sendMessage(confirmText, true, payload);
  };

  const handleCancel = () => {
    setPendingConfirm(null);
    // Add AI acknowledgment so the conversation doesn't feel broken
    addMessage({
      id: crypto.randomUUID(),
      role: 'assistant',
      content: 'Got it — action cancelled. No changes were made. Let me know if you need anything else.',
      timestamp: new Date(),
    });
  };

  const handleNewChat = async () => {
    // End the current session in DB
    try { await endChatSession(sessionId); } catch { /* non-critical */ }
    const nextId = newSessionId();
    setSessionId(nextId);
    setMessages([]);
    setPendingConfirm(null);
    setPendingForm(null);
    saveSession(nextId, []);
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  };

  const suggestions = ROLE_SUGGESTIONS[currentRole] || ROLE_SUGGESTIONS.viewer;
  const isEmpty = messages.length === 0 && !streaming;

  if (!open) return null;

  return (
    <>
      <aside style={styles.panel} aria-label="AI Assistant">
        {/* Header */}
        <div style={styles.header}>
          <div style={styles.headerLeft}>
            <span style={styles.headerIcon}>✦</span>
            <span style={styles.headerTitle}>AI Assistant</span>
            <span style={styles.roleBadge}>{currentRole}</span>
          </div>
          <div style={styles.headerRight}>
            <button
              style={styles.newChatBtn}
              onClick={handleNewChat}
              title="New Chat"
              disabled={streaming}
            >
              ＋ New Chat
            </button>
            <button style={styles.closeBtn} onClick={onClose} title="Close">✕</button>
          </div>
        </div>

        {/* Messages */}
        <div style={styles.body} ref={scrollRef}>
          {isEmpty && (
            <div style={styles.emptyState}>
              <div style={styles.emptyIcon}>✦</div>
              <p style={styles.emptyTitle}>How can I help you?</p>
              <p style={styles.emptySubtitle}>Ask anything about your CRM data.</p>
              <div style={styles.chips}>
                {suggestions.map(s => (
                  <button
                    key={s.command}
                    style={styles.chip}
                    onClick={() => sendMessage(s.command)}
                    disabled={streaming}
                    className="chat-chip"
                  >
                    <span>{s.icon}</span> {s.label}
                  </button>
                ))}
              </div>
            </div>
          )}

          {messages.map(msg => (
            <MessageBubble key={msg.id} message={msg} />
          ))}

          {/* Pending confirm banner */}
          {pendingConfirm && !streaming && (
            <ConfirmBanner
              payload={pendingConfirm}
              onConfirm={handleConfirm}
              onCancel={handleCancel}
            />
          )}

          {/* Pending inline form */}
          {pendingForm && !streaming && (
            <InlineForm
              payload={pendingForm}
              onSuccess={msg => {
                setPendingForm(null);
                addMessage({ id: crypto.randomUUID(), role: 'assistant', content: msg, timestamp: new Date() });
              }}
              onCancel={() => {
                setPendingForm(null);
                addMessage({
                  id: crypto.randomUUID(),
                  role: 'assistant',
                  content: 'No problem — form dismissed. Let me know if you want to try again.',
                  timestamp: new Date(),
                });
              }}
            />
          )}

          {/* Streaming indicator */}
          {streaming && (
            <div style={styles.thinkingRow}>
              <span style={styles.thinkingDot} />
              <span style={styles.thinkingDot} />
              <span style={styles.thinkingDot} />
            </div>
          )}
        </div>

        {/* Input */}
        <div style={styles.inputArea}>
          <textarea
            ref={inputRef}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Ask anything… (Shift+Enter for new line)"
            style={styles.textarea}
            rows={2}
            disabled={streaming}
          />
          <button
            style={{
              ...styles.sendBtn,
              opacity: (!input.trim() || streaming) ? 0.4 : 1,
            }}
            onClick={() => sendMessage()}
            disabled={!input.trim() || streaming}
            title="Send"
          >
            ➤
          </button>
        </div>
      </aside>

      <style>{panelCSS}</style>
    </>
  );
}

// ── Styles ───────────────────────────────────────────────────────────────────

const styles: Record<string, React.CSSProperties> = {
  panel: {
    position: 'fixed',
    top: 0,
    right: 0,
    bottom: 0,
    width: 340,
    zIndex: 900,
    background: 'var(--card, #fff)',
    borderLeft: '1px solid var(--border, #e5e7eb)',
    display: 'flex',
    flexDirection: 'column',
    boxShadow: '-8px 0 32px rgba(0,0,0,0.1)',
    animation: 'slideInRight 0.22s cubic-bezier(0.16,1,0.3,1)',
  },
  header: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    padding: '10px 14px',
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    flexShrink: 0,
  },
  headerLeft: { display: 'flex', alignItems: 'center', gap: 6 },
  headerIcon: { fontSize: 14, color: '#fff' },
  headerTitle: { fontWeight: 700, fontSize: 14, color: '#fff' },
  roleBadge: {
    fontSize: 10,
    fontWeight: 600,
    background: 'rgba(255,255,255,0.25)',
    color: '#fff',
    borderRadius: 10,
    padding: '1px 7px',
    textTransform: 'capitalize',
  },
  headerRight: { display: 'flex', alignItems: 'center', gap: 6 },
  newChatBtn: {
    background: 'rgba(255,255,255,0.2)',
    border: '1px solid rgba(255,255,255,0.35)',
    color: '#fff',
    borderRadius: 8,
    padding: '3px 10px',
    fontSize: 11,
    fontWeight: 600,
    cursor: 'pointer',
    transition: 'background 0.15s',
  },
  closeBtn: {
    background: 'rgba(255,255,255,0.2)',
    border: 'none',
    color: '#fff',
    borderRadius: 8,
    padding: '4px 8px',
    cursor: 'pointer',
    fontSize: 14,
  },
  body: {
    flex: 1,
    overflowY: 'auto',
    padding: '14px 14px 6px',
    display: 'flex',
    flexDirection: 'column',
    gap: 2,
  },
  emptyState: {
    textAlign: 'center',
    padding: '40px 16px 24px',
    flex: 1,
  },
  emptyIcon: {
    fontSize: 32,
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    WebkitBackgroundClip: 'text',
    WebkitTextFillColor: 'transparent',
    marginBottom: 12,
  },
  emptyTitle: { fontWeight: 700, fontSize: 16, margin: '0 0 6px' },
  emptySubtitle: { fontSize: 13, color: 'var(--muted-foreground)', margin: '0 0 20px' },
  chips: { display: 'flex', flexDirection: 'column', gap: 8, alignItems: 'stretch' },
  chip: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    padding: '9px 14px',
    borderRadius: 10,
    border: '1px solid var(--border)',
    background: 'var(--background)',
    cursor: 'pointer',
    fontSize: 13,
    textAlign: 'left',
    color: 'var(--foreground)',
    transition: 'all 0.15s',
    fontWeight: 500,
  },
  thinkingRow: {
    display: 'flex',
    gap: 4,
    padding: '8px 4px',
    alignItems: 'center',
  },
  thinkingDot: {
    width: 7,
    height: 7,
    borderRadius: '50%',
    background: '#f59e0b',
    display: 'inline-block',
  },
  inputArea: {
    borderTop: '1px solid var(--border)',
    padding: '10px 12px',
    display: 'flex',
    gap: 8,
    alignItems: 'flex-end',
    flexShrink: 0,
    background: 'var(--card)',
  },
  textarea: {
    flex: 1,
    border: '1px solid var(--border)',
    borderRadius: 12,
    padding: '8px 12px',
    fontSize: 13,
    resize: 'none',
    outline: 'none',
    background: 'var(--background)',
    color: 'var(--foreground)',
    fontFamily: 'inherit',
    lineHeight: 1.5,
    maxHeight: 120,
  },
  sendBtn: {
    width: 36,
    height: 36,
    borderRadius: 12,
    border: 'none',
    background: 'linear-gradient(135deg, #f59e0b, #ef4444)',
    color: '#fff',
    fontSize: 16,
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    flexShrink: 0,
    transition: 'opacity 0.15s',
  },
};

const panelCSS = `
  @keyframes slideInRight {
    from { transform: translateX(100%); opacity: 0; }
    to   { transform: translateX(0);    opacity: 1; }
  }
  @keyframes fadeSlide {
    from { opacity: 0; transform: translateY(6px); }
    to   { opacity: 1; transform: translateY(0); }
  }
  @keyframes blink {
    0%, 100% { opacity: 0.3; transform: scale(0.8); }
    50%       { opacity: 1;   transform: scale(1); }
  }
  span[style*="background: #f59e0b"][style*="border-radius: 50%"] {
    animation: blink 1s ease infinite;
  }
  span[style*="background: #f59e0b"][style*="border-radius: 50%"]:nth-child(2) { animation-delay: 0.15s; }
  span[style*="background: #f59e0b"][style*="border-radius: 50%"]:nth-child(3) { animation-delay: 0.3s; }
  .chat-chip:hover {
    border-color: #f59e0b !important;
    background: rgba(245,158,11,0.06) !important;
    color: #b45309 !important;
  }
  .chat-chip:hover span { filter: none; }
`;
