import { useState } from 'react';
import { SpinnerBlock } from '@/components/ui';
import { useCampaignAnalytics } from './analyticsQueries';

type Tone = 'default' | 'success' | 'warning' | 'destructive';

function StatTile({ label, value, sub, tone = 'default' }: { label: string; value: string | number; sub?: string; tone?: Tone }) {
  const toneClass =
    tone === 'success'
      ? 'text-emerald-600 dark:text-emerald-400'
      : tone === 'warning'
        ? 'text-amber-600 dark:text-amber-400'
        : tone === 'destructive'
          ? 'text-destructive'
          : 'text-foreground';
  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="text-xs font-medium uppercase tracking-wide text-muted-foreground">{label}</div>
      <div className={`mt-1 text-2xl font-semibold tabular-nums ${toneClass}`}>{value}</div>
      {sub && <div className="mt-0.5 text-xs text-muted-foreground">{sub}</div>}
    </div>
  );
}

const pct = (n: number, d: number) => (d > 0 ? `${((n / d) * 100).toFixed(1)}%` : '—');

/** Per-campaign engagement dashboard, mounted under the send monitor once a campaign is
 *  terminal. Clicks are the headline; opens are directional with an MPP-exclude toggle. */
export default function CampaignAnalyticsPanel({ campaignId }: { campaignId: string }) {
  const { data, isLoading, isError } = useCampaignAnalytics(campaignId);
  const [excludeMPP, setExcludeMPP] = useState(false);

  if (isLoading) return <div className="mt-6"><SpinnerBlock label="Loading analytics…" /></div>;
  if (isError || !data) return <p className="mt-6 text-sm text-muted-foreground">Engagement analytics are unavailable for this campaign.</p>;

  const opens = excludeMPP ? data.opened_engaged : data.opened;
  const openLabel = excludeMPP ? 'Opened (engaged)' : 'Opened';

  return (
    <div className="mt-6 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-foreground">Engagement</h3>
        <label className="flex items-center gap-2 text-xs text-muted-foreground">
          <input
            type="checkbox"
            className="h-3.5 w-3.5 rounded border-border"
            checked={excludeMPP}
            onChange={(e) => setExcludeMPP(e.target.checked)}
          />
          Exclude likely automated opens (Apple MPP)
        </label>
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
        <StatTile label="Delivered" value={data.delivered.toLocaleString()} sub={`${pct(data.delivered, data.sent)} of ${data.sent.toLocaleString()} sent`} />
        <StatTile label={openLabel} value={opens.toLocaleString()} sub={`${pct(opens, data.delivered)} open rate`} />
        <StatTile label="Clicked" value={data.clicked.toLocaleString()} sub={`${pct(data.clicked, data.delivered)} click rate`} tone="success" />
        <StatTile label="Bounced" value={data.bounced.toLocaleString()} sub={pct(data.bounced, data.sent)} tone={data.bounced > 0 ? 'warning' : 'default'} />
        <StatTile label="Complained" value={data.complained.toLocaleString()} sub={pct(data.complained, data.delivered)} tone={data.complained > 0 ? 'destructive' : 'default'} />
      </div>

      <p className="text-xs text-muted-foreground">
        Clicks are the headline metric — opens are directional (Apple Mail Privacy preloads the tracking pixel, inflating them).
        {data.opened_total > data.opened
          ? ` ${data.opened_total.toLocaleString()} total opens across ${data.opened.toLocaleString()} recipients.`
          : ''}
        {data.delivered === 0 ? ' No delivery events yet — engagement requires open/click tracking enabled on the sending domain.' : ''}
      </p>
    </div>
  );
}
