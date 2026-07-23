import React, { useState } from 'react';
import { AlertCircle, CheckCircle2, Copy, Check, Globe, Plus, RefreshCw, ShieldCheck, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import {
  Badge, Button, EmptyState, Input, PageHeader, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';
import { useDomains, useAddDomain, useVerifyDomain, useRefreshDomain, useRemoveDomain } from './domainsQueries';
import { sendingReasonLabel, type EmailDomain } from './domainsApi';

export const SendingDomainsPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) {
    return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  }
  if (!can('marketing.manage')) {
    return (
      <div className="mx-auto w-full max-w-5xl">
        <AccessDeniedPanel capability="marketing.manage" what="sending domains" />
      </div>
    );
  }
  return <SendingDomainsContent />;
};

function statusVariant(status: string): 'success' | 'warning' | 'secondary' | 'destructive' {
  switch (status) {
    case 'verified': return 'success';
    case 'pending': case 'partially_verified': return 'warning';
    case 'failed': case 'partially_failed': case 'temporary_failure': return 'destructive';
    default: return 'secondary'; // not_started
  }
}

const CheckChip: React.FC<{ ok: boolean; label: string }> = ({ ok, label }) => (
  <Badge variant={ok ? 'success' : 'secondary'} title={ok ? `${label} verified` : `${label} not verified`}>
    {ok ? '✓' : '○'} {label}
  </Badge>
);

const CopyButton: React.FC<{ value: string }> = ({ value }) => {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      if (!navigator.clipboard) throw new Error('clipboard unavailable');
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Leave the state uncopied — the value stays on screen to select manually.
    }
  };
  return (
    <button
      type="button"
      onClick={copy}
      title="Copy value"
      className="inline-flex shrink-0 items-center rounded p-1 text-muted-foreground transition-colors hover:text-foreground"
    >
      {copied ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
    </button>
  );
};

const SendingDomainsContent: React.FC = () => {
  const { data, isLoading, isError } = useDomains();
  const addMut = useAddDomain();
  const verifyMut = useVerifyDomain();
  const refreshMut = useRefreshDomain();
  const removeMut = useRemoveDomain();
  const [newDomain, setNewDomain] = useState('');
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);

  const domains = data?.data ?? [];
  const canSend = data?.meta.can_bulk_send ?? false;
  const reason = data?.meta.reason ?? '';

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const handleAdd = () => {
    const domain = newDomain.trim();
    if (!domain) return;
    addMut.mutate({ domain }, {
      onSuccess: () => { setNewDomain(''); showToast(`${domain} added — publish the DNS records below, then re-check.`); },
      onError: (e) => showToast((e as Error).message || 'Failed to add domain', 'error'),
    });
  };

  const handleRefresh = (d: EmailDomain) => {
    refreshMut.mutate(d.id, {
      onSuccess: (updated) => showToast(updated.status === 'verified' ? `${d.domain} is verified 🎉` : `Re-checked ${d.domain} — status: ${updated.status}`),
      onError: (e) => showToast((e as Error).message || 'Failed to re-check', 'error'),
    });
  };

  const handleVerify = (d: EmailDomain) => {
    verifyMut.mutate(d.id, {
      onSuccess: () => showToast(`Verification triggered for ${d.domain}. DNS checks can take up to 72h.`),
      onError: (e) => showToast((e as Error).message || 'Failed to trigger verification', 'error'),
    });
  };

  const handleRemove = (d: EmailDomain) => {
    if (!confirm(`Remove ${d.domain}? Marketing sends from this domain will stop, and it is removed from Resend.`)) return;
    removeMut.mutate(d.id, {
      onSuccess: () => showToast(`${d.domain} removed`),
      onError: (e) => showToast((e as Error).message || 'Failed to remove domain', 'error'),
    });
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error'
            ? <AlertCircle aria-hidden className="h-4 w-4 shrink-0 text-destructive" />
            : <CheckCircle2 aria-hidden className="h-4 w-4 shrink-0 text-primary" />}
          {toast.msg}
        </div>
      )}

      <PageHeader
        title="Sending domains"
        description="Verify a domain you own (SPF, DKIM, DMARC) so marketing email sends from your brand — not a shared platform address. Reputation is domain-first."
      />

      {/* Bulk-send readiness banner */}
      {!isLoading && !isError && (
        <div className={`mb-6 flex items-start gap-3 rounded-xl border p-4 text-sm ${canSend ? 'border-emerald-500/30 bg-emerald-500/10' : 'border-amber-500/30 bg-amber-500/10'}`}>
          <ShieldCheck aria-hidden className={`mt-0.5 h-5 w-5 shrink-0 ${canSend ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`} />
          <div>
            <p className="font-medium text-foreground">
              {canSend ? 'Marketing sending is enabled' : 'Marketing sending is blocked'}
            </p>
            <p className="text-muted-foreground">
              {canSend
                ? 'At least one domain is verified with SPF, DKIM, and DMARC.'
                : `You can’t send marketing campaigns yet. ${sendingReasonLabel(reason)}`}
            </p>
          </div>
        </div>
      )}

      {/* Add form */}
      <div className="mb-6 flex flex-wrap items-end gap-2 rounded-xl border border-border bg-card p-4">
        <div className="min-w-[16rem] flex-1">
          <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="new-domain">
            Domain to send from
          </label>
          <Input
            id="new-domain"
            value={newDomain}
            onChange={(e) => setNewDomain(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') handleAdd(); }}
            placeholder="yourcompany.com"
          />
        </div>
        <Button onClick={handleAdd} disabled={addMut.isPending || !newDomain.trim()}>
          <Plus aria-hidden /> Add domain
        </Button>
      </div>

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : isError ? (
        <div className="flex items-start gap-3 rounded-xl border border-destructive/30 bg-destructive/10 p-4 text-sm">
          <AlertCircle aria-hidden className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
          <div>
            <p className="font-medium text-foreground">Couldn’t load sending domains</p>
            <p className="text-muted-foreground">Something went wrong fetching your domains. Reload the page to try again — your domains haven’t been lost.</p>
          </div>
        </div>
      ) : domains.length === 0 ? (
        <EmptyState
          icon={Globe}
          title="No sending domains yet"
          description="Add a domain you own to start authenticating your marketing email."
        />
      ) : (
        <div className="space-y-4">
          {domains.map((d) => (
            <DomainCard
              key={d.id}
              domain={d}
              busy={refreshMut.isPending || verifyMut.isPending || removeMut.isPending}
              onRefresh={() => handleRefresh(d)}
              onVerify={() => handleVerify(d)}
              onRemove={() => handleRemove(d)}
            />
          ))}
        </div>
      )}
    </div>
  );
};

