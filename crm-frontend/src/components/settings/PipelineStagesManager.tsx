import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Sparkles, Target, Trash2 } from 'lucide-react';
import { useConfirm } from '../common/ConfirmDialog';
import {
  getStages,
  createStage,
  updateStage,
  deleteStage,
  seedDefaultStages,
  type PipelineStage,
} from '../../lib/api';
import { Badge, Button, EmptyState, Input, Skeleton } from '@/components/ui';

const COLORS = [
  '#6366F1', '#3B82F6', '#06B6D4', '#10B981',
  '#F59E0B', '#EF4444', '#EC4899', '#8B5CF6',
  '#14B8A6', '#F97316', '#64748B', '#22C55E',
];

function StageRow({ stage, onSave, onDelete }: {
  stage: PipelineStage;
  onSave: (id: string, data: Partial<PipelineStage>) => void;
  onDelete: (id: string) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(stage.name);
  const [color, setColor] = useState(stage.color);
  const [isWon, setIsWon] = useState(stage.is_won);
  const [isLost, setIsLost] = useState(stage.is_lost);

  const save = () => {
    onSave(stage.id, { name, color, is_won: isWon, is_lost: isLost });
    setEditing(false);
  };

  if (!editing) {
    return (
      <div className="flex items-center justify-between p-3 rounded-xl border border-border bg-card group hover:shadow-sm transition-shadow">
        <div className="flex items-center gap-3">
          {/* Dynamic user-chosen stage color. */}
          <div className="w-3 h-3 rounded-full flex-shrink-0" style={{ backgroundColor: stage.color }} />
          <span className="text-sm font-medium">{stage.name}</span>
          {stage.is_won && <Badge variant="success">Won</Badge>}
          {stage.is_lost && <Badge variant="destructive">Lost</Badge>}
        </div>
        {/* focus-within: hover-only reveal hid these from keyboard users (U7 a11y). */}
        <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 focus-within:opacity-100 transition-opacity">
          <Button variant="ghost" size="icon" onClick={() => setEditing(true)} title="Edit" className="h-8 w-8 text-muted-foreground hover:text-foreground">
            <Pencil className="h-3.5 w-3.5" />
          </Button>
          <Button variant="ghost" size="icon" onClick={() => onDelete(stage.id)} title="Delete" className="h-8 w-8 text-muted-foreground hover:text-destructive">
            <Trash2 className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="p-3 rounded-xl border border-border bg-accent/30 space-y-3">
      <Input
        value={name}
        onChange={e => setName(e.target.value)}
        placeholder="Stage name"
        autoFocus
      />
      <div className="flex flex-wrap gap-1.5">
        {COLORS.map(c => (
          <button
            key={c}
            type="button"
            onClick={() => setColor(c)}
            aria-label={`Use color ${c}`}
            className={`w-6 h-6 rounded-full transition-transform focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 ${color === c ? 'scale-125 ring-2 ring-offset-1 ring-ring' : 'hover:scale-110'}`}
            style={{ backgroundColor: c }}
          />
        ))}
      </div>
      <div className="flex items-center gap-4 text-sm">
        <label className="flex items-center gap-2 cursor-pointer">
          <input type="checkbox" checked={isWon} onChange={e => setIsWon(e.target.checked)} className="rounded" />
          <span>Mark as Won</span>
        </label>
        <label className="flex items-center gap-2 cursor-pointer">
          <input type="checkbox" checked={isLost} onChange={e => setIsLost(e.target.checked)} className="rounded" />
          <span>Mark as Lost</span>
        </label>
      </div>
      <div className="flex gap-2">
        <Button size="sm" onClick={save}>Save</Button>
        <Button
          size="sm"
          variant="secondary"
          onClick={() => { setName(stage.name); setColor(stage.color); setIsWon(stage.is_won); setIsLost(stage.is_lost); setEditing(false); }}
        >
          Cancel
        </Button>
      </div>
    </div>
  );
}

export default function PipelineStagesManager() {
  const qc = useQueryClient();
  const [showAdd, setShowAdd] = useState(false);
  const [newName, setNewName] = useState('');
  const [newColor, setNewColor] = useState(COLORS[0]);
  const [newIsWon, setNewIsWon] = useState(false);
  const [newIsLost, setNewIsLost] = useState(false);
  const [error, setError] = useState('');
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

  const { data: stages = [], isLoading } = useQuery<PipelineStage[]>({
    queryKey: ['stages'],
    queryFn: getStages,
  });

  const createMut = useMutation({
    mutationFn: (data: Parameters<typeof createStage>[0]) => createStage(data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['stages'] });
      setNewName(''); setNewColor(COLORS[0]); setNewIsWon(false); setNewIsLost(false);
      setShowAdd(false); setError('');
    },
    onError: (e: Error) => setError(e.message),
  });

  const updateMut = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<PipelineStage> }) => updateStage(id, data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stages'] }),
    onError: (e: Error) => setError(e.message), // failures used to vanish silently
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteStage(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stages'] }),
    onError: (e: Error) => setError(e.message),
  });

  const seedMut = useMutation({
    mutationFn: seedDefaultStages,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stages'] }),
    onError: (e: Error) => setError(e.message),
  });

  if (isLoading) {
    return <div className="space-y-2">{[...Array(4)].map((_, i) => <Skeleton key={i} className="h-12 rounded-xl" />)}</div>;
  }

  return (
    <div className="space-y-4 pt-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-base font-semibold">Pipeline Stages</h3>
          <p className="text-sm text-muted-foreground mt-0.5">Define the stages deals move through in your sales pipeline.</p>
        </div>
        <div className="flex gap-2">
          {stages.length === 0 && (
            <Button variant="outline" onClick={() => seedMut.mutate()} disabled={seedMut.isPending}>
              <Sparkles aria-hidden /> {seedMut.isPending ? 'Seeding…' : 'Seed Defaults'}
            </Button>
          )}
          <Button onClick={() => setShowAdd(v => !v)}>
            <Plus aria-hidden /> Add Stage
          </Button>
        </div>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">{error}</div>
      )}

      {stages.length === 0 && !showAdd && (
        <EmptyState
          icon={Target}
          title="No pipeline stages yet"
          description={'Click "Seed Defaults" to get started quickly, or add your own.'}
        />
      )}

      <div className="space-y-2">
        {stages.map(stage => (
          <StageRow
            key={stage.id}
            stage={stage}
            onSave={(id, data) => updateMut.mutate({ id, data })}
            onDelete={async (id) => {
              const stg = stages.find(s => s.id === id);
              if (!(await confirmDialog({
                title: `Delete "${stg?.name ?? 'this stage'}"`,
                body: 'Deals currently in this stage keep their data but lose their stage until you move them. This cannot be undone.',
                confirmLabel: 'Delete stage',
              }))) return;
              deleteMut.mutate(id);
            }}
          />
        ))}
      </div>

      {showAdd && (
        <div className="p-4 rounded-xl border border-border bg-accent/20 space-y-3">
          <h4 className="text-sm font-semibold">New Stage</h4>
          <Input
            value={newName}
            onChange={e => setNewName(e.target.value)}
            placeholder="e.g. Discovery Call"
            autoFocus
          />
          <div className="flex flex-wrap gap-1.5">
            {COLORS.map(c => (
              <button
                key={c}
                type="button"
                onClick={() => setNewColor(c)}
                aria-label={`Use color ${c}`}
                className={`w-6 h-6 rounded-full transition-transform focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 ${newColor === c ? 'scale-125 ring-2 ring-offset-1 ring-ring' : 'hover:scale-110'}`}
                style={{ backgroundColor: c }}
              />
            ))}
          </div>
          <div className="flex items-center gap-4 text-sm">
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" checked={newIsWon} onChange={e => setNewIsWon(e.target.checked)} className="rounded" />
              <span>Mark as Won</span>
            </label>
            <label className="flex items-center gap-2 cursor-pointer">
              <input type="checkbox" checked={newIsLost} onChange={e => setNewIsLost(e.target.checked)} className="rounded" />
              <span>Mark as Lost</span>
            </label>
          </div>
          <div className="flex gap-2">
            <Button
              disabled={!newName.trim() || createMut.isPending}
              onClick={() => createMut.mutate({ name: newName, color: newColor, is_won: newIsWon, is_lost: newIsLost })}
            >
              {createMut.isPending ? 'Creating…' : 'Create Stage'}
            </Button>
            <Button variant="secondary" onClick={() => { setShowAdd(false); setError(''); }}>
              Cancel
            </Button>
          </div>
        </div>
      )}
      {confirmDialogEl}
    </div>
  );
}
