import React, { useEffect, useMemo, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import {
  ArrowLeft, AlertCircle, Users, Filter, ListChecks,
  Pause, Play, XCircle,
} from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { useToast } from '@/lib/useToast';
import {
  Badge, Button, EmptyState, Input, PageHeader, Select, SpinnerBlock,
} from '@/components/ui';
import { useSegments } from './segmentsQueries';
import { useContentList } from './contentQueries';
import { useTopics } from './senderProfileQueries';
import SlideToConfirm from './SlideToConfirm';
import CheckList from './CheckList';
import CampaignAnalyticsPanel from './CampaignAnalyticsPanel';
import { campaignStatusVariant } from './CampaignsListPage';
import {
  useCampaign, useUpdateCampaign, useReadiness, useLaunchCampaign,
  useCampaignProgress, useCampaignProgressStream, useCampaignLifecycle, useABResults,
} from './campaignsQueries';
import { estimateAudience, isCampaignActive, type Campaign } from './campaignsApi';

const CampaignComposerPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  const { id } = useParams<{ id: string }>();
  const { data: campaign, isLoading, isError } = useCampaign(id);

  if (!loaded) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-4xl"><AccessDeniedPanel capability="marketing.manage" what="campaigns" /></div>;
  }
  if (isLoading) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (isError || !campaign) {
    return <div className="mx-auto w-full max-w-4xl"><EmptyState icon={AlertCircle} title="Campaign not found" description="It may have been deleted." /></div>;
  }
  // Editable before sending; a live monitor once launched.
  const editable = campaign.status === 'draft' || campaign.status === 'scheduled';
  return editable
    ? <CampaignEditor key={campaign.id} campaign={campaign} />
    : <CampaignMonitor key={campaign.id} campaign={campaign} />;
};

function BackLink() {
  const navigate = useNavigate();
  return (
    <button onClick={() => navigate('/marketing/campaigns')}
      className="mb-3 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
      <ArrowLeft aria-hidden className="h-4 w-4" /> Campaigns
    </button>
  );
}

// ── editor (draft / scheduled) ──────────────────────────────────────────────

