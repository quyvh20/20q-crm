import { useState, useEffect, useRef, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '../lib/auth';
import { sendCommand, endChatSession, type WorkspaceContext } from '../lib/api';
import MessageBubble from '../components/ai/MessageBubble';
import ConfirmBanner from '../components/ai/ConfirmBanner';
import InlineForm from '../components/ai/InlineForm';
import type { ChatMessage, CommandEvent, ConfirmPayload, FormPayload, NavigatePayload } from '../components/ai/chatTypes';

// ── Session helpers ──────────────────────────────────────────────────────────

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
  } catch { /* quota exceeded */ }
}

// ── Role-based suggested prompts ─────────────────────────────────────────────

const ROLE_SUGGESTIONS: Record<string, { icon: string; label: string; command: string }[]> = {
  owner: [
    { icon: '📊', label: 'Org analytics', command: "Show me this month's sales performance" },
    { icon: '🏆', label: 'Top performers', command: 'Who are the top performing sales reps?' },
    { icon: '💰', label: 'Revenue forecast', command: 'Give me the revenue forecast for next quarter' },
    { icon: '🎫', label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  admin: [
    { icon: '📊', label: 'Pipeline health', command: 'What is the current pipeline health?' },
    { icon: '🔥', label: 'Deals at risk', command: 'Which deals are at risk of being lost?' },
    { icon: '📈', label: 'Monthly summary', command: "Give me a summary of this month's performance" },
    { icon: '🎫', label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  manager: [
    { icon: '👥', label: 'Team pipeline', command: 'Give me a pipeline summary for my team' },
    { icon: '⚠️', label: 'Stale deals', command: 'Find all deals with no activity in 7+ days' },
    { icon: '🎯', label: 'Coaching insights', command: 'Which deals need my attention this week?' },
    { icon: '🎫', label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  sales_rep: [
    { icon: '📋', label: 'My deals', command: 'Show me my active deals' },
    { icon: '📅', label: 'Tasks today', command: 'What tasks are due today?' },
    { icon: '📞', label: 'Follow-ups', command: 'Which of my contacts need a follow-up?' },
    { icon: '🎫', label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  viewer: [
    { icon: '📊', label: 'Pipeline overview', command: 'Show the pipeline overview' },
    { icon: '👤', label: 'Top contacts', command: 'Who are the highest value contacts?' },
  ],
};

// ── Main Page ────────────────────────────────────────────────────────────────

export default function AIPage() {
  const { currentRole, workspaces } = useAuth();
  const navigate = useNavigate();

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
  const bottomAnchorRef = useRef<HTMLDivElement>(null);

  useEffect(() => { saveSession(sessionId, messages); }, [sessionId, messages]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [messages, streaming, pendingForm, pendingConfirm]);

  useEffect(() => {
    if (pendingForm || pendingConfirm) {
      setTimeout(() => bottomAnchorRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' }), 100);
    }
  }, [pendingForm, pendingConfirm]);

  useEffect(() => { setTimeout(() => inputRef.current?.focus(), 150); }, []);

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

    const userMsg: ChatMessage = {
      id: crypto.randomUUID(),
      role: 'user',
      content: msg,
      timestamp: new Date(),
    };
    if (!isConfirmed) addMessage(userMsg);

    setStreaming(true);
    setPendingConfirm(null);
    setPendingForm(null);

    const historyMessages = messages
      .filter(m => m.content)
      .slice(-10)
      .map(m => ({ role: m.role, content: m.content }));

    const safetyTimer = setTimeout(() => {
      setStreaming(false);
      addMessage({ id: crypto.randomUUID(), role: 'assistant', content: '⚠️ Request timed out. Please try again.', timestamp: new Date() });
    }, 60_000);

    sendCommand(
      msg,
      sessionId,
      historyMessages,
      isConfirmed,
      confirmedPayload?.tool,
      confirmedPayload?.args,
      (event: CommandEvent) => {
        switch (event.type) {
          case 'thinking':
          case 'tool_result':
            break;
          case 'response':
            if (event.message) {
              setMessages(prev => {
                const lastIsAssistant = prev.length > 0 && prev[prev.length - 1].role === 'assistant';
                if (lastIsAssistant) {
                  const copy = [...prev];
                  copy[copy.length - 1] = { ...copy[copy.length - 1], content: event.message || '' };
                  return copy;
                }
                return [...prev, { id: crypto.randomUUID(), role: 'assistant', content: event.message || '', timestamp: new Date() }];
              });
            }
            break;
          case 'navigate': {
            const navData = event.data as NavigatePayload;
            if (navData?.path) {
              const navMsg = `🔗 Navigating to **${navData.label || navData.path}**…`;
              addMessage({ id: crypto.randomUUID(), role: 'assistant', content: navMsg, timestamp: new Date() });
              setTimeout(() => navigate(navData.path), 800);
            }
            break;
          }
          case 'form':
            setMessages(prev => {
              const last = prev[prev.length - 1];
              if (last && last.role === 'assistant' && !last.content) return prev.slice(0, -1);
              return prev;
            });
            setPendingForm(event.data as FormPayload);
            break;
          case 'confirm':
            setPendingConfirm(event.data as ConfirmPayload);
            break;
          case 'error':
            addMessage({ id: crypto.randomUUID(), role: 'assistant', content: `⚠️ ${event.message || 'Something went wrong.'}`, timestamp: new Date() });
            setStreaming(false);
            clearTimeout(safetyTimer);
            break;
        }
      },
      () => {
        setStreaming(false);
        clearTimeout(safetyTimer);
        setMessages(prev => {
          const last = prev[prev.length - 1];
          if (last && last.role === 'assistant' && !last.content) return prev.slice(0, -1);
          return prev;
        });
      },
      (err: string) => {
        addMessage({ id: crypto.randomUUID(), role: 'assistant', content: `⚠️ ${err}`, timestamp: new Date() });
        setStreaming(false);
        clearTimeout(safetyTimer);
      },
      workspaceContext,
    );
  }, [input, streaming, messages, sessionId, addMessage, updateLastAssistant, navigate]);

  const handleConfirm = (payload: ConfirmPayload) => {
    setPendingConfirm(null);
    const confirmText = payload.summary || 'Proceed with the action';
    addMessage({ id: crypto.randomUUID(), role: 'user', content: `Yes, please proceed: ${confirmText}`, timestamp: new Date() });
    sendMessage(confirmText, true, payload);
  };

  const handleCancel = () => {
    setPendingConfirm(null);
    addMessage({ id: crypto.randomUUID(), role: 'assistant', content: 'Got it — action cancelled. No changes were made.', timestamp: new Date() });
  };

  const handleNewChat = async () => {
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

  return (
    <div className="ai-page">
      {/* Top bar */}
      <header className="ai-page-header">
        <div className="ai-page-header-left">
          <span className="ai-page-logo">✦</span>
          <span className="ai-page-title">AI Assistant</span>
          <span className="ai-page-badge">{currentRole}</span>
        </div>
        <button className="ai-page-new-chat" onClick={handleNewChat} disabled={streaming}>
          ＋ New Chat
        </button>
      </header>

      {/* Scrollable body */}
      <div className="ai-page-body" ref={scrollRef}>
        <div className="ai-page-column">
          {/* Empty state */}
          {isEmpty && (
            <div className="ai-page-empty">
              <div className="ai-page-empty-icon">✦</div>
              <h1 className="ai-page-empty-title">How can I help you?</h1>
              <p className="ai-page-empty-sub">Ask anything about your CRM data, or create records with natural language.</p>
              <div className="ai-page-chips">
                {suggestions.map(s => (
                  <button
                    key={s.command}
                    className="ai-page-chip"
                    onClick={() => sendMessage(s.command)}
                    disabled={streaming}
                  >
                    <span className="ai-page-chip-icon">{s.icon}</span>
                    <span>{s.label}</span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {/* Messages */}
          {messages.map(msg => (
            <MessageBubble key={msg.id} message={msg} />
          ))}

          {/* Confirm banner */}
          {pendingConfirm && !streaming && (
            <ConfirmBanner
              payload={pendingConfirm}
              onConfirm={handleConfirm}
              onCancel={handleCancel}
            />
          )}

          {/* Inline form */}
          {pendingForm && !streaming && (
            <InlineForm
              payload={pendingForm}
              onSuccess={msg => {
                setPendingForm(null);
                addMessage({ id: crypto.randomUUID(), role: 'assistant', content: msg, timestamp: new Date() });
              }}
              onCancel={() => {
                setPendingForm(null);
                addMessage({ id: crypto.randomUUID(), role: 'assistant', content: 'No problem — form dismissed.', timestamp: new Date() });
              }}
            />
          )}

          {/* Streaming indicator */}
          {streaming && (
            <div className="ai-page-thinking">
              <span className="ai-page-dot" />
              <span className="ai-page-dot" />
              <span className="ai-page-dot" />
            </div>
          )}

          <div ref={bottomAnchorRef} style={{ minHeight: 1 }} />
        </div>
      </div>

      {/* Input bar */}
      <div className="ai-page-input-area">
        <div className="ai-page-input-wrap">
          <textarea
            ref={inputRef}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message AI Assistant…"
            rows={1}
            disabled={streaming}
            className="ai-page-textarea"
          />
          <button
            className="ai-page-send"
            onClick={() => sendMessage()}
            disabled={!input.trim() || streaming}
            title="Send"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <line x1="22" y1="2" x2="11" y2="13" /><polygon points="22 2 15 22 11 13 2 9 22 2" />
            </svg>
          </button>
        </div>
        <p className="ai-page-disclaimer">AI can make mistakes. Verify important information.</p>
      </div>

      <style>{pageCSS}</style>
    </div>
  );
}

// ── Styles ────────────────────────────────────────────────────────────────────

const pageCSS = `
  .ai-page {
    display: flex;
    flex-direction: column;
    /* Counteract the p-6 (24px) padding from AppLayout's <main> */
    margin: -24px;
    height: calc(100% + 48px);
    min-height: 0;
    background: var(--background);
  }

  /* ── Header ── */
  .ai-page-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 24px;
    border-bottom: 1px solid var(--border);
    background: var(--card);
    flex-shrink: 0;
  }
  .ai-page-header-left {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .ai-page-logo {
    font-size: 18px;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
  }
  .ai-page-title {
    font-weight: 700;
    font-size: 16px;
    color: var(--foreground);
  }
  .ai-page-badge {
    font-size: 10px;
    font-weight: 600;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    color: #fff;
    border-radius: 10px;
    padding: 2px 8px;
    text-transform: capitalize;
  }
  .ai-page-new-chat {
    background: var(--background);
    border: 1px solid var(--border);
    color: var(--foreground);
    border-radius: 8px;
    padding: 6px 14px;
    font-size: 12px;
    font-weight: 600;
    cursor: pointer;
    transition: all 0.15s;
  }
  .ai-page-new-chat:hover {
    border-color: #f59e0b;
    color: #b45309;
  }

  /* ── Scrollable body ── */
  .ai-page-body {
    flex: 1;
    overflow-y: auto;
    padding: 24px 16px 16px;
  }
  .ai-page-column {
    max-width: 768px;
    margin: 0 auto;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }

  /* ── Empty state ── */
  .ai-page-empty {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 80px 20px 40px;
    text-align: center;
  }
  .ai-page-empty-icon {
    font-size: 48px;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    -webkit-background-clip: text;
    -webkit-text-fill-color: transparent;
    margin-bottom: 16px;
  }
  .ai-page-empty-title {
    font-size: 28px;
    font-weight: 700;
    color: var(--foreground);
    margin: 0 0 8px;
  }
  .ai-page-empty-sub {
    font-size: 15px;
    color: var(--muted-foreground);
    margin: 0 0 32px;
    max-width: 420px;
  }
  .ai-page-chips {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 10px;
    max-width: 480px;
    width: 100%;
  }
  .ai-page-chip {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 14px 18px;
    border-radius: 12px;
    border: 1px solid var(--border);
    background: var(--card);
    cursor: pointer;
    font-size: 14px;
    font-weight: 500;
    color: var(--foreground);
    text-align: left;
    transition: all 0.15s;
  }
  .ai-page-chip:hover {
    border-color: #f59e0b;
    background: rgba(245, 158, 11, 0.04);
    transform: translateY(-1px);
    box-shadow: 0 2px 8px rgba(245, 158, 11, 0.1);
  }
  .ai-page-chip-icon {
    font-size: 20px;
    flex-shrink: 0;
  }

  /* ── Thinking dots ── */
  .ai-page-thinking {
    display: flex;
    gap: 5px;
    padding: 12px 8px;
    align-items: center;
  }
  .ai-page-dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #f59e0b;
    animation: aip-blink 1s ease infinite;
  }
  .ai-page-dot:nth-child(2) { animation-delay: 0.15s; }
  .ai-page-dot:nth-child(3) { animation-delay: 0.3s; }

  /* ── Input area ── */
  .ai-page-input-area {
    border-top: 1px solid var(--border);
    padding: 16px;
    background: var(--card);
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    align-items: center;
  }
  .ai-page-input-wrap {
    max-width: 768px;
    width: 100%;
    display: flex;
    align-items: flex-end;
    gap: 10px;
    background: var(--background);
    border: 1px solid var(--border);
    border-radius: 16px;
    padding: 10px 12px 10px 16px;
    transition: border-color 0.15s, box-shadow 0.15s;
  }
  .ai-page-input-wrap:focus-within {
    border-color: #f59e0b;
    box-shadow: 0 0 0 3px rgba(245, 158, 11, 0.1);
  }
  .ai-page-textarea {
    flex: 1;
    border: none;
    outline: none;
    background: transparent;
    color: var(--foreground);
    font-family: inherit;
    font-size: 15px;
    line-height: 1.5;
    resize: none;
    max-height: 150px;
    min-height: 24px;
  }
  .ai-page-textarea::placeholder {
    color: var(--muted-foreground);
  }
  .ai-page-send {
    width: 36px;
    height: 36px;
    border-radius: 10px;
    border: none;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    color: #fff;
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;
    transition: opacity 0.15s, transform 0.1s;
  }
  .ai-page-send:disabled {
    opacity: 0.35;
    cursor: default;
  }
  .ai-page-send:not(:disabled):hover {
    transform: scale(1.05);
  }
  .ai-page-disclaimer {
    font-size: 11px;
    color: var(--muted-foreground);
    margin: 6px 0 0;
    opacity: 0.6;
  }

  /* ── Animations ── */
  @keyframes aip-blink {
    0%, 100% { opacity: 0.3; transform: scale(0.8); }
    50%      { opacity: 1;   transform: scale(1); }
  }

  /* ── Override message bubble widths for full page ── */
  .ai-page-column .ai-markdown-content table {
    max-width: 100%;
  }

  /* ── Inline form gets more breathing room ── */
  .ai-page-column .ai-inline-form {
    max-width: 520px;
  }
`;
