import React, { useEffect, useMemo, useState } from 'react';
import { AlertCircle, CheckCircle2, ShieldOff, Trash2, Plus } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import {
  Badge, Button, EmptyState, Input, PageHeader, Select, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';
import { useSuppressions, useAddSuppression, useRemoveSuppression } from './queries';
import { SUPPRESSION_REASONS, reasonLabel, type Suppression } from './api';

/** Marketing suppression list (M1). Every /api/marketing/* route requires
 *  marketing.manage, so the whole page is gated — a member without it gets the
 *  friendly denied panel instead of a surface that 403s. Wait for the capability
 *  fetch to settle before deciding, so a deep-linked marketer doesn't flash the
 *  denied panel (the SettingsLayout trap). */
export const SuppressionListPage: React.FC = () => {
  const { can, loaded } = usePermissions();

  if (!loaded) {
    return (
      <div className="mx-auto w-full max-w-5xl">
        <SpinnerBlock label="Loading…" />
      </div>
    );
  }
  if (!can('marketing.manage')) {
    return (
      <div className="mx-auto w-full max-w-5xl">
        <AccessDeniedPanel capability="marketing.manage" what="the suppression list" />
      </div>
    );
  }
  return <SuppressionListContent />;
};

function reasonVariant(reason: string): 'secondary' | 'outline' | 'destructive' | 'warning' {
  switch (reason) {
    case 'complaint':
    case 'hard_bounce':
      return 'destructive';
    case 'unsubscribe':
      return 'warning';
    case 'soft_bounce':
      return 'outline';
    default:
      return 'secondary';
  }
}

const SuppressionListContent: React.FC = () => {
  const [rawQ, setRawQ] = useState('');
  const [q, setQ] = useState('');
  const [reasonFilter, setReasonFilter] = useState('');
  const [newEmail, setNewEmail] = useState('');
  const [newReason, setNewReason] = useState('manual');
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);

  // Debounce the search so the list doesn't query on every keystroke.
  useEffect(() => {
    const t = setTimeout(() => setQ(rawQ.trim()), 250);
    return () => clearTimeout(t);
  }, [rawQ]);

  const params = useMemo(() => ({ q, reason: reasonFilter }), [q, reasonFilter]);
  const { data, isLoading } = useSuppressions(params);
  const addMutation = useAddSuppression();
  const removeMutation = useRemoveSuppression();

  const rows = data?.data ?? [];
  const total = data?.meta.total ?? 0;

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const handleAdd = () => {
    const email = newEmail.trim();
    if (!email) return;
    addMutation.mutate(
      { email, reason: newReason },
      {
        onSuccess: (r) => {
          setNewEmail('');
          showToast(r.already ? `${r.suppression.email} was already suppressed` : `${r.suppression.email} suppressed`);
        },
        onError: (e) => showToast((e as Error).message || 'Failed to add suppression', 'error'),
      },
    );
  };

  const handleRemove = (s: Suppression) => {
    if (!confirm(`Remove the suppression for ${s.email}? They may receive marketing email again.`)) return;
    removeMutation.mutate(s.id, {
      onSuccess: () => showToast(`Suppression removed for ${s.email}`),
      onError: (e) => showToast((e as Error).message || 'Failed to remove suppression', 'error'),
    });
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error' ? (
            <AlertCircle aria-hidden className="h-4 w-4 shrink-0 text-destructive" />
          ) : (
            <CheckCircle2 aria-hidden className="h-4 w-4 shrink-0 text-primary" />
          )}
          {toast.msg}
        </div>
      )}

      <PageHeader
        title="Suppression list"
        description="Addresses that must never receive marketing email — unsubscribes, complaints, bounces, and manual do-not-mail entries. Consulted live before every send."
      />

      {/* Add form */}
      <div className="mb-6 flex flex-wrap items-end gap-2 rounded-xl border border-border bg-card p-4">
        <div className="min-w-[16rem] flex-1">
          <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="supp-email">
            Email address
          </label>
          <Input
            id="supp-email"
            type="email"
            value={newEmail}
            onChange={(e) => setNewEmail(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') handleAdd(); }}
            placeholder="person@example.com"
          />
        </div>
        <div className="w-56">
          <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="supp-reason">
            Reason
          </label>
          <Select id="supp-reason" value={newReason} onChange={(e) => setNewReason(e.target.value)}>
            {SUPPRESSION_REASONS.map((r) => (
              <option key={r.value} value={r.value}>{r.label}</option>
            ))}
          </Select>
        </div>
        <Button onClick={handleAdd} disabled={addMutation.isPending || !newEmail.trim()}>
          <Plus aria-hidden /> Suppress
        </Button>
      </div>

      {/* Filters */}
      <div className="mb-4 flex flex-wrap items-center gap-2">
        <Input
          value={rawQ}
          onChange={(e) => setRawQ(e.target.value)}
          placeholder="Search by email…"
          className="max-w-xs"
          aria-label="Search suppressions by email"
        />
        <Select
          value={reasonFilter}
          onChange={(e) => setReasonFilter(e.target.value)}
          className="w-48"
          aria-label="Filter by reason"
        >
          <option value="">All reasons</option>
          {SUPPRESSION_REASONS.map((r) => (
            <option key={r.value} value={r.value}>{r.label}</option>
          ))}
        </Select>
        <span className="ml-auto text-xs text-muted-foreground">{total} suppressed</span>
      </div>

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={ShieldOff}
          title={q || reasonFilter ? 'No matching suppressions' : 'No suppressions yet'}
          description={
            q || reasonFilter
              ? 'Try a different search or reason filter.'
              : 'Unsubscribes, bounces, and complaints will appear here automatically once sending is live. You can also add addresses manually above.'
          }
        />
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Email</TableHead>
                <TableHead>Reason</TableHead>
                <TableHead>Scope</TableHead>
                <TableHead>Source</TableHead>
                <TableHead>Added</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-medium text-foreground">{s.email}</TableCell>
                  <TableCell>
                    <Badge variant={reasonVariant(s.reason)}>{reasonLabel(s.reason)}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground">
                    {s.scope === 'all' ? 'All email' : 'Marketing'}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground">{s.source || '—'}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {new Date(s.created_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      variant="ghost"
                      size="sm"
                      onClick={() => handleRemove(s)}
                      disabled={removeMutation.isPending}
                      title="Remove suppression"
                      className="text-destructive hover:text-destructive"
                    >
                      <Trash2 aria-hidden className="h-4 w-4" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableShell>
      )}
    </div>
  );
};

export default SuppressionListPage;
