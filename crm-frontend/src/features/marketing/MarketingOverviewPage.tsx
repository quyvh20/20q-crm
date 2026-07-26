import React from 'react';
import { Link } from 'react-router-dom';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { Badge, Button, PageHeader, SpinnerBlock } from '@/components/ui';
import { useDeliverability } from './analyticsQueries';
import { useDomains } from './domainsQueries';

const fmtPct = (r: number) => `${(r * 100).toFixed(2)}%`;

const MarketingOverviewPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-4xl"><AccessDeniedPanel capability="marketing.manage" what="deliverability" /></div>;
  }
  return <OverviewContent />;
};

const OverviewContent: React.FC = () => {
  const { data: deliv, isLoading, isError } = useDeliverability();
  const { data: domains } = useDomains();
  const canSend = domains?.meta.can_bulk_send ?? false;

  return (
    <div className="mx-auto w-full max-w-4xl">
      <PageHeader title="Marketing health" description="Sending readiness and deliverability across your marketing lane." />

      {/* Sending readiness (domain config) */}
      <div className="mb-4 rounded-xl border border-border bg-card p-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div className="min-w-0">
            <div className="text-sm font-semibold text-foreground">Sending domain</div>
            <div className="mt-0.5 text-xs text-muted-foreground">
              {canSend ? 'Verified and cleared for marketing sends.' : 'Not ready — verify a sending domain (SPF + DKIM + DMARC) before sending.'}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant={canSend ? 'success' : 'warning'}>{canSend ? 'Ready' : 'Not ready'}</Badge>
            <Link to="/marketing/domains"><Button variant="outline">Domains</Button></Link>
          </div>
        </div>
      </div>

      {/* Deliverability rates */}
      {isLoading ? (
        <SpinnerBlock label="Loading deliverability…" />
      ) : isError || !deliv ? (
        <div className="rounded-xl border border-border bg-card p-4 text-sm text-muted-foreground">Deliverability metrics are unavailable.</div>
      ) : (
        <div className="rounded-xl border border-border bg-card p-4">
          <div className="mb-3 text-sm font-semibold text-foreground">
            Deliverability ({deliv.thresholds.window_days}-day rolling)
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <RateRow label="Complaint rate" value={deliv.rates.complaint_rate} denom={deliv.rates.delivered}
              warn={deliv.thresholds.warn_rate} pause={deliv.thresholds.pause_rate} minVolume={deliv.thresholds.warn_min_volume} />
            <RateRow label="Hard bounce rate" value={deliv.rates.bounce_rate} denom={deliv.rates.sent}
              warn={deliv.thresholds.warn_rate} pause={deliv.thresholds.pause_rate} minVolume={deliv.thresholds.warn_min_volume} />
          </div>
          <p className="mt-3 text-xs text-muted-foreground">
            Warn at {fmtPct(deliv.thresholds.warn_rate)}, auto-pause at {fmtPct(deliv.thresholds.pause_rate)}. A rate over fewer
            than {deliv.thresholds.warn_min_volume.toLocaleString()} messages is shown but does not trip the auto-pause.
          </p>
        </div>
      )}
    </div>
  );
};

function RateRow({ label, value, denom, warn, pause, minVolume }: { label: string; value: number; denom: number; warn: number; pause: number; minVolume: number }) {
  const belowFloor = denom < minVolume;
  const tone = belowFloor
    ? 'text-muted-foreground'
    : value >= pause
      ? 'text-destructive'
      : value >= warn
        ? 'text-amber-600 dark:text-amber-400'
        : 'text-emerald-600 dark:text-emerald-400';
  return (
    <div className="rounded-lg border border-border p-3">
      <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className={`mt-1 text-xl font-semibold tabular-nums ${tone}`}>{(value * 100).toFixed(2)}%</div>
      <div className="mt-0.5 text-xs text-muted-foreground">
        over {denom.toLocaleString()} {belowFloor ? `messages (below ${minVolume.toLocaleString()} floor)` : 'messages'}
      </div>
    </div>
  );
}

export default MarketingOverviewPage;
