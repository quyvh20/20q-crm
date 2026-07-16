import { useState, useEffect, useRef, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  BarChart3, Calendar, ClipboardList, DollarSign, Flame, Phone, Plus, Send, Sparkles,
  Target, Ticket, TrendingUp, TriangleAlert, Trophy, User, Users, type LucideIcon,
} from 'lucide-react';
import { useAuth } from '../lib/auth';
import { sendCommand, endChatSession, type WorkspaceContext } from '../lib/api';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
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

const ROLE_SUGGESTIONS: Record<string, { icon: LucideIcon; label: string; command: string }[]> = {
  owner: [
    { icon: BarChart3, label: 'Org analytics', command: "Show me this month's sales performance" },
    { icon: Trophy, label: 'Top performers', command: 'Who are the top performing sales reps?' },
    { icon: DollarSign, label: 'Revenue forecast', command: 'Give me the revenue forecast for next quarter' },
    { icon: Ticket, label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  admin: [
    { icon: BarChart3, label: 'Pipeline health', command: 'What is the current pipeline health?' },
    { icon: Flame, label: 'Deals at risk', command: 'Which deals are at risk of being lost?' },
    { icon: TrendingUp, label: 'Monthly summary', command: "Give me a summary of this month's performance" },
    { icon: Ticket, label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  manager: [
    { icon: Users, label: 'Team pipeline', command: 'Give me a pipeline summary for my team' },
    { icon: TriangleAlert, label: 'Stale deals', command: 'Find all deals with no activity in 7+ days' },
    { icon: Target, label: 'Coaching insights', command: 'Which deals need my attention this week?' },
    { icon: Ticket, label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  sales_rep: [
    { icon: ClipboardList, label: 'My deals', command: 'Show me my active deals' },
    { icon: Calendar, label: 'Tasks today', command: 'What tasks are due today?' },
    { icon: Phone, label: 'Follow-ups', command: 'Which of my contacts need a follow-up?' },
    { icon: Ticket, label: 'Create a ticket', command: 'Create a new support ticket' },
  ],
  viewer: [
    { icon: BarChart3, label: 'Pipeline overview', command: 'Show the pipeline overview' },
    { icon: User, label: 'Top contacts', command: 'Who are the highest value contacts?' },
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
  const [formQueue, setFormQueue] = useState<FormPayload[]>([]);

  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const bottomAnchorRef = useRef<HTMLDivElement>(null);

  useEffect(() => { saveSession(sessionId, messages); }, [sessionId, messages]);

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [messages, streaming, formQueue, pendingConfirm]);

  useEffect(() => {
    if (formQueue.length > 0 || pendingConfirm) {
      setTimeout(() => bottomAnchorRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' }), 100);
    }
  }, [formQueue, pendingConfirm]);

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
    setFormQueue([]);

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
            setFormQueue(prev => [...prev, event.data as FormPayload]);
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
    setFormQueue([]);
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
    // Counteract the p-6 (24px) padding from AppLayout's <main>.
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col bg-background">
      {/* Top bar */}
      <header className="flex shrink-0 items-center justify-between border-b border-border bg-card px-6 py-3">
        <div className="flex items-center gap-2">
          <Sparkles aria-hidden className="h-[18px] w-[18px] text-primary" />
          <span className="text-base font-bold text-foreground">AI Assistant</span>
          <Badge className="capitalize">{currentRole}</Badge>
        </div>
        <Button variant="outline" size="sm" onClick={handleNewChat} disabled={streaming}>
          <Plus aria-hidden />
          New Chat
        </Button>
      </header>

      {/* Scrollable body */}
      <div className="flex-1 overflow-y-auto px-4 pb-4 pt-6" ref={scrollRef}>
        <div className="mx-auto flex w-full max-w-3xl flex-col gap-1 [&_.ai-inline-form]:max-w-[520px] [&_.ai-markdown-content_table]:max-w-full">
          {/* Empty state */}
          {isEmpty && (
            <div className="flex flex-col items-center justify-center px-5 pb-10 pt-20 text-center">
              <Sparkles aria-hidden className="mb-4 h-12 w-12 text-primary" />
              <h1 className="mb-2 text-3xl font-bold text-foreground">How can I help you?</h1>
              <p className="mb-8 max-w-md text-[15px] text-muted-foreground">Ask anything about your CRM data, or create records with natural language.</p>
              <div className="grid w-full max-w-lg grid-cols-2 gap-2.5">
                {suggestions.map(s => (
                  <button
                    key={s.command}
                    className="flex items-center gap-2.5 rounded-xl border border-border bg-card px-4 py-3.5 text-left text-sm font-medium text-foreground transition-colors hover:border-primary/50 hover:bg-accent disabled:opacity-50"
                    onClick={() => sendMessage(s.command)}
                    disabled={streaming}
                  >
                    <s.icon aria-hidden className="h-5 w-5 shrink-0 text-primary" />
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

          {/* Inline form queue */}
          {formQueue.length > 0 && !streaming && (
            <>
              {formQueue.length > 1 && (
                <div className="mb-1 inline-flex items-center gap-1.5 self-start rounded-full border border-primary/25 bg-primary/10 px-3.5 py-1.5 text-xs font-semibold text-primary">
                  Form 1 of {formQueue.length} — {formQueue.length - 1} more after this
                </div>
              )}
              <InlineForm
                key={formQueue[0]?.form_type + '-' + formQueue[0]?.prefill_display_name + '-' + formQueue.length}
                payload={formQueue[0]}
                onSuccess={msg => {
                  addMessage({ id: crypto.randomUUID(), role: 'assistant', content: msg, timestamp: new Date() });
                  setFormQueue(prev => prev.slice(1));
                }}
                onCancel={() => {
                  const remaining = formQueue.length - 1;
                  setFormQueue([]);
                  addMessage({
                    id: crypto.randomUUID(), role: 'assistant',
                    content: remaining > 0
                      ? `Form dismissed — cancelled this and ${remaining} remaining form${remaining > 1 ? 's' : ''}.`
                      : 'No problem — form dismissed.',
                    timestamp: new Date(),
                  });
                }}
              />
            </>
          )}

          {/* Streaming indicator */}
          {streaming && (
            <div className="flex items-center gap-1.5 px-2 py-3">
              <span className="h-2 w-2 animate-pulse rounded-full bg-primary" />
              <span className="h-2 w-2 animate-pulse rounded-full bg-primary [animation-delay:150ms]" />
              <span className="h-2 w-2 animate-pulse rounded-full bg-primary [animation-delay:300ms]" />
            </div>
          )}

          <div ref={bottomAnchorRef} className="min-h-px" />
        </div>
      </div>

      {/* Input bar */}
      <div className="flex shrink-0 flex-col items-center border-t border-border bg-card p-4">
        <div className="flex w-full max-w-3xl items-end gap-2.5 rounded-2xl border border-input bg-background py-2.5 pl-4 pr-3 transition-colors focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20">
          <textarea
            ref={inputRef}
            value={input}
            onChange={e => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message AI Assistant…"
            rows={1}
            disabled={streaming}
            className="max-h-40 min-h-6 flex-1 resize-none bg-transparent text-[15px] leading-normal text-foreground outline-none placeholder:text-muted-foreground"
          />
          <Button
            size="icon"
            onClick={() => sendMessage()}
            disabled={!input.trim() || streaming}
            title="Send"
            aria-label="Send"
          >
            <Send aria-hidden />
          </Button>
        </div>
        <p className="mt-1.5 text-[11px] text-muted-foreground/70">AI can make mistakes. Verify important information.</p>
      </div>
    </div>
  );
}
