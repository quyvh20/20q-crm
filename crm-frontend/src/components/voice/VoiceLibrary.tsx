import { useEffect, useRef, useState } from 'react';
import {
  getVoiceNotes,
  getVoiceNote,
  applyVoiceNoteUpdates,
  analyzeVoiceNote,
  deleteVoiceNote,
  type VoiceNote,
  type VoiceNoteStatus,
  type ExtractedContactUpdates,
} from '../../lib/api';

interface VoiceLibraryProps {
  contactId?: string;
  dealId?: string;
}



const statusConfig: Record<string, { label: string; color: string; bg: string }> = {
  uploaded:   { label: 'Ready',      color: '#0ea5e9', bg: 'rgba(14,165,233,0.15)' },
  pending:    { label: 'Queued',     color: '#f59e0b', bg: 'rgba(245,158,11,0.15)' },
  processing: { label: 'Analyzing', color: '#6366f1', bg: 'rgba(99,102,241,0.15)' },
  done:       { label: 'Done',       color: '#22c55e', bg: 'rgba(34,197,94,0.15)' },
  error:      { label: 'Error',      color: '#ef4444', bg: 'rgba(239,68,68,0.15)' },
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

function sentimentEmoji(s?: string) {
  if (s === 'positive') return '😊';
  if (s === 'negative') return '😟';
  if (s === 'mixed') return '😐';
  return '🔵';
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

  // Real-time SSE Connection
  useEffect(() => {
    const hasPending = notes.some((n) => n.status === 'pending' || n.status === 'processing');
    if (!hasPending) return;

    const token = localStorage.getItem('access_token');
    if (!token) return;

    const API_BASE = (import.meta as any).env?.VITE_API_URL || 'http://localhost:8080';
    const abort = new AbortController();

    const pullEvents = async () => {
      try {
        const response = await fetch(`${API_BASE}/api/events`, {
          headers: { 'Authorization': `Bearer ${token}`, 'Accept': 'text/event-stream' },
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
                  // Fetch the mutated note from the DB explicitly to retrieve insights
                  getVoiceNote(data.voice_note_id).then((updatedNote) => {
                    setNotes((prev) =>
                      prev.map((n) => (n.id === updatedNote.id ? updatedNote : n))
                    );
                  }).catch(console.error);
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

    return () => {
      abort.abort();
    };
  }, [notes]);

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
      <div style={containerStyle}>
        {[1, 2, 3].map((i) => (
          <div key={i} style={{ ...cardStyle, opacity: 0.4, height: 80, animation: 'pulse 1.5s infinite' }} />
        ))}
        <style>{`@keyframes pulse { 0%,100%{opacity:.4} 50%{opacity:.7} }`}</style>
      </div>
    );
  }

  if (notes.length === 0) {
    return (
      <div style={{ ...containerStyle, textAlign: 'center', padding: 40, opacity: 0.6 }}>
        <div style={{ fontSize: 36, marginBottom: 12 }}>🎙</div>
        <p style={{ margin: 0, fontSize: 14 }}>No voice notes yet.<br />Record your first note above.</p>
      </div>
    );
  }

  return (
    <div style={containerStyle}>
      {notes.map((note) => {
        const sc = statusConfig[note.status];
        const isExpanded = expandedId === note.id;
        const updates = note.extracted_contact_updates;
        const showUpdates = note.status === 'done' && hasExtractedUpdates(updates) && note.contact_id;

        return (
          <div key={note.id} style={cardStyle}>
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 12 }}>
              <div style={{ width: 40, height: 40, borderRadius: 10, background: 'rgba(99,102,241,0.2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 18, flexShrink: 0 }}>
                🎙
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 13, fontWeight: 600, color: '#e2e8f0' }}>
                    {formatDate(note.created_at)}
                  </span>
                  <span style={{ padding: '2px 8px', borderRadius: 100, fontSize: 11, fontWeight: 600, color: sc.color, background: sc.bg }}>
                    {note.status === 'processing'
                      ? <span style={{ display: 'inline-flex', gap: 4, alignItems: 'center' }}>
                          <span style={{ animation: 'spin 1s linear infinite', display: 'inline-block' }}>⟳</span>
                          {sc.label}
                        </span>
                      : sc.label}
                  </span>
                  {note.duration_seconds > 0 && (
                    <span style={{ fontSize: 11, opacity: 0.5 }}>⏱ {formatDuration(note.duration_seconds)}</span>
                  )}
                  {note.sentiment && (
                    <span style={{ fontSize: 13 }}>{sentimentEmoji(note.sentiment)}</span>
                  )}
                </div>
                {(note.contact || note.deal) && (
                  <p style={{ margin: '4px 0 0', fontSize: 12, opacity: 0.6 }}>
                    {note.contact && `👤 ${note.contact.first_name} ${note.contact.last_name}`}
                    {note.contact && note.deal && ' · '}
                    {note.deal && `💼 ${note.deal.title}`}
                  </p>
                )}
              </div>
              {note.status === 'uploaded' && (
                <button
                  id={`voice-analyze-${note.id}`}
                  onClick={() => handleAnalyze(note.id)}
                  style={{
                    padding: '6px 12px', borderRadius: 8, border: 'none', cursor: 'pointer',
                    background: 'linear-gradient(135deg, #0ea5e9, #0284c7)', color: '#fff',
                    fontSize: 12, fontWeight: 600, marginRight: 8,
                    opacity: analyzingId === note.id ? 0.6 : 1,
                  }}
                  disabled={analyzingId === note.id}
                >
                  {analyzingId === note.id ? 'Starting...' : '✨ Analyze Audio'}
                </button>
              )}
              {note.status === 'done' && (
                <button
                  id={`voice-expand-${note.id}`}
                  onClick={() => setExpandedId(isExpanded ? null : note.id)}
                  style={iconBtnStyle}
                  title={isExpanded ? 'Collapse' : 'AI Insights'}
                >
                  {isExpanded ? '▲' : '▼'}
                </button>
              )}
              {/* Delete button */}
              {confirmDeleteId === note.id ? (
                <div style={{ display: 'flex', gap: 4, alignItems: 'center', flexShrink: 0 }}>
                  <span style={{ fontSize: 11, opacity: 0.7 }}>Delete?</span>
                  <button
                    id={`voice-delete-confirm-${note.id}`}
                    onClick={() => handleDelete(note.id)}
                    disabled={deletingId === note.id}
                    style={{ ...iconBtnStyle, background: 'rgba(239,68,68,0.3)', borderColor: 'rgba(239,68,68,0.5)', color: '#fca5a5', fontSize: 11 }}
                  >
                    {deletingId === note.id ? '⟳' : 'Yes'}
                  </button>
                  <button
                    id={`voice-delete-cancel-${note.id}`}
                    onClick={() => setConfirmDeleteId(null)}
                    style={{ ...iconBtnStyle, fontSize: 11 }}
                  >
                    No
                  </button>
                </div>
              ) : (
                <button
                  id={`voice-delete-${note.id}`}
                  onClick={() => setConfirmDeleteId(note.id)}
                  style={{ ...iconBtnStyle, color: '#f87171', flexShrink: 0 }}
                  title="Delete voice note"
                >
                  🗑
                </button>
              )}
            </div>

            {note.status === 'error' && note.error_message && (
              <p style={{ margin: '10px 0 0', fontSize: 12, color: '#f87171', background: 'rgba(239,68,68,0.1)', padding: '6px 10px', borderRadius: 6 }}>
                ✗ {note.error_message}
              </p>
            )}

            {isExpanded && note.status === 'done' && (
              <div style={{ marginTop: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
                {note.summary && (
                  <div style={insightSectionStyle}>
                    <p style={insightLabelStyle}>✦ Summary</p>
                    <p style={{ margin: 0, fontSize: 13, lineHeight: 1.6, opacity: 0.9 }}>{note.summary}</p>
                  </div>
                )}

                {(note.key_points?.length ?? 0) > 0 && (
                  <div style={insightSectionStyle}>
                    <p style={insightLabelStyle}>📌 Key Points</p>
                    <ul style={{ margin: 0, paddingLeft: 18 }}>
                      {note.key_points!.map((kp, i) => (
                        <li key={i} style={{ fontSize: 13, opacity: 0.85, marginBottom: 4, lineHeight: 1.5 }}>{kp}</li>
                      ))}
                    </ul>
                  </div>
                )}

                {(note.action_items?.length ?? 0) > 0 && (
                  <div style={insightSectionStyle}>
                    <p style={insightLabelStyle}>✅ Action Items</p>
                    {note.action_items!.map((ai, i) => (
                      <div key={i} style={{ display: 'flex', gap: 8, alignItems: 'flex-start', marginBottom: 6 }}>
                        <span style={{ padding: '1px 6px', borderRadius: 4, fontSize: 10, fontWeight: 700, background: priorityBg(ai.priority), color: '#fff', flexShrink: 0, marginTop: 1 }}>
                          {ai.priority.toUpperCase()}
                        </span>
                        <span style={{ fontSize: 13, opacity: 0.85 }}>
                          {ai.title}
                          {ai.due && <span style={{ opacity: 0.5, marginLeft: 6, fontSize: 11 }}>{new Date(ai.due).toLocaleDateString()}</span>}
                        </span>
                      </div>
                    ))}
                  </div>
                )}

                {note.transcript && (
                  <details style={{ marginTop: 4 }}>
                    <summary style={{ cursor: 'pointer', fontSize: 12, opacity: 0.5, userSelect: 'none' }}>📄 Show transcript</summary>
                    <p style={{ margin: '8px 0 0', fontSize: 12, lineHeight: 1.7, opacity: 0.7, fontFamily: 'monospace', background: 'rgba(0,0,0,0.2)', padding: 10, borderRadius: 6 }}>
                      {note.transcript}
                    </p>
                  </details>
                )}

                {showUpdates && (
                  <div style={{ ...insightSectionStyle, borderColor: 'rgba(245,158,11,0.5)', background: 'rgba(245,158,11,0.08)' }}>
                    <p style={{ ...insightLabelStyle, color: '#f59e0b' }}>🔔 AI Extracted Contact Data</p>
                    <div style={{ fontSize: 13, display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 12 }}>
                      {(updates!.phone_numbers?.length ?? 0) > 0 && (
                        <span>📞 <strong>Phone:</strong> {updates!.phone_numbers!.join(', ')}</span>
                      )}
                      {(updates!.emails?.length ?? 0) > 0 && (
                        <span>✉️ <strong>Email:</strong> {updates!.emails!.join(', ')}</span>
                      )}
                      {updates!.budget && (
                        <span>💰 <strong>Budget:</strong> {updates!.budget}</span>
                      )}
                      {updates!.next_meeting_date && (
                        <span>📅 <strong>Next Meeting:</strong> {updates!.next_meeting_date}</span>
                      )}
                      {updates!.company_name && (
                        <span>🏢 <strong>Company:</strong> {updates!.company_name}</span>
                      )}
                    </div>
                    {applySuccess === note.id ? (
                      <p style={{ margin: 0, fontSize: 12, color: '#22c55e', fontWeight: 600 }}>✓ Applied to contact profile</p>
                    ) : (
                      <button
                        id={`voice-apply-${note.id}`}
                        onClick={() => handleApply(note)}
                        disabled={applyingId === note.id}
                        style={{
                          padding: '7px 16px', borderRadius: 8, border: 'none', cursor: 'pointer',
                          background: 'linear-gradient(135deg, #f59e0b, #d97706)',
                          color: '#fff', fontSize: 12, fontWeight: 600,
                          opacity: applyingId === note.id ? 0.6 : 1,
                        }}
                      >
                        {applyingId === note.id ? '⟳ Applying…' : '⚡ Apply to Contact Profile'}
                      </button>
                    )}
                    {applyError && <p style={{ margin: '6px 0 0', fontSize: 11, color: '#f87171' }}>{applyError}</p>}
                  </div>
                )}
              </div>
            )}
          </div>
        );
      })}
      <style>{`@keyframes spin { to{transform:rotate(360deg)} }`}</style>
    </div>
  );
}

function priorityBg(p: string) {
  if (p === 'high') return '#ef4444';
  if (p === 'medium') return '#f59e0b';
  return '#6b7280';
}

const containerStyle: React.CSSProperties = {
  display: 'flex', flexDirection: 'column', gap: 12,
  fontFamily: "'Inter', sans-serif",
};

const cardStyle: React.CSSProperties = {
  background: 'rgba(255,255,255,0.03)',
  border: '1px solid rgba(255,255,255,0.08)',
  borderRadius: 14,
  padding: 16,
  transition: 'border-color 0.2s',
};

const insightSectionStyle: React.CSSProperties = {
  background: 'rgba(99,102,241,0.08)',
  border: '1px solid rgba(99,102,241,0.2)',
  borderRadius: 10,
  padding: 14,
};

const insightLabelStyle: React.CSSProperties = {
  margin: '0 0 8px', fontSize: 12, fontWeight: 700, color: '#a5b4fc', letterSpacing: '0.5px',
};

const iconBtnStyle: React.CSSProperties = {
  background: 'rgba(255,255,255,0.07)', border: '1px solid rgba(255,255,255,0.1)',
  borderRadius: 6, padding: '4px 8px', cursor: 'pointer', color: '#94a3b8', fontSize: 11,
  flexShrink: 0,
};