const DomainCard: React.FC<{
  domain: EmailDomain;
  busy: boolean;
  onRefresh: () => void;
  onVerify: () => void;
  onRemove: () => void;
}> = ({ domain: d, busy, onRefresh, onVerify, onRemove }) => {
  const verified = d.status === 'verified';
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Globe aria-hidden className="h-4 w-4 text-muted-foreground" />
            <h3 className="truncate font-semibold text-foreground">{d.domain}</h3>
            <Badge variant={statusVariant(d.status)}>{d.status.replace(/_/g, ' ')}</Badge>
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-1.5">
            <CheckChip ok={d.spf_verified} label="SPF" />
            <CheckChip ok={d.dkim_verified} label="DKIM" />
            <CheckChip ok={!!d.dmarc_policy} label={d.dmarc_policy ? `DMARC (p=${d.dmarc_policy})` : 'DMARC'} />
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" onClick={onRefresh} disabled={busy}>
            <RefreshCw aria-hidden className="h-3.5 w-3.5" /> Re-check
          </Button>
          {!verified && (
            <Button variant="outline" size="sm" onClick={onVerify} disabled={busy}>Verify with Resend</Button>
          )}
          <Button variant="ghost" size="sm" onClick={onRemove} disabled={busy} className="text-destructive hover:text-destructive" title="Remove domain">
            <Trash2 aria-hidden className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {/* DNS records — shown until verified (that's when the user needs them) */}
      {!verified && d.dns_records?.length > 0 && (
        <div className="mt-4">
          <p className="mb-2 text-xs text-muted-foreground">
            Add these DNS records at your domain registrar, then click <strong>Re-check</strong>. DMARC (a{' '}
            <code className="rounded bg-muted px-1">_dmarc</code> TXT record with at least <code className="rounded bg-muted px-1">p=none</code>) must be added separately — Resend manages only SPF and DKIM.
          </p>
          <TableShell>
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>Type</TableHead>
                  <TableHead>Name / Host</TableHead>
                  <TableHead>Value</TableHead>
                  <TableHead>Status</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {d.dns_records.map((r, i) => (
                  <TableRow key={i}>
                    <TableCell className="whitespace-nowrap font-mono text-xs">
                      {r.type}{typeof r.priority === 'number' ? ` (prio ${r.priority})` : ''}
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      <span className="inline-flex items-center gap-1"><span className="break-all">{r.name || '@'}</span><CopyButton value={r.name} /></span>
                    </TableCell>
                    <TableCell className="font-mono text-xs">
                      <span className="inline-flex items-start gap-1"><span className="break-all">{r.value}</span><CopyButton value={r.value} /></span>
                    </TableCell>
                    <TableCell>
                      <Badge variant={r.status === 'verified' ? 'success' : 'secondary'}>{r.status || 'pending'}</Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableShell>
        </div>
      )}
    </div>
  );
};

export default SendingDomainsPage;
