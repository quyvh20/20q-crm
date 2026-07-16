import { useEffect, useState } from 'react';
import {
  Bell, Briefcase, Building2, Calendar, Check, CheckSquare, ChevronDown, ChevronUp, Circle,
  Clock, DollarSign, FileText, Frown, Loader2, Mail, Meh, Mic, Phone, Pin, RotateCw,
  Smile, Sparkles, Trash2, TriangleAlert, UserRound, Zap, type LucideIcon,
} from 'lucide-react';
import {
  getVoiceNotes,
  getVoiceNote,
  applyVoiceNoteUpdates,
  analyzeVoiceNote,
  deleteVoiceNote,
  getAccessToken,
  type VoiceNote,
  type ExtractedContactUpdates,
} from '../../lib/api';
import { Badge, type BadgeProps } from '../ui/badge';
import { Button } from '../ui/button';
import { EmptyState } from '../ui/empty-state';
import { Skeleton } from '../ui/skeleton';

interface VoiceLibraryProps {
  contactId?: string;
  dealId?: string;
}



const statusConfig: Record<string, { label: string; variant: BadgeProps['variant'] }> = {
  uploaded:   { label: 'Ready',     variant: 'default' },
  pending:    { label: 'Queued',    variant: 'warning' },
  processing: { label: 'Analyzing', variant: 'default' },
  done:       { label: 'Done',      variant: 'success' },
  error:      { label: 'Error',     variant: 'destructive' },
};

function hasExtractedUpdates(u?: ExtractedContactUpdates): boolean {
  if (!u) return false;
  return (
    (u.phone_numbers?.length ?? 0) > 0 ||
    (u.emails?.length ?? 0) > 0 ||
    !!u.budget ||
    !!u.next_meeting_date ||
    !!u.company_name
  );
}

function SentimentIcon({ sentiment }: { sentiment?: string }) {
  const cls = 'h-4 w-4 shrink-0';
  if (sentiment === 'positive') return <Smile aria-hidden className={`${cls} text-emerald-600 dark:text-emerald-400`} />;
  if (sentiment === 'negative') return <Frown aria-hidden className={`${cls} text-destructive`} />;
  if (sentiment === 'mixed') return <Meh aria-hidden className={`${cls} text-amber-600 dark:text-amber-400`} />;
  return <Circle aria-hidden className={`${cls} text-primary`} />;
}

function formatDuration(s: number) {
  const m = Math.floor(s / 60);
  const sec = s % 60;
  return `${m}:${String(sec).padStart(2, '0')}`;
}

