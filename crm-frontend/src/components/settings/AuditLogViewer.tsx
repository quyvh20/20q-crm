import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getAuditEvents, exportAuditCsv, type AuditEventFilters } from '../../lib/api';

const PAGE_SIZE = 50;

const CATEGORIES = [
  { value: '', label: 'All activity' },
  { value: 'admin', label: 'Admin changes' },
  { value: 'auth', label: 'Sign-in activity' },
  { value: 'security', label: 'Security events' },
] as const;

// Friendly labels for the event_type vocabulary emitted by the backend.
const EVENT_LABELS: Record<string, string> = {
  'login.success': 'Signed in',
  'login.failed': 'Failed sign-in',
  'login.throttled': 'Sign-in throttled',
  'login.new_device': 'New-device sign-in',
  'token.reuse': 'Token reuse detected',
  'password.reset_requested': 'Password reset requested',
  'password.reset': 'Password reset',
  'email.verified': 'Email verified',
  'email.verification_sent': 'Verification email sent',
  'member.invited': 'Member invited',
  'member.role_changed': 'Member role changed',
  'member.suspended': 'Member suspended',
  'member.reinstated': 'Member reinstated',
  'member.ownership_transferred': 'Ownership transferred',
  'member.removed': 'Member removed',
  'role.created': 'Role created',
  'role.updated': 'Role updated',
  'role.deleted': 'Role deleted',
  'role.capabilities_changed': 'Role capabilities changed',
  'permission.ols_changed': 'Object permission changed',
  'permission.fls_changed': 'Field permission changed',
  'session.revoked': 'Session revoked',
  'session.signed_out_others': 'Signed out other devices',
};

const CATEGORY_BADGE: Record<string, string> = {
  admin: 'bg-blue-500/15 text-blue-400',
  auth: 'bg-emerald-500/15 text-emerald-400',
  security: 'bg-amber-500/15 text-amber-400',
};

function eventLabel(t: string): string {
  return EVENT_LABELS[t] || t;
}

function summarizeMeta(meta: Record<string, unknown>): string {
  const entries = Object.entries(meta || {}).filter(([, v]) => v !== '' && v != null);
  if (entries.length === 0) return '';
  return entries
    .map(([k, v]) => `${k}: ${Array.isArray(v) ? v.join(', ') || '—' : String(v)}`)
    .join(' · ');
}

