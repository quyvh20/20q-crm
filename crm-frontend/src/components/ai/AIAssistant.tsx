import { useState, useRef, useEffect } from 'react';
import { streamChat } from '../../lib/api';

interface Message {
  role: 'user' | 'assistant';
  content: string;
  streaming?: boolean;
}

export default function AIAssistant() {
  const [isOpen, setIsOpen] = useState(false);
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (isOpen) {
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [isOpen]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  const send = async () => {
    const text = input.trim();
    if (!text || loading) return;
    setInput('');
    setError('');

    setMessages(prev => [...prev, { role: 'user', content: text }]);
    setLoading(true);

    // Add streaming assistant placeholder
    setMessages(prev => [...prev, { role: 'assistant', content: '', streaming: true }]);

    await streamChat(
      text,
      (chunk) => {
        setMessages(prev => {
          const next = [...prev];
          const last = next[next.length - 1];
          if (last?.role === 'assistant') {
            next[next.length - 1] = { ...last, content: last.content + chunk };
          }
          return next;
        });
      },
      () => {
        setMessages(prev => {
          const next = [...prev];
          const last = next[next.length - 1];
          if (last?.role === 'assistant') {
            next[next.length - 1] = { ...last, streaming: false };
          }
          return next;
        });
        setLoading(false);
      },
      (err) => {
        setError(err);
        setMessages(prev => prev.filter((_, i) => i < prev.length - 1));
        setLoading(false);
      },
    );
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      send();
    }
  };

  return (
    <>
      {/* Floating button */}
      <button
        id="ai-assistant-toggle"
        onClick={() => setIsOpen(v => !v)}
        className="ai-fab"
        title="AI Assistant"
        aria-label="Open AI Assistant"
      >
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
          <path d="M12 2a8 8 0 0 1 8 8c0 5.25-8 12-8 12S4 15.25 4 10a8 8 0 0 1 8-8z"/>
          <circle cx="12" cy="10" r="3"/>
        </svg>
        <span className="ai-fab-label">AI</span>
      </button>

      {/* Chat panel */}
      {isOpen && (
        <div className="ai-panel" id="ai-chat-panel">
          {/* Header */}
          <div className="ai-panel-header">
            <div className="ai-panel-title">
              <div className="ai-status-dot" />
              <span>CRM Assistant</span>
            </div>
            <button onClick={() => setIsOpen(false)} className="ai-close-btn" aria-label="Close">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M18 6 6 18M6 6l12 12"/></svg>
            </button>
          </div>

          {/* Messages */}
          <div className="ai-messages">
            {messages.length === 0 && (
              <div className="ai-empty">
                <p>👋 Ask me anything about your pipeline, contacts, or deals.</p>
                <div className="ai-suggestions">
                  {['Summarize top deals', 'Who are my hottest leads?', 'Draft a follow-up email'].map(s => (
                    <button key={s} className="ai-suggestion" onClick={() => { setInput(s); inputRef.current?.focus(); }}>{s}</button>
                  ))}
                </div>
              </div>
            )}
            {messages.map((m, i) => (
              <div key={i} className={`ai-message ai-message-${m.role}`}>
                {m.role === 'assistant' && (
                  <div className="ai-avatar">AI</div>
                )}
                <div className="ai-bubble">
                  <span style={{ whiteSpace: 'pre-wrap' }}>{m.content}</span>
                  {m.streaming && <span className="ai-cursor">▋</span>}
                </div>
              </div>
            ))}

            {error && (
              <div className="ai-error">⚠️ {error}</div>
            )}
            <div ref={bottomRef} />
          </div>

          {/* Input */}
          <div className="ai-input-area">
            <textarea
              ref={inputRef}
              id="ai-chat-input"
              value={input}
              onChange={e => setInput(e.target.value)}
              onKeyDown={handleKeyDown}
              placeholder="Ask anything… (Enter to send)"
              rows={2}
              disabled={loading}
              className="ai-textarea"
            />
            <button
              id="ai-send-btn"
              onClick={send}
              disabled={!input.trim() || loading}
              className="ai-send-btn"
              aria-label="Send message"
            >
              {loading ? (
                <span className="ai-spinner" />
              ) : (
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M22 2 11 13M22 2 15 22l-4-9-9-4 20-7z"/></svg>
              )}
            </button>
          </div>
        </div>
      )}

      <style>{`
        .ai-fab {
          position: fixed; bottom: 28px; right: 28px; z-index: 1000;
          width: 56px; height: 56px; border-radius: 16px;
          background: linear-gradient(135deg, #6366f1, #8b5cf6);
          border: none; cursor: pointer; color: white;
          display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 1px;
          box-shadow: 0 8px 24px rgba(99,102,241,0.4);
          transition: transform 0.2s, box-shadow 0.2s;
        }
        .ai-fab:hover { transform: scale(1.08); box-shadow: 0 12px 32px rgba(99,102,241,0.5); }
        .ai-fab-label { font-size: 9px; font-weight: 700; letter-spacing: 0.05em; }

        .ai-panel {
          position: fixed; bottom: 96px; right: 24px; z-index: 999;
          width: 380px; max-height: 560px;
          background: var(--background, #fff); border: 1px solid var(--border, #e5e7eb);
          border-radius: 20px; box-shadow: 0 24px 64px rgba(0,0,0,0.18);
          display: flex; flex-direction: column; overflow: hidden;
          animation: aiPanelIn 0.2s ease;
        }
        @keyframes aiPanelIn { from { opacity:0; transform: translateY(12px) scale(0.97); } }

        .ai-panel-header {
          display: flex; align-items: center; justify-content: space-between;
          padding: 14px 16px; border-bottom: 1px solid var(--border, #e5e7eb);
          background: linear-gradient(135deg, #6366f1, #8b5cf6);
        }
        .ai-panel-title { display: flex; align-items: center; gap: 8px; color: white; font-weight: 600; font-size: 14px; }
        .ai-status-dot { width: 8px; height: 8px; border-radius: 50%; background: #4ade80; box-shadow: 0 0 8px #4ade80; }
        .ai-close-btn { background: rgba(255,255,255,0.2); border: none; color: white; border-radius: 8px; padding: 4px; cursor: pointer; display: flex; }
        .ai-close-btn:hover { background: rgba(255,255,255,0.3); }

        .ai-messages { flex: 1; overflow-y: auto; padding: 12px; display: flex; flex-direction: column; gap: 10px; min-height: 0; }

        .ai-empty { text-align: center; padding: 20px 12px; color: var(--muted-foreground, #6b7280); font-size: 13px; }
        .ai-suggestions { display: flex; flex-direction: column; gap: 6px; margin-top: 12px; }
        .ai-suggestion {
          background: var(--muted, #f3f4f6); border: 1px solid var(--border, #e5e7eb); border-radius: 10px;
          padding: 7px 10px; font-size: 12px; cursor: pointer; text-align: left; color: #374151;
          transition: background 0.15s;
        }
        .ai-suggestion:hover { background: #e0e7ff; border-color: #6366f1; color: #4f46e5; }

        .ai-message { display: flex; align-items: flex-start; gap: 8px; }
        .ai-message-user { flex-direction: row-reverse; }
        .ai-avatar { width: 26px; height: 26px; border-radius: 8px; background: linear-gradient(135deg,#6366f1,#8b5cf6); color:white; font-size:10px; font-weight:700; display:flex; align-items:center; justify-content:center; flex-shrink:0; }
        .ai-bubble {
          max-width: 85%; padding: 8px 12px; border-radius: 14px; font-size: 13px; line-height: 1.5;
          background: var(--muted, #f3f4f6); color: var(--foreground, #111);
        }
        .ai-message-user .ai-bubble { background: linear-gradient(135deg,#6366f1,#8b5cf6); color: white; border-radius: 14px 4px 14px 14px; }
        .ai-message-assistant .ai-bubble { border-radius: 4px 14px 14px 14px; }
        .ai-cursor { animation: blink 1s infinite; font-size: 14px; color: #6366f1; }
        @keyframes blink { 50% { opacity: 0; } }
        .ai-error { background: #fef2f2; border: 1px solid #fecaca; border-radius: 10px; padding: 8px 12px; font-size: 12px; color: #dc2626; }

        .ai-input-area { display: flex; gap: 8px; padding: 10px 12px; border-top: 1px solid var(--border, #e5e7eb); align-items: flex-end; }
        .ai-textarea {
          flex: 1; resize: none; border: 1px solid var(--border, #e5e7eb); border-radius: 12px;
          padding: 8px 12px; font-size: 13px; line-height: 1.5; background: var(--background, #fff);
          color: var(--foreground, #111); font-family: inherit; outline: none;
          transition: border-color 0.15s;
        }
        .ai-textarea:focus { border-color: #6366f1; box-shadow: 0 0 0 2px rgba(99,102,241,0.15); }
        .ai-send-btn {
          width: 36px; height: 36px; border-radius: 10px; border: none; cursor: pointer;
          background: linear-gradient(135deg,#6366f1,#8b5cf6); color: white;
          display: flex; align-items: center; justify-content: center; flex-shrink: 0;
          transition: opacity 0.15s, transform 0.15s;
        }
        .ai-send-btn:disabled { opacity: 0.4; cursor: not-allowed; transform: none; }
        .ai-send-btn:not(:disabled):hover { transform: scale(1.08); }
        .ai-spinner { width: 14px; height: 14px; border: 2px solid rgba(255,255,255,0.4); border-top-color: white; border-radius: 50%; animation: spin 0.6s linear infinite; }
        @keyframes spin { to { transform: rotate(360deg); } }
      `}</style>
    </>
  );
}