function formatDate(iso: string) {
  return new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

export default function VoiceLibrary({ contactId, dealId }: VoiceLibraryProps) {
  const [notes, setNotes] = useState<VoiceNote[]>([]);
  const [loading, setLoading] = useState(true);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [applyingId, setApplyingId] = useState<string | null>(null);
  const [applySuccess, setApplySuccess] = useState<string | null>(null);
  const [applyError, setApplyError] = useState<string | null>(null);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const [analyzingId, setAnalyzingId] = useState<string | null>(null);

  const fetchNotes = async () => {
    try {
      const data = await getVoiceNotes({ contact_id: contactId, deal_id: dealId });
      setNotes(data);
    } catch {
      // silent
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchNotes();
  }, [contactId, dealId]);

  // Derived boolean — only changes when pending state transitions, not on every notes update
  const hasPending = notes.some((n) => n.status === 'pending' || n.status === 'processing');

  // Real-time SSE Connection + 10s fallback poll for stuck pending notes.
  // Depends on hasPending (boolean) so the connection only restarts when
  // pending state transitions, not on every poll-triggered notes refresh.
  useEffect(() => {
    if (!hasPending) return;

    const token = getAccessToken();
    if (!token) return;

    const API_BASE = (import.meta as any).env?.VITE_API_URL ?? ((import.meta as any).env?.DEV ? 'http://localhost:8080' : '');
    const abort = new AbortController();

    const pullEvents = async () => {
      try {
        const response = await fetch(`${API_BASE}/api/events`, {
          headers: { 'Authorization': `Bearer ${token}`, 'Accept': 'text/event-stream' },
          credentials: 'include',
          signal: abort.signal
        });

        if (!response.ok) throw new Error('SSE failed to connect');
        if (!response.body) return;

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() ?? '';
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const str = line.slice(6);
              if (str === '') continue;
              try {
                const data = JSON.parse(str);
                if (data.type === 'voice_note_ready' && data.voice_note_id) {
                  getVoiceNote(data.voice_note_id).then((updatedNote) => {
                    setNotes((prev) =>
                      prev.map((n) => (n.id === updatedNote.id ? updatedNote : n))
                    );
                  }).catch(console.error);
                } else if (data.type === 'voice_note_error' && data.voice_note_id) {
                  setNotes((prev) =>
                    prev.map((n) =>
                      n.id === data.voice_note_id
                        ? { ...n, status: 'error', error_message: data.error }
                        : n
                    )
                  );
                }
              } catch (e) {}
            }
          }
        }
      } catch (e: any) {
        if (e.name !== 'AbortError') console.error('SSE Error:', e.message);
      }
    };

    pullEvents();

    // Fallback: re-fetch all notes every 10s in case SSE misses an event
    const fallbackPoll = setInterval(fetchNotes, 10000);

    return () => {
      abort.abort();
      clearInterval(fallbackPoll);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasPending]);

  const handleApply = async (note: VoiceNote) => {
    setApplyingId(note.id);
    setApplySuccess(null);
    setApplyError(null);
    try {
      await applyVoiceNoteUpdates(note.id);
      setApplySuccess(note.id);
      await fetchNotes();
    } catch (err) {
      setApplyError(err instanceof Error ? err.message : 'Failed');
    } finally {
      setApplyingId(null);
    }
  };

  const handleDelete = async (id: string) => {
    setDeletingId(id);
    try {
      await deleteVoiceNote(id);
      // Optimistic remove — no re-fetch needed
      setNotes((prev) => prev.filter((n) => n.id !== id));
      setConfirmDeleteId(null);
      if (expandedId === id) setExpandedId(null);
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Delete failed');
    } finally {
      setDeletingId(null);
    }
  };

  const handleAnalyze = async (id: string) => {
    setAnalyzingId(id);
    try {
      await analyzeVoiceNote(id);
      setNotes((prev) => prev.map((n) => (n.id === id ? { ...n, status: 'pending' } : n)));
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Analysis failed to start');
    } finally {
      setAnalyzingId(null);
    }
  };

  if (loading) {
    return (
      <div className="flex flex-col gap-3">
        {[1, 2, 3].map((i) => (
          <Skeleton key={i} className="h-20 rounded-xl" />
        ))}
      </div>
    );
  }

  if (notes.length === 0) {
    return (
      <EmptyState
        icon={Mic}
        title="No voice notes yet."
        description="Upload your first audio file to get started."
      />
    );
  }

  return (
    <div className="flex flex-col gap-3">
      {notes.map((note) => {
        const sc = statusConfig[note.status];
        const isExpanded = expandedId === note.id;
        const updates = note.extracted_contact_updates;
        const showUpdates = note.status === 'done' && hasExtractedUpdates(updates) && note.contact_id;

        return (
          <div key={note.id} className="rounded-xl border border-border bg-card p-4">
            <div className="flex items-start gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/10">
                <Mic aria-hidden className="h-5 w-5 text-primary" />
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-[13px] font-semibold text-foreground">
                    {formatDate(note.created_at)}
                  </span>
                  <Badge variant={sc.variant}>
                    {note.status === 'processing' && <Loader2 aria-hidden className="h-3 w-3 animate-spin" />}
                    {sc.label}
                  </Badge>
                  {note.duration_seconds > 0 && (
                    <span className="inline-flex items-center gap-1 text-[11px] text-muted-foreground">
                      <Clock aria-hidden className="h-3 w-3" />
                      {formatDuration(note.duration_seconds)}
                    </span>
                  )}
                  {note.sentiment && <SentimentIcon sentiment={note.sentiment} />}
                </div>
                {(note.contact || note.deal) && (
                  <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
                    {note.contact && (
                      <span className="inline-flex items-center gap-1">
                        <UserRound aria-hidden className="h-3 w-3" />
                        {note.contact.first_name} {note.contact.last_name}
                      </span>
                    )}
                    {note.contact && note.deal && ' · '}
                    {note.deal && (
                      <span className="inline-flex items-center gap-1">
                        <Briefcase aria-hidden className="h-3 w-3" />
                        {note.deal.title}
                      </span>
                    )}
                  </p>
                )}
              </div>
              {(note.status === 'uploaded' || note.status === 'error') && (
                <Button
                  id={`voice-analyze-${note.id}`}
                  size="sm"
                  className="mr-2 shrink-0"
                  onClick={() => handleAnalyze(note.id)}
                  disabled={analyzingId === note.id}
                >
                  {analyzingId === note.id
                    ? 'Starting...'
                    : note.status === 'error'
                      ? <><RotateCw aria-hidden />Retry Analysis</>
                      : <><Sparkles aria-hidden />Analyze Audio</>}
                </Button>
              )}
              {(note.status === 'pending' || note.status === 'processing') && (
                <span className="mr-2 inline-flex shrink-0 items-center gap-1.5">
                  <span className="inline-flex items-center gap-1.5 rounded-lg bg-primary/10 px-2.5 py-1.5 text-xs font-semibold text-primary">
                    <Loader2 aria-hidden className="h-3.5 w-3.5 animate-spin" />
                    {note.status === 'pending' ? 'Queued…' : 'Analyzing…'}
                  </span>
                  {note.status === 'pending' && (
                    <Button
                      id={`voice-retry-${note.id}`}
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-amber-600 dark:text-amber-400"
                      onClick={() => handleAnalyze(note.id)}
                      disabled={analyzingId === note.id}
                      title="Job may be stuck — click to re-queue"
                    >
                      {analyzingId === note.id ? <Loader2 aria-hidden className="animate-spin" /> : <RotateCw aria-hidden />}
                    </Button>
                  )}
                </span>
              )}
              {note.status === 'done' && (
                <Button
                  id={`voice-expand-${note.id}`}
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7 shrink-0 text-muted-foreground hover:text-foreground"
                  onClick={() => setExpandedId(isExpanded ? null : note.id)}
                  title={isExpanded ? 'Collapse' : 'AI Insights'}
                >
                  {isExpanded ? <ChevronUp aria-hidden /> : <ChevronDown aria-hidden />}
                </Button>
              )}
              {/* Delete button */}
              {confirmDeleteId === note.id ? (
                <div className="flex shrink-0 items-center gap-1">
                  <span className="text-[11px] text-muted-foreground">Delete?</span>
                  <Button
                    id={`voice-delete-confirm-${note.id}`}
                    variant="destructive"
                    size="sm"
                    className="h-7 px-2 text-[11px]"
                    onClick={() => handleDelete(note.id)}
                    disabled={deletingId === note.id}
                  >
                    {deletingId === note.id ? <Loader2 aria-hidden className="animate-spin" /> : 'Yes'}
                  </Button>
                  <Button
                    id={`voice-delete-cancel-${note.id}`}
                    variant="outline"
                    size="sm"
                    className="h-7 px-2 text-[11px]"
                    onClick={() => setConfirmDeleteId(null)}
                  >
                    No
                  </Button>
                </div>
              ) : (
                <Button
                  id={`voice-delete-${note.id}`}
                  variant="ghost"
                  size="icon"
                  className="h-7 w-7 shrink-0 text-destructive hover:text-destructive"
                  onClick={() => setConfirmDeleteId(note.id)}
                  title="Delete voice note"
                >
                  <Trash2 aria-hidden />
                </Button>
              )}
            </div>

            {note.status === 'error' && note.error_message && (
              <p className="mt-2.5 flex items-start gap-1.5 rounded-md bg-destructive/10 px-2.5 py-1.5 text-xs text-destructive">
                <TriangleAlert aria-hidden className="mt-0.5 h-3 w-3 shrink-0" />
                {note.error_message}
              </p>
            )}

            {isExpanded && note.status === 'done' && (
              <div className="mt-4 flex flex-col gap-3.5">
                {note.summary && (
                  <InsightSection icon={Sparkles} label="Summary">
                    <p className="m-0 text-[13px] leading-relaxed text-foreground/90">{note.summary}</p>
                  </InsightSection>
                )}

                {(note.key_points?.length ?? 0) > 0 && (
                  <InsightSection icon={Pin} label="Key Points">
                    <ul className="m-0 list-disc pl-4">
                      {note.key_points!.map((kp, i) => (
                        <li key={i} className="mb-1 text-[13px] leading-normal text-foreground/85">{kp}</li>
                      ))}
                    </ul>
                  </InsightSection>
                )}

                {(note.action_items?.length ?? 0) > 0 && (
                  <InsightSection icon={CheckSquare} label="Action Items">
                    {note.action_items!.map((ai, i) => (
                      <div key={i} className="mb-1.5 flex items-start gap-2">
                        <span className={`mt-px shrink-0 rounded px-1.5 py-px text-[10px] font-bold ${priorityClass(ai.priority)}`}>
                          {ai.priority.toUpperCase()}
                        </span>
                        <span className="text-[13px] text-foreground/85">
                          {ai.title}
                          {ai.due && <span className="ml-1.5 text-[11px] text-muted-foreground">{new Date(ai.due).toLocaleDateString()}</span>}
                        </span>
                      </div>
                    ))}
                  </InsightSection>
                )}

                {note.transcript && (
                  <details className="mt-1">
                    <summary className="cursor-pointer select-none text-xs text-muted-foreground">
                      <FileText aria-hidden className="mr-1 inline h-3 w-3 align-[-1px]" />
                      Show transcript
                    </summary>
                    <p className="mt-2 rounded-md bg-muted/60 p-2.5 font-mono text-xs leading-relaxed text-muted-foreground">
                      {note.transcript}
                    </p>
                  </details>
                )}

                {showUpdates && (
                  <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3.5">
                    <p className="mb-2 flex items-center gap-1.5 text-xs font-bold text-amber-600 dark:text-amber-400">
                      <Bell aria-hidden className="h-3.5 w-3.5" />
                      AI Extracted Contact Data
                    </p>
                    <div className="mb-3 flex flex-col gap-1.5 text-[13px]">
                      {(updates!.phone_numbers?.length ?? 0) > 0 && (
                        <span className="inline-flex items-center gap-1.5">
                          <Phone aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                          <strong>Phone:</strong> {updates!.phone_numbers!.join(', ')}
                        </span>
                      )}
                      {(updates!.emails?.length ?? 0) > 0 && (
                        <span className="inline-flex items-center gap-1.5">
                          <Mail aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                          <strong>Email:</strong> {updates!.emails!.join(', ')}
                        </span>
                      )}
                      {updates!.budget && (
                        <span className="inline-flex items-center gap-1.5">
                          <DollarSign aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                          <strong>Budget:</strong> {updates!.budget}
                        </span>
                      )}
                      {updates!.next_meeting_date && (
                        <span className="inline-flex items-center gap-1.5">
                          <Calendar aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                          <strong>Next Meeting:</strong> {updates!.next_meeting_date}
                        </span>
                      )}
                      {updates!.company_name && (
                        <span className="inline-flex items-center gap-1.5">
                          <Building2 aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                          <strong>Company:</strong> {updates!.company_name}
                        </span>
                      )}
                    </div>
                    {applySuccess === note.id ? (
                      <p className="m-0 flex items-center gap-1 text-xs font-semibold text-emerald-600 dark:text-emerald-400">
                        <Check aria-hidden className="h-3.5 w-3.5" />
                        Applied to contact profile
                      </p>
                    ) : (
                      <Button
                        id={`voice-apply-${note.id}`}
                        size="sm"
                        onClick={() => handleApply(note)}
                        disabled={applyingId === note.id}
                      >
                        {applyingId === note.id
                          ? <><Loader2 aria-hidden className="animate-spin" />Applying…</>
                          : <><Zap aria-hidden />Apply to Contact Profile</>}
                      </Button>
                    )}
                    {applyError && <p className="mt-1.5 text-[11px] text-destructive">{applyError}</p>}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function InsightSection({ icon: Icon, label, children }: {
  icon: LucideIcon; label: string; children: React.ReactNode;
}) {
  return (
    <div className="rounded-lg border border-border bg-muted/40 p-3.5">
      <p className="mb-2 flex items-center gap-1.5 text-xs font-bold text-primary">
        <Icon aria-hidden className="h-3.5 w-3.5" />
        {label}
      </p>
      {children}
    </div>
  );
}

function priorityClass(p: string) {
  if (p === 'high') return 'bg-destructive/10 text-destructive';
  if (p === 'medium') return 'bg-amber-500/10 text-amber-600 dark:text-amber-400';
  return 'bg-secondary text-secondary-foreground';
}
