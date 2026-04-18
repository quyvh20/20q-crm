import React, { useEffect, useState } from 'react';
import { useAuth } from '../lib/auth';
import {
  listChatSessions,
  getChatSessionMessages,
  deleteChatSession,
  type ChatSession,
  type ChatMessageItem,
} from '../lib/api';

const ROLE_COLORS: Record<string, string> = {
  owner: '#7c3aed',
  admin: '#1d4ed8',
  manager: '#0891b2',
  sales_rep: '#059669',
  viewer: '#6b7280',
};

export default function ConversationLogPage() {
  const { currentRole } = useAuth();
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [transcript, setTranscript] = useState<ChatMessageItem[]>([]);
  const [transcriptLoading, setTranscriptLoading] = useState(false);
  const [error, setError] = useState('');

  const limit = 20;
  const canAccess = currentRole === 'owner' || currentRole === 'admin';

  useEffect(() => {
    if (!canAccess) return;
    setLoading(true);
    listChatSessions(page, limit)
      .then(res => {
        setSessions(res.data);
        setTotal(res.total);
      })
      .catch(e => setError(e.message))
      .finally(() => setLoading(false));
  }, [page, canAccess]);

  const expand = async (sessionId: string) => {
    if (expandedId === sessionId) {
      setExpandedId(null);
      setTranscript([]);
      return;
    }
    setExpandedId(sessionId);
    setTranscriptLoading(true);
    try {
      const msgs = await getChatSessionMessages(sessionId);
      setTranscript(msgs);
    } catch (e: unknown) {
      console.error(e);
    } finally {
      setTranscriptLoading(false);
    }
  };

  const remove = async (sessionId: string) => {
    if (!confirm('Delete this conversation? This cannot be undone.')) return;
    try {
      await deleteChatSession(sessionId);
      setSessions(prev => prev.filter(s => s.id !== sessionId));
      setTotal(t => t - 1);
      if (expandedId === sessionId) {
        setExpandedId(null);
        setTranscript([]);
      }
    } catch (e: unknown) {
      const err = e instanceof Error ? e.message : 'Delete failed';
      alert(err);
    }
  };

  if (!canAccess) {
    return (
      <div style={styles.forbidden}>
        <div style={styles.forbiddenIcon}>🔒</div>
        <h2 style={styles.forbiddenTitle}>Access Restricted</h2>
        <p style={styles.forbiddenText}>Only admins and owners can view conversation logs.</p>
      </div>
    );
  }

  const totalPages = Math.ceil(total / limit);

  return (
    <div style={styles.page}>
      <div style={styles.header}>
        <div>
          <h1 style={styles.title}>Conversation Logs</h1>
          <p style={styles.subtitle}>{total} total sessions · Page {page} of {Math.max(1, totalPages)}</p>
        </div>
      </div>

      {error && <div style={styles.errorBanner}>{error}</div>}

      {loading ? (
        <div style={styles.loading}>Loading sessions…</div>
      ) : sessions.length === 0 ? (
        <div style={styles.empty}>
          <p>No conversation sessions yet.</p>
          <p style={{ color: 'var(--muted-foreground)', fontSize: 13 }}>Sessions appear here once users start chatting.</p>
        </div>
      ) : (
        <div style={styles.tableWrapper}>
          <table style={styles.table}>
            <thead>
              <tr>
                {['User', 'Role', 'First Message', 'Started', 'Status', ''].map(h => (
                  <th key={h} style={styles.th}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {sessions.map(session => {
                const isExpanded = expandedId === session.id;
                return (
                  <React.Fragment key={session.id}>
                    <tr style={{ ...styles.tr, background: isExpanded ? 'rgba(245,158,11,0.04)' : undefined }}>
                      <td style={styles.td}>
                        <span style={styles.userName}>
                          {session.user ? `${session.user.first_name} ${session.user.last_name}` : 'Unknown'}
                        </span>
                        <br />
                        <span style={styles.userEmail}>{session.user?.email}</span>
                      </td>
                      <td style={styles.td}>
                        <span style={{
                          ...styles.roleBadge,
                          background: (ROLE_COLORS[session.role] || '#6b7280') + '22',
                          color: ROLE_COLORS[session.role] || '#6b7280',
                          border: `1px solid ${(ROLE_COLORS[session.role] || '#6b7280')}44`,
                        }}>
                          {session.role}
                        </span>
                      </td>
                      <td style={{ ...styles.td, maxWidth: 220 }}>
                        <span style={styles.titleCell}>{session.title || '(empty)'}</span>
                      </td>
                      <td style={styles.td}>
                        <span style={styles.date}>{new Date(session.created_at).toLocaleString()}</span>
                      </td>
                      <td style={styles.td}>
                        <span style={{
                          ...styles.statusDot,
                          background: session.ended_at ? '#10b981' : '#f59e0b',
                        }} />
                        {session.ended_at ? 'Ended' : 'Active'}
                      </td>
                      <td style={styles.td}>
                        <div style={styles.actions}>
                          <button style={styles.expandBtn} onClick={() => expand(session.id)}>
                            {isExpanded ? '▲ Hide' : '▼ View'}
                          </button>
                          <button style={styles.deleteBtn} onClick={() => remove(session.id)}>🗑</button>
                        </div>
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr>
                        <td colSpan={6} style={styles.transcriptCell}>
                          {transcriptLoading ? (
                            <div style={{ padding: '16px', color: 'var(--muted-foreground)' }}>Loading transcript…</div>
                          ) : transcript.length === 0 ? (
                            <div style={{ padding: '16px', color: 'var(--muted-foreground)' }}>No messages in this session.</div>
                          ) : (
                            <div style={styles.transcript}>
                              {transcript.map(m => (
                                <div key={m.id} style={{
                                  ...styles.transcriptMsg,
                                  ...(m.role === 'user' ? styles.transcriptUser : styles.transcriptAssistant),
                                }}>
                                  <span style={styles.transcriptRole}>{m.role === 'user' ? '👤 User' : '✦ AI'}</span>
                                  <p style={styles.transcriptContent}>{m.content}</p>
                                  <span style={styles.transcriptTime}>{new Date(m.created_at).toLocaleTimeString()}</span>
                                </div>
                              ))}
                            </div>
                          )}
                        </td>
                      </tr>
                    )}
                  </React.Fragment>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div style={styles.pagination}>
          <button style={styles.pageBtn} disabled={page <= 1} onClick={() => setPage(p => p - 1)}>← Prev</button>
          <span style={styles.pageInfo}>Page {page} of {totalPages}</span>
          <button style={styles.pageBtn} disabled={page >= totalPages} onClick={() => setPage(p => p + 1)}>Next →</button>
        </div>
      )}
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  page: { padding: '24px', maxWidth: '100%' },
  header: { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', marginBottom: 24 },
  title: { fontSize: 24, fontWeight: 700, margin: '0 0 4px' },
  subtitle: { fontSize: 13, color: 'var(--muted-foreground)', margin: 0 },
  errorBanner: { background: '#fef2f2', border: '1px solid #fecaca', color: '#dc2626', borderRadius: 8, padding: '10px 14px', marginBottom: 16, fontSize: 13 },
  loading: { textAlign: 'center', padding: 40, color: 'var(--muted-foreground)' },
  empty: { textAlign: 'center', padding: 60 },
  tableWrapper: { overflowX: 'auto', borderRadius: 12, border: '1px solid var(--border)' },
  table: { width: '100%', borderCollapse: 'collapse' },
  th: { textAlign: 'left', padding: '10px 14px', fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.05em', color: 'var(--muted-foreground)', borderBottom: '1px solid var(--border)', background: 'var(--accent)', whiteSpace: 'nowrap' },
  tr: { borderBottom: '1px solid var(--border)', transition: 'background 0.1s' },
  td: { padding: '12px 14px', fontSize: 13, verticalAlign: 'middle' },
  userName: { fontWeight: 600 },
  userEmail: { fontSize: 11, color: 'var(--muted-foreground)' },
  roleBadge: { fontSize: 11, fontWeight: 700, padding: '2px 8px', borderRadius: 10, textTransform: 'capitalize' },
  titleCell: { display: 'block', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', maxWidth: 220, color: 'var(--foreground)' },
  date: { fontSize: 12, color: 'var(--muted-foreground)', whiteSpace: 'nowrap' },
  statusDot: { width: 7, height: 7, borderRadius: '50%', display: 'inline-block', marginRight: 5 },
  actions: { display: 'flex', gap: 6, alignItems: 'center' },
  expandBtn: { padding: '4px 10px', borderRadius: 7, border: '1px solid var(--border)', background: 'transparent', cursor: 'pointer', fontSize: 11, fontWeight: 600, color: 'var(--foreground)', whiteSpace: 'nowrap' },
  deleteBtn: { padding: '4px 8px', borderRadius: 7, border: '1px solid #fecaca', background: '#fff1f2', cursor: 'pointer', fontSize: 13 },
  transcriptCell: { padding: 0 },
  transcript: { display: 'flex', flexDirection: 'column', gap: 8, padding: '12px 16px', background: 'var(--accent)', maxHeight: 360, overflowY: 'auto' },
  transcriptMsg: { borderRadius: 10, padding: '8px 12px', maxWidth: '80%' },
  transcriptUser: { background: 'linear-gradient(135deg, rgba(245,158,11,0.15), rgba(239,68,68,0.1))', alignSelf: 'flex-end', border: '1px solid rgba(245,158,11,0.2)' },
  transcriptAssistant: { background: 'var(--background)', alignSelf: 'flex-start', border: '1px solid var(--border)' },
  transcriptRole: { fontSize: 10, fontWeight: 700, color: 'var(--muted-foreground)', textTransform: 'uppercase', letterSpacing: '0.05em' },
  transcriptContent: { fontSize: 13, margin: '4px 0 2px', lineHeight: 1.55, whiteSpace: 'pre-wrap', wordBreak: 'break-word' },
  transcriptTime: { fontSize: 10, color: 'var(--muted-foreground)' },
  pagination: { display: 'flex', alignItems: 'center', gap: 12, justifyContent: 'center', marginTop: 20 },
  pageBtn: { padding: '6px 16px', borderRadius: 8, border: '1px solid var(--border)', background: 'transparent', cursor: 'pointer', fontSize: 13, fontWeight: 600 },
  pageInfo: { fontSize: 13, color: 'var(--muted-foreground)' },
  forbidden: { display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', height: '100%', gap: 12, padding: 40 },
  forbiddenIcon: { fontSize: 48 },
  forbiddenTitle: { fontSize: 22, fontWeight: 700, margin: 0 },
  forbiddenText: { fontSize: 14, color: 'var(--muted-foreground)', margin: 0 },
};
