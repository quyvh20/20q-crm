import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { ScrollText } from 'lucide-react';
import { getAuditEvents, exportAuditCsv, type AuditEventFilters } from '../../lib/api';
import {
  Badge, Button, EmptyState, Input, Select, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive' | 'success' | 'warning';

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

// Map each audit category to a Badge variant, preserving the semantic color
// distinctions the old hand-rolled tint map carried.
const CATEGORY_VARIANT: Record<string, BadgeVariant> = {
  admin: 'default',
  auth: 'success',
  security: 'warning',
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
  const [exportError, setExportError] = useState('');

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
    setExportError('');
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
      setExportError(e instanceof Error ? e.message : 'Export failed');
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

      {exportError && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{exportError}</div>
      )}

      {/* Filters */}
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Activity
          <Select
            value={category}
            onChange={(e) => resetPageAnd(() => setCategory(e.target.value))}
            className="w-auto"
          >
            {CATEGORIES.map((c) => (
              <option key={c.value} value={c.value}>{c.label}</option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          From
          <Input
            type="date"
            value={from}
            onChange={(e) => resetPageAnd(() => setFrom(e.target.value))}
            className="w-auto"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          To
          <Input
            type="date"
            value={to}
            onChange={(e) => resetPageAnd(() => setTo(e.target.value))}
            className="w-auto"
          />
        </label>
        <div className="ml-auto">
          <Button variant="outline" onClick={handleExport} disabled={exporting}>
            {exporting ? 'Exporting…' : 'Export CSV'}
          </Button>
        </div>
      </div>

      {/* Table */}
      {isError ? (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">
          {error instanceof Error ? error.message : 'Failed to load audit log'}
        </div>
      ) : isLoading ? (
        <SpinnerBlock />
      ) : events.length === 0 ? (
        <EmptyState icon={ScrollText} title="No activity recorded for this filter yet." />
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>When</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Event</TableHead>
                <TableHead>Details</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {events.map((e) => (
                <TableRow key={e.id}>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {new Date(e.created_at).toLocaleString()}
                  </TableCell>
                  <TableCell>
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
                  </TableCell>
                  <TableCell>
                    <Badge variant={CATEGORY_VARIANT[e.category] ?? 'secondary'}>
                      {eventLabel(e.event_type)}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {summarizeMeta(e.metadata)}
                    {e.ip ? <span className="ml-2 opacity-70">{e.ip}</span> : null}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableShell>
      )}

      {/* Pagination */}
      {total > PAGE_SIZE && (
        <div className="flex items-center justify-between text-sm text-muted-foreground">
          <span>
            {page * PAGE_SIZE + 1}–{Math.min((page + 1) * PAGE_SIZE, total)} of {total}
          </span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.max(0, p - 1))} disabled={page === 0}>
              Previous
            </Button>
            <Button variant="outline" size="sm" onClick={() => setPage((p) => (p + 1 < totalPages ? p + 1 : p))} disabled={page + 1 >= totalPages}>
              Next
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
