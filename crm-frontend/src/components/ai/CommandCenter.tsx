import { useState, useEffect, useRef } from 'react';
import { sendCommand, type CommandEvent } from '../../lib/api';
import CommandEventCard from './CommandEventCard';

const SLASH_COMMANDS: Record<string, string> = {
  '/followup':  'Draft follow-up emails for all deals inactive 7+ days',
  '/summary':   'Give me a pipeline summary and key actions for this week',
  '/tasks':     'What are my overdue and due-today tasks?',
  '/coach':     'Which of my deals has the highest chance of closing this month and why?',
  '/report':    'Create a brief performance summary I can share with my manager',
};

interface PageSuggestion {
  label: string;
  icon: string;
  command: string;
}

const PAGE_SUGGESTIONS: Record<string, PageSuggestion[]> = {
  deals: [
    { label: 'Stale deals this week', icon: '⚠️', command: 'Find all deals with no activity in the last 7 days' },
    { label: 'Pipeline summary', icon: '📊', command: 'Give me a pipeline summary' },
    { label: 'Deals near closing', icon: '🏆', command: 'Which deals are most likely to close this month?' },
  ],
  contacts: [
    { label: 'Contacts without deals', icon: '👤', command: 'Find contacts that have no associated deals' },
    { label: 'Last contacted 7d+', icon: '📞', command: 'Which contacts have not been contacted in the last 7 days?' },
    { label: 'Top value contacts', icon: '⭐', command: 'Who are my highest value contacts?' },
  ],
  default: [
    { label: 'Month summary', icon: '📈', command: 'Give me a summary of this month sales performance' },
    { label: 'My tasks today', icon: '🎯', command: 'What tasks are due today?' },
    { label: 'AI recommendations', icon: '💡', command: 'What should I focus on today to close more deals?' },
  ],
};

export default function CommandCenter() {
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState('');
  const [events, setEvents] = useState<CommandEvent[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [showSlash, setShowSlash] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  // Cmd+K / Ctrl+K keyboard shortcut
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
        e.preventDefault();
        setOpen(prev => !prev);
      }
      if (e.key === 'Escape' && open) {
        setOpen(false);
      }
    };
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [open]);

  // Focus input when opened
  useEffect(() => {
    if (open) {
      setTimeout(() => inputRef.current?.focus(), 100);
    }
  }, [open]);

  // Scroll to bottom on new events
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [events]);

  // Detect slash commands
  useEffect(() => {
    setShowSlash(input === '/');
  }, [input]);

  const currentPage = window.location.pathname.split('/')[1] || 'default';
  const suggestions = PAGE_SUGGESTIONS[currentPage] || PAGE_SUGGESTIONS.default;

  const submit = (text?: string) => {
    const msg = (text || input).trim();
    if (!msg || streaming) return;

    // Expand slash commands
    const expanded = SLASH_COMMANDS[msg] || msg;

    setInput('');
    setStreaming(true);
    setEvents([]);
    setShowSlash(false);

    sendCommand(
      expanded,
      { page: currentPage },
      (event) => {
        if (event.type !== 'done') {
          setEvents(prev => [...prev, event]);
        }
      },
      () => setStreaming(false),
      (err) => {
        setEvents(prev => [...prev, { type: 'error', message: err }]);
        setStreaming(false);
      },
    );
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      submit();
    }
  };

  if (!open) {
    return (
      <button
        onClick={() => setOpen(true)}
        className="cc-fab"
        title="Command Center (Ctrl+K)"
        aria-label="Open Command Center"
      >
        <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/>
        </svg>
        <span className="cc-fab-label">⌘K</span>

        <style>{ccStyles}</style>
      </button>
    );
  }

  return (
    <>
      {/* Backdrop */}
      <div className="cc-backdrop" onClick={() => setOpen(false)} />

      {/* Modal */}
      <div className="cc-modal">
        {/* Header */}
        <div className="cc-header">
          <div className="cc-header-left">
            <span className="cc-bolt">⚡</span>
            <span className="cc-title">Command Center</span>
          </div>
          <div className="cc-header-right">
            <kbd className="cc-kbd">Esc</kbd>
            <button onClick={() => setOpen(false)} className="cc-close">✕</button>
          </div>
        </div>

        {/* Input */}
        <div className="cc-input-row">
          <input
            ref={inputRef}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Ask anything or type / for commands..."
            className="cc-input"
            disabled={streaming}
          />
          <button
            onClick={() => submit()}
            disabled={!input.trim() || streaming}
            className="cc-submit"
          >
            {streaming ? (
              <span className="cc-spin" />
            ) : (
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M22 2 11 13M22 2 15 22l-4-9-9-4 20-7z"/>
              </svg>
            )}
          </button>
        </div>

        {/* Slash commands dropdown */}
        {showSlash && (
          <div className="cc-slash-menu">
            {Object.entries(SLASH_COMMANDS).map(([cmd, desc]) => (
              <button
                key={cmd}
                onClick={() => { setInput(cmd); submit(cmd); }}
                className="cc-slash-item"
              >
                <span className="cc-slash-cmd">{cmd}</span>
                <span className="cc-slash-desc">{desc}</span>
              </button>
            ))}
          </div>
        )}

        {/* Content */}
        <div className="cc-body" ref={scrollRef}>
          {events.length === 0 && !streaming && (
            <div className="cc-empty">
              <p className="cc-empty-title">What can I help with?</p>
              <div className="cc-chips">
                {suggestions.map(s => (
                  <button
                    key={s.command}
                    onClick={() => submit(s.command)}
                    className="cc-chip"
                  >
                    <span>{s.icon}</span>
                    {s.label}
                  </button>
                ))}
              </div>
            </div>
          )}

          {events.map((ev, i) => (
            <CommandEventCard key={i} event={ev} />
          ))}

          {streaming && events.length > 0 && events[events.length - 1]?.type !== 'thinking' && (
            <div className="cc-event cc-thinking">
              <div className="cc-event-icon"><span className="cc-pulse">⏳</span></div>
              <span className="cc-event-text">Processing...</span>
            </div>
          )}
        </div>
      </div>

      <style>{ccStyles}</style>
    </>
  );
}