export default function AuditLogViewer() {
  const [category, setCategory] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [page, setPage] = useState(0);
  const [exporting, setExporting] = useState(false);

  // Convert the date inputs (yyyy-mm-dd) to RFC3339 bounds.
  const filters: AuditEventFilters = {
    category: category || undefined,
    from: from ? new Date(from + 'T00:00:00').toISOString() : undefined,
    to: to ? new Date(to + 'T23:59:59').toISOString() : undefined,
    limit: PAGE_SIZE,
    offset: page * PAGE_SIZE,
  };

  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['audit-events', category, from, to, page],
    queryFn: () => getAuditEvents(filters),
    placeholderData: (prev) => prev,
  });

  const events = data?.events ?? [];
  const total = data?.total ?? 0;
  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE));

  const resetPageAnd = (fn: () => void) => {
    setPage(0);
    fn();
  };

  const handleExport = async () => {
    setExporting(true);
    try {
      const blob = await exportAuditCsv({ category: filters.category, from: filters.from, to: filters.to });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `audit-log-${new Date().toISOString().slice(0, 10)}.csv`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch (e) {
      alert(e instanceof Error ? e.message : 'Export failed');
    } finally {
      setExporting(false);
    }
  };

  return (
    <div className="space-y-4">
      <div>
        <h2 className="text-lg font-semibold">Audit Log</h2>
        <p className="text-sm text-muted-foreground mt-0.5">
          Every sign-in, member change, role edit, and permission change — who did it and when.
        </p>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Activity
          <select
            value={category}
            onChange={(e) => resetPageAnd(() => setCategory(e.target.value))}
            className="h-9 rounded-md border border-border bg-background px-2 text-sm text-foreground"
          >
            {CATEGORIES.map((c) => (
              <option key={c.value} value={c.value}>{c.label}</option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          From
          <input
            type="date"
            value={from}
            onChange={(e) => resetPageAnd(() => setFrom(e.target.value))}
            className="h-9 rounded-md border border-border bg-background px-2 text-sm text-foreground"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          To
          <input
            type="date"
            value={to}
            onChange={(e) => resetPageAnd(() => setTo(e.target.value))}
            className="h-9 rounded-md border border-border bg-background px-2 text-sm text-foreground"
          />
        </label>
        <div className="ml-auto">
          <button
            onClick={handleExport}
            disabled={exporting}
            className="h-9 rounded-md border border-border px-3 text-sm font-medium hover:bg-accent/40 disabled:opacity-50"
          >
            {exporting ? 'Exporting…' : 'Export CSV'}
          </button>
        </div>
      </div>

      {/* Table */}
      {isError ? (
        <div className="rounded-md border border-red-500/40 bg-red-500/10 p-4 text-sm text-red-400">
          {error instanceof Error ? error.message : 'Failed to load audit log'}
        </div>
      ) : isLoading ? (
        <div className="flex items-center justify-center py-12">
          <div className="h-6 w-6 animate-spin rounded-full border-2 border-primary border-t-transparent" />
        </div>
      ) : events.length === 0 ? (
        <div className="rounded-md border border-border py-12 text-center text-sm text-muted-foreground">
          No activity recorded for this filter yet.
        </div>
      ) : (
        <div className="overflow-x-auto rounded-md border border-border">
          <table className="w-full text-left text-sm">
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wider text-muted-foreground">
                <th className="px-3 py-2 font-medium">When</th>
                <th className="px-3 py-2 font-medium">Actor</th>
                <th className="px-3 py-2 font-medium">Event</th>
                <th className="px-3 py-2 font-medium">Details</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id} className="border-b border-border/50 last:border-0 hover:bg-accent/20">
                  <td className="whitespace-nowrap px-3 py-2 text-muted-foreground">
                    {new Date(e.created_at).toLocaleString()}
                  </td>
                  <td className="px-3 py-2">
                    {e.actor_name || e.actor_email ? (
                      <div>
                        <div className="text-foreground">{e.actor_name || e.actor_email}</div>
                        {e.actor_name && e.actor_email && (
                          <div className="text-xs text-muted-foreground">{e.actor_email}</div>
                        )}
                      </div>
                    ) : (
                      <span className="text-muted-foreground">System</span>
                    )}
                  </td>
                  <td className="px-3 py-2">
                    <span
                      className={`inline-block rounded px-2 py-0.5 text-xs font-medium ${
                        CATEGORY_BADGE[e.category] || 'bg-muted text-muted-foreground'
                      }`}
                    >
                      {eventLabel(e.event_type)}
                    </span>
                  </td>
                  <td className="px-3 py-2 text-xs text-muted-foreground">
                    {summarizeMeta(e.metadata)}
                    {e.ip ? <span className="ml-2 opacity-70">{e.ip}</span> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {/* Pagination */}
      {total > PAGE_SIZE && (
        <div className="flex items-center justify-between text-sm text-muted-foreground">
          <span>
            {page * PAGE_SIZE + 1}–{Math.min((page + 1) * PAGE_SIZE, total)} of {total}
          </span>
          <div className="flex gap-2">
            <button
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0}
              className="h-8 rounded-md border border-border px-3 hover:bg-accent/40 disabled:opacity-40"
            >
              Previous
            </button>
            <button
              onClick={() => setPage((p) => (p + 1 < totalPages ? p + 1 : p))}
              disabled={page + 1 >= totalPages}
              className="h-8 rounded-md border border-border px-3 hover:bg-accent/40 disabled:opacity-40"
            >
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