function CampaignEditor({ campaign }: { campaign: Campaign }) {
  const toast = useToast();
  const update = useUpdateCampaign();
  const launch = useLaunchCampaign();
  const { data: segments = [] } = useSegments();
  const { data: content = [] } = useContentList();
  const { data: topics = [] } = useTopics();
  const readiness = useReadiness(campaign.id);

  const [name, setName] = useState(campaign.name);
  const [include, setInclude] = useState<Set<string>>(new Set(campaign.segment_ids));
  const [exclude, setExclude] = useState<Set<string>>(new Set(campaign.exclude_segment_ids));
  const [contentId, setContentId] = useState(campaign.content_id ?? '');
  const [topicId, setTopicId] = useState(campaign.topic_id ?? '');
  const [abTestPct, setAbTestPct] = useState(campaign.ab_test_pct ?? 0);
  const [abSubjectB, setAbSubjectB] = useState(campaign.ab_subject_b ?? '');
  const [abWindowHours, setAbWindowHours] = useState(campaign.ab_test_window_hours || 4);
  const abEnabled = abTestPct > 0;
  const [dirty, setDirty] = useState(false);

  const markDirty = () => setDirty(true);

  // Live audience estimate (debounced), on the current include/exclude selection.
  const includeArr = useMemo(() => [...include], [include]);
  const excludeArr = useMemo(() => [...exclude], [exclude]);
  const [debounced, setDebounced] = useState<{ inc: string[]; exc: string[] }>({ inc: includeArr, exc: excludeArr });
  useEffect(() => {
    const t = setTimeout(() => setDebounced({ inc: includeArr, exc: excludeArr }), 400);
    return () => clearTimeout(t);
  }, [includeArr, excludeArr]);
  const estimate = useQuery({
    queryKey: ['campaign-estimate', debounced.inc, debounced.exc],
    queryFn: () => estimateAudience(debounced.inc, debounced.exc),
    enabled: debounced.inc.length > 0,
    retry: false,
    staleTime: 10_000,
  });

  const toggle = (set: Set<string>, setter: (s: Set<string>) => void, other: Set<string>, otherSetter: (s: Set<string>) => void, id: string) => {
    const next = new Set(set);
    if (next.has(id)) next.delete(id);
    else {
      next.add(id);
      if (other.has(id)) { const o = new Set(other); o.delete(id); otherSetter(o); } // a segment can't be both
    }
    setter(next);
    markDirty();
  };

  const handleSave = () => {
    if (!name.trim()) { toast.error('Name is required'); return; }
    update.mutate(
      { id: campaign.id, input: {
        name: name.trim(),
        content_id: contentId || null,
        segment_ids: [...include],
        exclude_segment_ids: [...exclude],
        topic_id: topicId || null,
        ab_test_pct: abTestPct,
        ab_subject_b: abSubjectB,
        ab_test_window_hours: abWindowHours,
      } },
      { onSuccess: () => { setDirty(false); toast.show('Saved'); }, onError: (e) => toast.error((e as Error).message || 'Failed to save') },
    );
  };

  const handleLaunch = () => {
    launch.mutate(campaign.id, {
      onError: (e) => toast.error((e as Error).message || 'Failed to launch'),
      // onSuccess: the detail query invalidates → status flips to sending → the page
      // re-renders as the live monitor automatically.
    });
  };

  // !readiness.isFetching closes the stale-data window: after a Save the readiness
  // refetches, and the slide stays disabled until the fresh result lands (never
  // enabling send against a now-stale "ready").
  const canLaunch = !!readiness.data?.ready && !dirty && !launch.isPending && !readiness.isFetching;

  return (
    <div className="mx-auto w-full max-w-4xl">
      <BackLink />
      <PageHeader
        title="Edit campaign"
        description="Pick who receives it and what they get. The checklist must be all-green before you can send — every send is gated by suppression, consent, a verified domain, and a compiled email."
        actions={<Button onClick={handleSave} disabled={update.isPending}>Save</Button>}
      />

      <div className="space-y-6">
        <div>
          <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="camp-name">Name</label>
          <Input id="camp-name" value={name} onChange={(e) => { setName(e.target.value); markDirty(); }} className="max-w-md" />
        </div>

        {/* Audiences */}
        <div className="rounded-xl border border-border bg-card p-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold text-foreground">Audience</h3>
            <AudienceCount estimate={estimate} empty={include.size === 0} />
          </div>
          {segments.length === 0 ? (
            <p className="text-sm text-muted-foreground">No audiences yet — create one under Audiences first.</p>
          ) : (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <SegmentPicker title="Send to" hint="include" segments={segments} selected={include}
                onToggle={(sid) => toggle(include, setInclude, exclude, setExclude, sid)} />
              <SegmentPicker title="Exclude" hint="exclude" segments={segments} selected={exclude}
                onToggle={(sid) => toggle(exclude, setExclude, include, setInclude, sid)} />
            </div>
          )}
        </div>

        {/* Content + topic */}
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="camp-content">Email content</label>
            <Select id="camp-content" value={contentId} onChange={(e) => { setContentId(e.target.value); markDirty(); }}>
              <option value="">Select content…</option>
              {content.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </Select>
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="camp-topic">Topic (optional)</label>
            <Select id="camp-topic" value={topicId} onChange={(e) => { setTopicId(e.target.value); markDirty(); }}>
              <option value="">No topic</option>
              {topics.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
            </Select>
          </div>
        </div>

        {/* A/B test (subject line) */}
        <div className="rounded-xl border border-border bg-card p-4">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div className="min-w-0">
              <h3 className="text-sm font-semibold text-foreground">A/B test</h3>
              <p className="mt-0.5 text-xs text-muted-foreground">Test two subject lines on a fraction of your audience; the higher unique open rate wins and is sent to the rest.</p>
            </div>
            <label className="flex items-center gap-2 whitespace-nowrap text-xs text-muted-foreground">
              <input type="checkbox" className="h-3.5 w-3.5 rounded border-border" checked={abEnabled}
                onChange={(e) => { setAbTestPct(e.target.checked ? (abTestPct > 0 ? abTestPct : 20) : 0); markDirty(); }} />
              Enable
            </label>
          </div>
          {abEnabled && (
            <div className="mt-3 space-y-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="ab-subject">Variant B subject line</label>
                <Input id="ab-subject" value={abSubjectB} onChange={(e) => { setAbSubjectB(e.target.value); markDirty(); }} placeholder="An alternate subject to test against your content's subject" />
                <p className="mt-1 text-xs text-muted-foreground">Variant A uses your email content's subject; variant B uses this one. Same body for both.</p>
              </div>
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div>
                  <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="ab-pct">Test audience</label>
                  <Select id="ab-pct" value={String(abTestPct)} onChange={(e) => { setAbTestPct(Number(e.target.value)); markDirty(); }}>
                    <option value="10">10% (5% per variant)</option>
                    <option value="20">20% (10% per variant)</option>
                    <option value="30">30% (15% per variant)</option>
                    <option value="40">40% (20% per variant)</option>
                  </Select>
                </div>
                <div>
                  <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="ab-window">Test window</label>
                  <Select id="ab-window" value={String(abWindowHours)} onChange={(e) => { setAbWindowHours(Number(e.target.value)); markDirty(); }}>
                    <option value="4">4 hours</option>
                    <option value="8">8 hours</option>
                    <option value="24">24 hours</option>
                    <option value="48">48 hours</option>
                  </Select>
                </div>
              </div>
              {!abSubjectB.trim() && <p className="text-xs text-destructive">Add a variant B subject line to run the test.</p>}
            </div>
          )}
        </div>

        {/* Readiness checklist */}
        <div className="rounded-xl border border-border bg-card p-4">
          <h3 className="mb-3 text-sm font-semibold text-foreground">Pre-send checklist</h3>
          {readiness.isLoading ? (
            <SpinnerBlock label="Checking…" />
          ) : (
            <CheckList checks={readiness.data?.checks ?? []} />
          )}
        </div>

        {/* Launch */}
        <div className="rounded-xl border border-border bg-card p-4">
          {dirty && <p className="mb-2 text-xs text-amber-500">Save your changes to refresh the checklist and enable sending.</p>}
          <SlideToConfirm
            label={`Slide to send${readiness.data ? ` to ${readiness.data.audience_count.toLocaleString()}` : ''}`}
            confirmingLabel="Launching…"
            disabled={!canLaunch}
            confirming={launch.isPending}
            onConfirm={handleLaunch}
          />
          {!readiness.data?.ready && !readiness.isLoading && !dirty && (
            <p className="mt-2 text-center text-xs text-muted-foreground">Resolve every checklist item to enable sending.</p>
          )}
        </div>
      </div>
    </div>
  );
}

function SegmentPicker({ title, hint, segments, selected, onToggle }: {
  title: string; hint: 'include' | 'exclude';
  segments: import('./segmentsApi').Segment[]; selected: Set<string>; onToggle: (id: string) => void;
}) {
  return (
    <div>
      <p className="mb-2 text-xs font-medium text-muted-foreground">{title}</p>
      <div className="max-h-56 space-y-1 overflow-y-auto rounded-lg border border-border p-2">
        {segments.map((s) => (
          <label key={s.id} className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-accent">
            <input type="checkbox" checked={selected.has(s.id)} onChange={() => onToggle(s.id)}
              aria-label={`${hint} ${s.name}`} />
            <span className="flex-1 text-foreground">{s.name}</span>
            <Badge variant={s.type === 'dynamic' ? 'secondary' : 'outline'} className="gap-1">
              {s.type === 'dynamic' ? <Filter aria-hidden className="h-3 w-3" /> : <ListChecks aria-hidden className="h-3 w-3" />}
              {s.type}
            </Badge>
          </label>
        ))}
      </div>
    </div>
  );
}

function AudienceCount({ estimate, empty }: { estimate: ReturnType<typeof useQuery<number>>; empty: boolean }) {
  if (empty) return <Badge variant="outline">pick an audience</Badge>;
  if (estimate.isFetching && estimate.data === undefined) return <Badge variant="outline">counting…</Badge>;
  if (estimate.isError) return <Badge variant="destructive">count failed</Badge>;
  const n = estimate.data ?? 0;
  return <Badge variant="secondary" className="gap-1"><Users aria-hidden className="h-3 w-3" />{n.toLocaleString()} {n === 1 ? 'recipient' : 'recipients'}</Badge>;
}

// ── monitor (sending / paused / sent / canceled) ────────────────────────────

function CampaignMonitor({ campaign }: { campaign: Campaign }) {
  const toast = useToast();
  const { confirm, dialog } = useConfirm();
  const active = isCampaignActive(campaign.status);
  useCampaignProgressStream(campaign.id, active);
  const progress = useCampaignProgress(campaign.id, campaign.status);
  const pause = useCampaignLifecycle('pause');
  const resume = useCampaignLifecycle('resume');
  const cancel = useCampaignLifecycle('cancel');

  const counts = progress.data?.counts ?? {};
  const total = progress.data?.total ?? 0;
  const done = (counts.sent ?? 0) + (counts.failed ?? 0) + (counts.suppressed ?? 0) + (counts.skipped ?? 0);
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;
  // Once the AUTHORITATIVE detail status is terminal, trust it (a missed final SSE
  // frame could otherwise leave the best-effort progress status stuck on 'sending' at
  // 100%). While still active, prefer the live progress status — `||` (not `??`) so a
  // status-less frame falls back to the detail status instead of blanking the badge.
  const status = isCampaignActive(campaign.status) ? (progress.data?.status || campaign.status) : campaign.status;

  const onCancel = async () => {
    const ok = await confirm({ title: 'Cancel campaign', body: 'Stop this campaign? Recipients not yet sent will not receive it.', confirmLabel: 'Cancel campaign', tone: 'danger' });
    if (!ok) return;
    cancel.mutate(campaign.id, { onError: (e) => toast.error((e as Error).message || 'Failed to cancel') });
  };

  return (
    <div className="mx-auto w-full max-w-4xl">
      {dialog}
      <BackLink />
      <PageHeader
        title={campaign.name}
        description="Live send progress. Recipient state is the durable authority — this reflects what has actually been sent."
        actions={<Badge variant={campaignStatusVariant(status)}>{status}</Badge>}
      />

      <div className="rounded-xl border border-border bg-card p-4">
        <div className="mb-2 flex items-center justify-between text-sm">
          <span className="font-medium text-foreground">{done.toLocaleString()} / {total.toLocaleString()}</span>
          <span className="text-muted-foreground">{pct}%</span>
        </div>
        <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
          <div className={`h-full transition-[width] ${status === 'canceled' ? 'bg-muted-foreground/40' : 'bg-primary'}`} style={{ width: `${pct}%` }} />
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <CountChip label="Sent" value={counts.sent ?? 0} variant="success" />
          <CountChip label="Pending" value={counts.pending ?? 0} variant="secondary" />
          <CountChip label="Processing" value={counts.processing ?? 0} variant="outline" />
          <CountChip label="Suppressed" value={counts.suppressed ?? 0} variant="warning" />
          <CountChip label="Failed" value={counts.failed ?? 0} variant="destructive" />
        </div>
      </div>

      <div className="mt-4 flex gap-2">
        {status === 'sending' && (
          <Button variant="outline" onClick={() => pause.mutate(campaign.id, { onError: (e) => toast.error((e as Error).message || 'Failed to pause') })} disabled={pause.isPending}>
            <Pause aria-hidden /> Pause
          </Button>
        )}
        {status === 'paused' && (
          <Button variant="outline" onClick={() => resume.mutate(campaign.id, { onError: (e) => toast.error((e as Error).message || 'Failed to resume') })} disabled={resume.isPending}>
            <Play aria-hidden /> Resume
          </Button>
        )}
        {(status === 'sending' || status === 'paused') && (
          <Button variant="ghost" onClick={onCancel} disabled={cancel.isPending} className="text-destructive hover:text-destructive">
            <XCircle aria-hidden /> Cancel
          </Button>
        )}
      </div>

      {campaign.ab_test_pct > 0 && <CampaignABPanel campaign={campaign} />}
      {status === 'sent' && <CampaignAnalyticsPanel campaignId={campaign.id} />}
    </div>
  );
}

function CountChip({ label, value, variant }: { label: string; value: number; variant: 'success' | 'secondary' | 'outline' | 'warning' | 'destructive' }) {
  return <Badge variant={variant} className="gap-1">{label}: <span className="tabular-nums">{value.toLocaleString()}</span></Badge>;
}

function CampaignABPanel({ campaign }: { campaign: Campaign }) {
  const ab = useABResults(campaign.id, campaign.ab_test_pct > 0);
  const d = ab.data;
  if (!d || !d.enabled) return null;

  const a = d.variant_a ?? { variant: 'A', sent: 0, opened: 0, clicked: 0 };
  const b = d.variant_b ?? { variant: 'B', sent: 0, opened: 0, clicked: 0 };
  const rate = (s: { sent: number; opened: number }) => (s.sent > 0 ? (s.opened / s.sent) * 100 : 0);
  const decided = !!d.winner;
  const lead = decided ? d.winner : d.projected_winner;

  return (
    <div className="mt-4 rounded-xl border border-border bg-card p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold text-foreground">A/B test — subject line</h3>
        {decided
          ? <Badge variant="success">Winner: {d.winner}{d.significant ? ' · significant' : ''}</Badge>
          : <Badge variant="secondary">Testing…</Badge>}
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <ABVariantRow label="A — content subject" stat={a} rate={rate(a)} isLead={lead === 'A'} />
        <ABVariantRow label={`B — “${d.subject_b || ''}”`} stat={b} rate={rate(b)} isLead={lead === 'B'} />
      </div>
      <p className="mt-3 text-xs text-muted-foreground">
        {decided
          ? `Winner ${d.winner} was sent to the remaining audience${d.significant ? ' with 95% confidence.' : d.reason === 'cell_too_small' ? ' (test cells were too small to reach significance — the higher rate was chosen).' : ' (not statistically significant — the higher rate was chosen).'}`
          : `The higher unique open rate wins after the ${campaign.ab_test_window_hours}-hour window; currently leaning ${lead}${d.significant ? ' (significant).' : d.reason === 'cell_too_small' ? ' (cells still too small for significance).' : ' (not yet significant).'}`}
      </p>
    </div>
  );
}

function ABVariantRow({ label, stat, rate, isLead }: { label: string; stat: { sent: number; opened: number }; rate: number; isLead: boolean }) {
  return (
    <div className={`rounded-lg border p-3 ${isLead ? 'border-primary' : 'border-border'}`}>
      <div className="flex items-center justify-between gap-2">
        <div className="truncate text-xs font-medium text-foreground">{label}</div>
        {isLead && <Badge variant="success">Lead</Badge>}
      </div>
      <div className="mt-1 text-xl font-semibold tabular-nums text-foreground">{rate.toFixed(1)}%</div>
      <div className="mt-0.5 text-xs text-muted-foreground">{stat.opened.toLocaleString()} opens / {stat.sent.toLocaleString()} sent</div>
    </div>
  );
}

export default CampaignComposerPage;
