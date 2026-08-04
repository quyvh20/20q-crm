import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Megaphone, Plus, Trash2 } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import Modal from '../../components/common/Modal';
import { useConfirm } from '../../components/common/ConfirmDialog';
import {
  Badge, Button, EmptyState, ErrorState, Input, PageHeader, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';
import { useCampaigns, useCreateCampaign, useDeleteCampaign } from './campaignsQueries';
import type { Campaign, CampaignStatus } from './campaignsApi';

export function campaignStatusVariant(s: CampaignStatus): 'default' | 'secondary' | 'outline' | 'destructive' | 'success' | 'warning' {
  switch (s) {
    case 'sending':
    case 'snapshotting':
      return 'default';
    case 'sent':
      return 'success';
    case 'paused':
      return 'warning';
    case 'canceled':
      return 'destructive';
    case 'scheduled':
      return 'outline';
    default:
      return 'secondary';
  }
}

const CampaignsListPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-5xl"><AccessDeniedPanel capability="marketing.manage" what="campaigns" /></div>;
  }
  return <CampaignsContent />;
};

const CampaignsContent: React.FC = () => {
  const navigate = useNavigate();
  const { data: campaigns, isLoading, error: loadError, refetch } = useCampaigns();
  const createMutation = useCreateCampaign();
  const deleteMutation = useDeleteCampaign();
  const { confirm, dialog } = useConfirm();

  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [error, setError] = useState('');

  const rows = campaigns ?? [];

  const handleCreate = () => {
    const n = name.trim();
    if (!n) return;
    setError('');
    createMutation.mutate(
      { name: n, segment_ids: [], exclude_segment_ids: [] },
      {
        onSuccess: (c) => { setShowCreate(false); setName(''); navigate(`/marketing/campaigns/${c.id}`); },
        onError: (e) => setError((e as Error).message || 'Failed to create campaign'),
      },
    );
  };

  const handleDelete = async (c: Campaign) => {
    const ok = await confirm({
      title: 'Delete campaign',
      body: `Delete “${c.name}”? This can’t be undone.`,
      confirmLabel: 'Delete',
      tone: 'danger',
    });
    if (!ok) return;
    deleteMutation.mutate(c.id);
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {dialog}
      <PageHeader
        title="Campaigns"
        description="Bulk email sends to an audience. Build the recipient list from your audiences, pick email content, and launch — every send is guarded by suppression, consent, and a verified sending domain."
        actions={<Button onClick={() => { setError(''); setShowCreate(true); }}><Plus aria-hidden /> New campaign</Button>}
      />

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : loadError ? (
        <ErrorState title="Couldn't load your campaigns." error={loadError} onRetry={() => refetch()} />
      ) : rows.length === 0 ? (
        <EmptyState icon={Megaphone} title="No campaigns yet" description="Create a campaign to send to one of your audiences." />
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Name</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((c) => (
                <TableRow key={c.id} className="cursor-pointer" onClick={() => navigate(`/marketing/campaigns/${c.id}`)}>
                  <TableCell className="font-medium text-foreground">{c.name}</TableCell>
                  <TableCell><Badge variant={campaignStatusVariant(c.status)}>{c.status}</Badge></TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">{new Date(c.updated_at).toLocaleDateString()}</TableCell>
                  <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                    {(c.status === 'draft' || c.status === 'scheduled' || c.status === 'canceled' || c.status === 'sent') && (
                      <Button variant="ghost" size="sm" onClick={() => handleDelete(c)} disabled={deleteMutation.isPending}
                        title="Delete campaign" className="text-destructive hover:text-destructive">
                        <Trash2 aria-hidden className="h-4 w-4" />
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </TableShell>
      )}

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="New campaign" size="md">
        <div className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="camp-name">Name</label>
            <Input id="camp-name" value={name} onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') handleCreate(); }} placeholder="e.g. October product update" />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending || !name.trim()}>Create</Button>
          </div>
        </div>
      </Modal>
    </div>
  );
};

export default CampaignsListPage;
