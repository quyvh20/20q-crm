import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Repeat } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import Modal from '../../components/common/Modal';
import {
  Badge, Button, EmptyState, PageHeader, Select, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';
import { useSequences, useCreateSequence } from './sequencesQueries';
import { useSegments } from './segmentsQueries';
import { useWorkflowsList } from '../workflows/queries';
import type { Workflow } from '../workflows/types';
import { SEQUENCE_STATUS_LABEL, sequenceStatusVariant } from './sequencesApi';

// A workflow is usable as a drip sequence only if it actually sends marketing mail.
function hasMarketingSend(w: Workflow): boolean {
  return (w.actions ?? []).some((a) => a.type === 'send_email' && (a.params as { channel?: unknown } | undefined)?.channel === 'marketing');
}

// 'schedule' is the only trigger that fires without a per-record context; every other
// type auto-enrolls records on its own, which would double-send to fed contacts.
function triggerAutoEnrolls(w: Workflow): boolean {
  return (w.trigger?.type ?? '') !== 'schedule';
}

const SequencesListPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-5xl"><AccessDeniedPanel capability="marketing.manage" what="sequences" /></div>;
  }
  return <SequencesContent />;
};

const SequencesContent: React.FC = () => {
  const navigate = useNavigate();
  const { data: sequences = [], isLoading } = useSequences();
  const createMutation = useCreateSequence();
  const { data: wfData } = useWorkflowsList({});
  const workflows = wfData?.workflows ?? [];
  const { data: segments = [] } = useSegments();

  const [showCreate, setShowCreate] = useState(false);
  const [workflowId, setWorkflowId] = useState('');
  const [segmentId, setSegmentId] = useState('');
  const [error, setError] = useState('');

  const openCreate = () => { setWorkflowId(''); setSegmentId(''); setError(''); setShowCreate(true); };

  const selectedWf = workflows.find((w) => w.id === workflowId);
  const wfNoMarketing = !!selectedWf && !hasMarketingSend(selectedWf);
  const wfAutoEnrolls = !!selectedWf && !wfNoMarketing && triggerAutoEnrolls(selectedWf);

  const handleCreate = () => {
    if (!workflowId || !segmentId) return;
    setError('');
    createMutation.mutate(
      { sequence_workflow_id: workflowId, segment_id: segmentId },
      {
        onSuccess: (s) => { setShowCreate(false); navigate(`/marketing/sequences/${s.id}`); },
        onError: (e) => setError((e as Error).message || 'Failed to create enrollment'),
      },
    );
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      <PageHeader
        title="Sequences"
        description="Enroll a segment into a drip workflow. Each contact runs the workflow's delay + marketing send steps, with unsubscribe/consent checked live at every send."
        actions={<Button onClick={openCreate}>Enroll a segment</Button>}
      />

      {isLoading ? (
        <SpinnerBlock label="Loading sequences…" />
      ) : sequences.length === 0 ? (
        <EmptyState
          icon={Repeat}
          title="No sequence enrollments yet"
          description="Build a drip workflow (delay + marketing send steps) in Workflows, then enroll a segment into it here."
          action={<Button onClick={openCreate}>Enroll a segment</Button>}
        />
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Sequence</TableHead>
                <TableHead>Segment</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Enrolled</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {sequences.map((s) => (
                <TableRow key={s.id} className="cursor-pointer" onClick={() => navigate(`/marketing/sequences/${s.id}`)}>
                  <TableCell className="font-medium text-foreground">{s.sequence_name || '—'}</TableCell>
                  <TableCell className="text-muted-foreground">{s.segment_name || '—'}</TableCell>
                  <TableCell><Badge variant={sequenceStatusVariant(s.status)}>{SEQUENCE_STATUS_LABEL[s.status]}</Badge></TableCell>
                  <TableCell className="text-right tabular-nums text-foreground">{s.enrolled_count.toLocaleString()}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableShell>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="Enroll a segment" size="md">
        <div className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="seq-wf">Sequence (workflow)</label>
            <Select id="seq-wf" value={workflowId} onChange={(e) => setWorkflowId(e.target.value)}>
              <option value="">Select a workflow…</option>
              {workflows.map((w) => <option key={w.id} value={w.id}>{w.name}{w.is_active ? '' : ' (inactive)'}</option>)}
            </Select>
            {wfNoMarketing ? (
              <p className="mt-1 text-xs text-destructive">
                This workflow has no marketing send step. Add a send step with Channel = Marketing before enrolling.
              </p>
            ) : wfAutoEnrolls ? (
              <p className="mt-1 text-xs text-amber-600 dark:text-amber-400">
                This workflow’s trigger also auto-enrolls records on its own, so matching contacts may receive the sequence twice. For a segment-only drip, use a Schedule trigger.
              </p>
            ) : (
              <p className="mt-1 text-xs text-muted-foreground">
                Build the drip in{' '}
                <a className="text-primary hover:underline" href="/workflows" target="_blank" rel="noreferrer">Workflows</a>
                {' '}— delay + marketing send steps.
              </p>
            )}
          </div>
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="seq-seg">Audience segment</label>
            <Select id="seq-seg" value={segmentId} onChange={(e) => setSegmentId(e.target.value)}>
              <option value="">Select a segment…</option>
              {segments.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
            </Select>
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending || !workflowId || !segmentId || wfNoMarketing}>Enroll</Button>
          </div>
        </div>
      </Modal>
    </div>
  );
};

export default SequencesListPage;
