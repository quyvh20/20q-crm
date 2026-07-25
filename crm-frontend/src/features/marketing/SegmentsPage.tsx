import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Users, Plus, Trash2, Filter, ListChecks } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import Modal from '../../components/common/Modal';
import { useConfirm } from '../../components/common/ConfirmDialog';
import {
  Badge, Button, EmptyState, Input, PageHeader, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';
import { useSegments, useCreateSegment, useDeleteSegment, useSegmentCount } from './segmentsQueries';
import type { Segment, SegmentType } from './segmentsApi';

/** M5 Audiences — saved segments (dynamic predicate) + static lists. Every
 *  /api/marketing/* route requires marketing.manage, so the whole page is gated;
 *  wait for the capability fetch to settle so a deep-linked marketer doesn't flash
 *  the denied panel. */
const SegmentsPage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) {
    return <div className="mx-auto w-full max-w-5xl"><SpinnerBlock label="Loading…" /></div>;
  }
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-5xl"><AccessDeniedPanel capability="marketing.manage" what="audiences" /></div>;
  }
  return <SegmentsContent />;
};

const SegmentsContent: React.FC = () => {
  const navigate = useNavigate();
  const { data: segments, isLoading } = useSegments();
  const createMutation = useCreateSegment();
  const deleteMutation = useDeleteSegment();
  const { confirm, dialog } = useConfirm();

  const [showCreate, setShowCreate] = useState(false);
  const [newName, setNewName] = useState('');
  const [newType, setNewType] = useState<SegmentType>('dynamic');
  const [error, setError] = useState('');

  const rows = segments ?? [];

  const handleCreate = () => {
    const name = newName.trim();
    if (!name) return;
    setError('');
    createMutation.mutate(
      { name, type: newType, definition: newType === 'dynamic' ? {} : undefined },
      {
        onSuccess: (seg) => {
          setShowCreate(false);
          setNewName('');
          navigate(`/marketing/audiences/${seg.id}`);
        },
        onError: (e) => setError((e as Error).message || 'Failed to create audience'),
      },
    );
  };

  const handleDelete = async (seg: Segment) => {
    const ok = await confirm({
      title: 'Delete audience',
      body: `Delete “${seg.name}”? Any campaign still targeting it will need a new audience. This can’t be undone.`,
      confirmLabel: 'Delete',
      tone: 'danger',
    });
    if (!ok) return;
    deleteMutation.mutate(seg.id);
  };

  return (
    <div className="mx-auto w-full max-w-5xl">
      {dialog}
      <PageHeader
        title="Audiences"
        description="Reusable groups of contacts a campaign can send to — a dynamic segment (a saved filter that always reflects who matches right now) or a static list (an explicit set of contacts you manage by hand)."
        actions={<Button onClick={() => { setError(''); setShowCreate(true); }}><Plus aria-hidden /> New audience</Button>}
      />

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : rows.length === 0 ? (
        <EmptyState
          icon={Users}
          title="No audiences yet"
          description="Create a dynamic segment to target everyone matching a filter, or a static list you curate by hand."
        />
      ) : (
        <TableShell>
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Name</TableHead>
                <TableHead>Type</TableHead>
                <TableHead>Contacts</TableHead>
                <TableHead>Updated</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => (
                <TableRow
                  key={s.id}
                  className="cursor-pointer"
                  onClick={() => navigate(`/marketing/audiences/${s.id}`)}
                >
                  <TableCell className="font-medium text-foreground">{s.name}</TableCell>
                  <TableCell>
                    <Badge variant={s.type === 'dynamic' ? 'secondary' : 'outline'} className="gap-1">
                      {s.type === 'dynamic' ? <Filter aria-hidden className="h-3 w-3" /> : <ListChecks aria-hidden className="h-3 w-3" />}
                      {s.type === 'dynamic' ? 'Dynamic' : 'Static list'}
                    </Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground"><CountBadge id={s.id} /></TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {new Date(s.updated_at).toLocaleDateString()}
                  </TableCell>
                  <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                    <Button
                      variant="ghost" size="sm"
                      onClick={() => handleDelete(s)}
                      disabled={deleteMutation.isPending}
                      title="Delete audience"
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

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="New audience" size="md">
        <div className="space-y-4">
          <div>
            <label className="mb-1 block text-xs font-medium text-muted-foreground" htmlFor="seg-name">Name</label>
            <Input
              id="seg-name"
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') handleCreate(); }}
              placeholder="e.g. Active trial users"
              autoFocus={false}
            />
          </div>
          <fieldset className="space-y-2">
            <legend className="mb-1 block text-xs font-medium text-muted-foreground">Type</legend>
            <TypeOption
              selected={newType === 'dynamic'} onSelect={() => setNewType('dynamic')}
              icon={<Filter aria-hidden className="h-4 w-4" />}
              title="Dynamic segment"
              desc="A saved filter. Membership always reflects who matches right now."
            />
            <TypeOption
              selected={newType === 'static'} onSelect={() => setNewType('static')}
              icon={<ListChecks aria-hidden className="h-4 w-4" />}
              title="Static list"
              desc="An explicit set of contacts you add and remove by hand."
            />
          </fieldset>
          {error && <p className="text-sm text-destructive">{error}</p>}
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setShowCreate(false)}>Cancel</Button>
            <Button onClick={handleCreate} disabled={createMutation.isPending || !newName.trim()}>Create</Button>
          </div>
        </div>
      </Modal>
    </div>
  );
};

function TypeOption({ selected, onSelect, icon, title, desc }: {
  selected: boolean; onSelect: () => void; icon: React.ReactNode; title: string; desc: string;
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      className={`flex w-full items-start gap-3 rounded-lg border p-3 text-left transition ${
        selected ? 'border-primary bg-primary/5' : 'border-border hover:bg-accent'
      }`}
    >
      <span className="mt-0.5 text-muted-foreground">{icon}</span>
      <span>
        <span className="block text-sm font-medium text-foreground">{title}</span>
        <span className="block text-xs text-muted-foreground">{desc}</span>
      </span>
    </button>
  );
}

/** Live member count for a row (short-cached; a 403/blip renders as “—”). */
function CountBadge({ id }: { id: string }) {
  const { data, isLoading, isError } = useSegmentCount(id);
  if (isLoading) return <span className="text-xs text-muted-foreground">…</span>;
  if (isError || data === undefined) return <span className="text-xs text-muted-foreground">—</span>;
  return <span className="tabular-nums">{data.toLocaleString()}</span>;
}

export default SegmentsPage;
