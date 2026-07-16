import React, { useEffect, useState } from 'react';
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, MessagesSquare, Sparkles, Trash2, User } from 'lucide-react';
import { useAuth, usePermissions } from '../lib/auth';
import { prettyRole } from '../lib/roles';
import { useConfirm } from '../components/common/ConfirmDialog';
import AccessDeniedPanel from '../components/common/AccessDeniedPanel';
import {
  listChatSessions,
  getChatSessionMessages,
  deleteChatSession,
  type ChatSession,
  type ChatMessageItem,
} from '../lib/api';
import {
  Badge,
  Button,
  EmptyState,
  SpinnerBlock,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  TableShell,
} from '@/components/ui';

type RoleBadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive' | 'success' | 'warning';

const ROLE_BADGES: Record<string, RoleBadgeVariant> = {
  owner: 'warning',
  admin: 'default',
  manager: 'secondary',
  sales_rep: 'success',
  viewer: 'outline',
};

export default function ConversationLogPage() {
  const { hasCapability } = useAuth();
  const { loaded: permsLoaded } = usePermissions();
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [transcript, setTranscript] = useState<ChatMessageItem[]>([]);
  const [transcriptLoading, setTranscriptLoading] = useState(false);
  const [error, setError] = useState('');

  const limit = 20;
  const canAccess = hasCapability('members.manage');
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

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
    if (!(await confirmDialog({
      title: 'Delete conversation',
      body: 'Delete this conversation and its transcript? This cannot be undone.',
      confirmLabel: 'Delete',
    }))) return;
    try {
      await deleteChatSession(sessionId);
      setSessions(prev => prev.filter(s => s.id !== sessionId));
      setTotal(t => t - 1);
      if (expandedId === sessionId) {
        setExpandedId(null);
        setTranscript([]);
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Delete failed');
    }
  };

  // Wait for the capability fetch to settle before deciding, so a deep-linked
  // admin doesn't flash the denied panel (the SettingsLayout trap; matches
  // EmailTemplatesPage). hasCapability is false while perms are still loading.
  if (!permsLoaded) {
    return <SpinnerBlock />;
  }

  if (!canAccess) {
    return <AccessDeniedPanel capability="members.manage" what="AI conversation logs" />;
  }

  const totalPages = Math.ceil(total / limit);

  return (
    // Section-level chrome: this renders INSIDE the settings shell (U1), which
    // already owns the page header and padding.
    <div className="w-full">
      <div className="mb-4 flex items-start justify-between">
        <div>
          <h2 className="mb-1 text-lg font-semibold text-foreground">AI Conversation Logs</h2>
          <p className="text-sm text-muted-foreground">{total} total sessions · Page {page} of {Math.max(1, totalPages)}</p>
        </div>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-destructive/40 bg-destructive/10 px-3.5 py-2.5 text-sm text-destructive">{error}</div>
      )}

      {loading ? (
        <SpinnerBlock label="Loading sessions…" />
      ) : sessions.length === 0 ? (
        <EmptyState
          icon={MessagesSquare}
          title="No conversation sessions yet."
          description="Sessions appear here once users start chatting."
        />
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                {['User', 'Role', 'First Message', 'Started', 'Status', ''].map((h, i) => (
                  <TableHead key={h || `col-${i}`} className="whitespace-nowrap">{h}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {sessions.map(session => {
                const isExpanded = expandedId === session.id;
                return (
                  <React.Fragment key={session.id}>
                    <TableRow className={isExpanded ? 'bg-muted/40' : undefined}>
                      <TableCell>
                        <span className="font-semibold text-foreground">
                          {session.user ? `${session.user.first_name} ${session.user.last_name}` : 'Unknown'}
                        </span>
                        <br />
                        <span className="text-xs text-muted-foreground">{session.user?.email}</span>
                      </TableCell>
                      <TableCell>
                        <Badge variant={ROLE_BADGES[session.role] ?? 'outline'} className="capitalize">
                          {prettyRole(session.role)}
                        </Badge>
                      </TableCell>
                      <TableCell className="max-w-[220px]">
                        <span className="block truncate text-foreground">{session.title || '(empty)'}</span>
                      </TableCell>
                      <TableCell>
                        <span className="whitespace-nowrap text-xs text-muted-foreground">{new Date(session.created_at).toLocaleString()}</span>
                      </TableCell>
                      <TableCell>
                        <Badge variant={session.ended_at ? 'success' : 'warning'}>
                          {session.ended_at ? 'Ended' : 'Active'}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1.5">
                          <Button variant="outline" size="sm" onClick={() => expand(session.id)}>
                            {isExpanded ? <ChevronUp aria-hidden /> : <ChevronDown aria-hidden />}
                            {isExpanded ? 'Hide' : 'View'}
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => remove(session.id)}
                            aria-label="Delete conversation"
                            className="h-8 w-8 text-destructive hover:bg-destructive/10 hover:text-destructive"
                          >
                            <Trash2 aria-hidden />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                    {isExpanded && (
                      <TableRow className="hover:bg-transparent">
                        <TableCell colSpan={6} className="p-0">
                          {transcriptLoading ? (
                            <div className="p-4 text-sm text-muted-foreground">Loading transcript…</div>
                          ) : transcript.length === 0 ? (
                            <div className="p-4 text-sm text-muted-foreground">No messages in this session.</div>
                          ) : (
                            <div className="flex max-h-[360px] flex-col gap-2 overflow-y-auto bg-muted/50 px-4 py-3">
                              {transcript.map(m => (
                                <div
                                  key={m.id}
                                  className={`max-w-[80%] rounded-lg border px-3 py-2 ${
                                    m.role === 'user'
                                      ? 'self-end border-primary/20 bg-primary/10'
                                      : 'self-start border-border bg-background'
                                  }`}
                                >
                                  <span className="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wide text-muted-foreground">
                                    {m.role === 'user'
                                      ? <User aria-hidden className="h-3 w-3" />
                                      : <Sparkles aria-hidden className="h-3 w-3" />}
                                    {m.role === 'user' ? 'User' : 'AI'}
                                  </span>
                                  <p className="mb-0.5 mt-1 whitespace-pre-wrap break-words text-sm leading-relaxed text-foreground">{m.content}</p>
                                  <span className="text-[10px] text-muted-foreground">{new Date(m.created_at).toLocaleTimeString()}</span>
                                </div>
                              ))}
                            </div>
                          )}
                        </TableCell>
                      </TableRow>
                    )}
                  </React.Fragment>
                );
              })}
            </TableBody>
          </Table>
        </TableShell>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="mt-5 flex items-center justify-center gap-3">
          <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => setPage(p => p - 1)}>
            <ChevronLeft aria-hidden /> Prev
          </Button>
          <span className="text-sm text-muted-foreground">Page {page} of {totalPages}</span>
          <Button variant="outline" size="sm" disabled={page >= totalPages} onClick={() => setPage(p => p + 1)}>
            Next <ChevronRight aria-hidden />
          </Button>
        </div>
      )}
      {confirmDialogEl}
    </div>
  );
}
