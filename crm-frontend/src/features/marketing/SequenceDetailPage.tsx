import React from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Badge, Button, PageHeader, SpinnerBlock } from '@/components/ui';
import { useSequence, useSequenceProgress, useCancelSequence } from './sequencesQueries';
import { SEQUENCE_STATUS_LABEL, sequenceStatusVariant, type BadgeVariant } from './sequencesApi';

function CountChip({ label, value, variant }: { label: string; value: number; variant: BadgeVariant }) {
  return (
    <Badge variant={variant} className="gap-1">
      {label}: <span className="tabular-nums">{value.toLocaleString()}</span>
    </Badge>
  );
}

const SequenceDetailPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { confirm, dialog } = useConfirm();
  const { data: seq, isLoading } = useSequence(id);
  const progress = useSequenceProgress(id, seq?.status);
  const cancelMutation = useCancelSequence();

  if (!loaded) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) return <div className="mx-auto w-full max-w-4xl"><AccessDeniedPanel capability="marketing.manage" what="sequences" /></div>;
  if (isLoading) return <div className="mx-auto w-full max-w-4xl"><SpinnerBlock label="Loading sequence…" /></div>;
  if (!seq) return <div className="mx-auto w-full max-w-4xl"><p className="text-sm text-muted-foreground">Sequence enrollment not found.</p></div>;

  const p = progress.data;
  const total = p?.total ?? 0;
  const active = p?.active ?? 0;
  const completed = p?.completed ?? 0;
  const failed = p?.failed ?? 0;
  const skipped = p?.skipped ?? 0;
  const done = completed + failed + skipped;
  const pct = total > 0 ? Math.round((done / total) * 100) : 0;
  const cancellable = seq.status === 'active' || seq.status === 'blocked';

  const handleCancel = async () => {
    const ok = await confirm({
      title: 'Stop enrolling?',
      body: 'New contacts will stop being enrolled into this sequence. Contacts already enrolled keep running to completion.',
      confirmLabel: 'Stop enrolling',
      tone: 'danger',
    });
    if (!ok) return;
    cancelMutation.mutate(seq.id);
  };

  return (
    <div className="mx-auto w-full max-w-4xl">
      <PageHeader
        title={seq.sequence_name || 'Sequence'}
        description={`Audience: ${seq.segment_name || '—'}`}
        actions={
          <div className="flex items-center gap-2">
            <Badge variant={sequenceStatusVariant(seq.status)}>{SEQUENCE_STATUS_LABEL[seq.status]}</Badge>
            {cancellable && (
              <Button variant="outline" onClick={handleCancel} disabled={cancelMutation.isPending}>Stop enrolling</Button>
            )}
          </div>
        }
      />

      {seq.last_error && seq.status !== 'completed' && (
        <div className="mb-4 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-foreground">
          {seq.last_error}
        </div>
      )}

      <div className="rounded-xl border border-border bg-card p-4">
        <div className="mb-2 flex items-center justify-between text-sm">
          <span className="font-medium text-foreground">{done.toLocaleString()} / {total.toLocaleString()} finished</span>
          <span className="text-muted-foreground">{pct}%</span>
        </div>
        <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
          <div className="h-full bg-primary transition-[width]" style={{ width: `${pct}%` }} />
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <CountChip label="Enrolled" value={total} variant="secondary" />
          <CountChip label="In progress" value={active} variant="outline" />
          <CountChip label="Completed" value={completed} variant="success" />
          <CountChip label="Skipped" value={skipped} variant="warning" />
          <CountChip label="Failed" value={failed} variant="destructive" />
        </div>
        <p className="mt-3 text-xs text-muted-foreground">
          “Skipped” includes contacts dropped at a send step because they unsubscribed or have no lawful basis to be mailed —
          the sequence keeps running for them but sends nothing.
        </p>
      </div>

      <div className="mt-4">
        <Button variant="ghost" onClick={() => navigate('/marketing/sequences')}>← All sequences</Button>
      </div>
      {dialog}
    </div>
  );
};

export default SequenceDetailPage;