const ccStyles = `
  .cc-fab {
    position: fixed; bottom: 28px; right: 28px; z-index: 1000;
    width: 56px; height: 56px; border-radius: 16px;
    background: linear-gradient(135deg, #f59e0b, #ef4444);
    border: none; cursor: pointer; color: white;
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 1px;
    box-shadow: 0 8px 24px rgba(245,158,11,0.4);
    transition: transform 0.2s, box-shadow 0.2s;
  }
  .cc-fab:hover { transform: scale(1.08); box-shadow: 0 12px 32px rgba(245,158,11,0.5); }
  .cc-fab-label { font-size: 9px; font-weight: 700; letter-spacing: 0.05em; }

  .cc-backdrop {
    position: fixed; inset: 0; z-index: 1000;
    background: rgba(0,0,0,0.5); backdrop-filter: blur(4px);
    animation: ccFadeIn 0.15s ease;
  }
  @keyframes ccFadeIn { from { opacity: 0; } }

  .cc-modal {
    position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%);
    z-index: 1001; width: 680px; max-width: 95vw; max-height: 85vh;
    background: var(--background, #fff); border: 1px solid var(--border, #e5e7eb);
    border-radius: 20px; box-shadow: 0 32px 80px rgba(0,0,0,0.3);
    display: flex; flex-direction: column; overflow: hidden;
    animation: ccSlideIn 0.2s ease;
  }
  @keyframes ccSlideIn { from { opacity: 0; transform: translate(-50%, -48%) scale(0.97); } }

  .cc-header {
    display: flex; align-items: center; justify-content: space-between;
    padding: 12px 16px; border-bottom: 1px solid var(--border, #e5e7eb);
    background: linear-gradient(135deg, #f59e0b, #ef4444);
  }
  .cc-header-left { display: flex; align-items: center; gap: 8px; color: white; }
  .cc-header-right { display: flex; align-items: center; gap: 8px; }
  .cc-bolt { font-size: 18px; }
  .cc-title { font-weight: 700; font-size: 15px; color: white; }
  .cc-kbd { background: rgba(255,255,255,0.2); color: white; font-size: 11px; padding: 2px 6px; border-radius: 4px; font-family: monospace; }
  .cc-close { background: rgba(255,255,255,0.2); border: none; color: white; border-radius: 8px; padding: 4px 8px; cursor: pointer; font-size: 14px; }

  .cc-input-row { display: flex; gap: 8px; padding: 12px 16px; border-bottom: 1px solid var(--border, #e5e7eb); }
  .cc-input {
    flex: 1; border: 1px solid var(--border, #e5e7eb); border-radius: 12px;
    padding: 10px 14px; font-size: 14px; outline: none; background: var(--background,#fff);
    color: var(--foreground, #111); font-family: inherit;
  }
  .cc-input:focus { border-color: #f59e0b; box-shadow: 0 0 0 2px rgba(245,158,11,0.15); }
  .cc-submit {
    width: 40px; height: 40px; border-radius: 12px; border: none; cursor: pointer;
    background: linear-gradient(135deg, #f59e0b, #ef4444); color: white;
    display: flex; align-items: center; justify-content: center;
    transition: opacity 0.15s, transform 0.15s;
  }
  .cc-submit:disabled { opacity: 0.4; cursor: not-allowed; }
  .cc-submit:not(:disabled):hover { transform: scale(1.05); }
  .cc-spin { width: 14px; height: 14px; border: 2px solid rgba(255,255,255,0.4); border-top-color: white; border-radius: 50%; animation: spin 0.6s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }

  .cc-slash-menu { border-bottom: 1px solid var(--border, #e5e7eb); max-height: 200px; overflow-y: auto; }
  .cc-slash-item {
    display: flex; align-items: center; gap: 12px; padding: 8px 16px; width: 100%;
    border: none; background: none; cursor: pointer; text-align: left; font-size: 13px;
    transition: background 0.1s;
  }
  .cc-slash-item:hover { background: var(--accent, #f3f4f6); }
  .cc-slash-cmd { font-weight: 600; color: #f59e0b; min-width: 80px; font-family: monospace; }
  .cc-slash-desc { color: var(--muted-foreground, #6b7280); }

  .cc-body { flex: 1; overflow-y: auto; padding: 16px; display: flex; flex-direction: column; gap: 12px; min-height: 200px; max-height: 60vh; }

  .cc-empty { text-align: center; padding: 24px; }
  .cc-empty-title { font-size: 15px; font-weight: 600; color: var(--foreground, #111); margin-bottom: 16px; }
  .cc-chips { display: flex; flex-wrap: wrap; gap: 8px; justify-content: center; }
  .cc-chip {
    display: flex; align-items: center; gap: 6px; padding: 8px 14px; border-radius: 10px;
    border: 1px solid var(--border, #e5e7eb); background: var(--background, #fff);
    cursor: pointer; font-size: 13px; color: var(--foreground, #111);
    transition: all 0.15s;
  }
  .cc-chip:hover { border-color: #f59e0b; background: #fffbeb; color: #b45309; }

  .cc-event { display: flex; align-items: flex-start; gap: 10px; padding: 10px 12px; border-radius: 12px; }
  .cc-event-icon { font-size: 16px; flex-shrink: 0; margin-top: 1px; }
  .cc-event-text { font-size: 13px; color: var(--muted-foreground, #6b7280); }
  .cc-pulse { animation: pulse 1.5s infinite; display: inline-block; }
  @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }

  .cc-thinking { background: var(--accent, #f3f4f6); }
  .cc-planning { background: #fef3c7; }
  .cc-tool-result { background: #f0fdf4; border: 1px solid #bbf7d0; }
  .cc-tool-info { display: flex; flex-direction: column; gap: 2px; }
  .cc-tool-label { font-size: 11px; font-weight: 600; color: #166534; text-transform: uppercase; letter-spacing: 0.05em; }
  .cc-tool-summary { font-size: 13px; color: #15803d; }
  .cc-error { background: #fef2f2; border: 1px solid #fecaca; }
  .cc-error .cc-event-text { color: #dc2626; }
  .cc-response { background: var(--accent, #f3f4f6); }
  .cc-response-content { font-size: 14px; line-height: 1.6; color: var(--foreground, #111); white-space: pre-wrap; }
`;
