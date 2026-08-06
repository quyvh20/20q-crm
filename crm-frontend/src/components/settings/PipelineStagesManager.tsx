import { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Pencil, Plus, Sparkles, Star, Target, Trash2 } from 'lucide-react';
import { useConfirm } from '../common/ConfirmDialog';
import {
  getStages,
  createStage,
  updateStage,
  deleteStage,
  seedDefaultStages,
  getPipelines,
  createPipeline,
  updatePipeline,
  deletePipeline,
  type PipelineStage,
  type Pipeline,
} from '../../lib/api';
import { Badge, Button, EmptyState, ErrorState, Input, Skeleton } from '@/components/ui';

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

  // R9.3: which board is being edited. Stages are per-pipeline now, so the
  // whole page is scoped to one.
  const [pipelineId, setPipelineId] = useState<string | null>(null);
  const { data: pipelines = [] } = useQuery<Pipeline[]>({
    queryKey: ['pipelines'],
    queryFn: getPipelines,
  });
  // Land on the default board once the list arrives, and recover if the
  // selected board is deleted underneath us.
  useEffect(() => {
    if (pipelines.length === 0) return;
    if (pipelineId && pipelines.some((p) => p.id === pipelineId)) return;
    setPipelineId((pipelines.find((p) => p.is_default) ?? pipelines[0]).id);
  }, [pipelines, pipelineId]);

  // The query key carries the pipeline. Without it every board would share one
  // cache entry and the switcher would show pipeline A's stages under B.
  const stagesKey = ['stages', pipelineId] as const;
  const invalidateStages = () => {
    qc.invalidateQueries({ queryKey: ['stages'] });
    // The board pages key their own stage queries the same way.
    qc.invalidateQueries({ queryKey: ['pipelines'] });
  };

  const { data: stages = [], isLoading, error: loadError, refetch } = useQuery<PipelineStage[]>({
    queryKey: stagesKey,
    queryFn: () => getStages(pipelineId ?? undefined),
    enabled: !!pipelineId,
  });

  const createMut = useMutation({
    mutationFn: (data: Parameters<typeof createStage>[0]) => createStage(data),
    onSuccess: () => {
      invalidateStages();
      setNewName(''); setNewColor(COLORS[0]); setNewIsWon(false); setNewIsLost(false);
      setShowAdd(false); setError('');
    },
    onError: (e: Error) => setError(e.message),
  });

  const updateMut = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<PipelineStage> }) => updateStage(id, data),
    onSuccess: () => invalidateStages(),
    onError: (e: Error) => setError(e.message), // failures used to vanish silently
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteStage(id),
    onSuccess: () => invalidateStages(),
    onError: (e: Error) => setError(e.message),
  });

  const seedMut = useMutation({
    mutationFn: () => seedDefaultStages(pipelineId ?? undefined),
    onSuccess: () => invalidateStages(),
    onError: (e: Error) => setError(e.message),
  });

  const createPipelineMut = useMutation({
    mutationFn: (name: string) => createPipeline({ name, seed_default_stages: true }),
    onSuccess: (p) => {
      setError('');
      setPipelineId(p.id);
      qc.invalidateQueries({ queryKey: ['pipelines'] });
      qc.invalidateQueries({ queryKey: ['stages'] });
    },
    onError: (e: Error) => setError(e.message),
  });

  const setDefaultMut = useMutation({
    mutationFn: (id: string) => updatePipeline(id, { is_default: true }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['pipelines'] }),
    onError: (e: Error) => setError(e.message),
  });

  const deletePipelineMut = useMutation({
    mutationFn: (id: string) => deletePipeline(id),
    onSuccess: () => {
      setPipelineId(null);
      qc.invalidateQueries({ queryKey: ['pipelines'] });
      qc.invalidateQueries({ queryKey: ['stages'] });
    },
    // The backend refuses a board that still has deals, or the default one,
    // with a 409 whose message says which — surface it verbatim.
    onError: (e: Error) => setError(e.message),
  });

  const activePipeline = pipelines.find((p) => p.id === pipelineId) ?? null;

  if (isLoading) {
    return <div className="space-y-2">{[...Array(4)].map((_, i) => <Skeleton key={i} className="h-12 rounded-xl" />)}</div>;
  }

  return (
    <div className="space-y-4 pt-4">
      {/* R9.3 board switcher. Stages belong to a pipeline, so the whole panel
          below is scoped to whichever one is selected here. */}
      <div className="flex flex-wrap items-center gap-2 rounded-xl border border-border bg-accent/20 p-3">
        <label className="text-sm font-medium text-muted-foreground" htmlFor="pipeline-select">Pipeline</label>
        <select
          id="pipeline-select"
          value={pipelineId ?? ''}
          onChange={(e) => setPipelineId(e.target.value)}
          className="rounded-md border border-input bg-background px-2 py-1.5 text-sm"
        >
          {pipelines.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name}{p.is_default ? ' (default)' : ''}
            </option>
          ))}
        </select>
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            const name = window.prompt('Name the new pipeline');
            if (name && name.trim()) createPipelineMut.mutate(name.trim());
          }}
          disabled={createPipelineMut.isPending}
        >
          <Plus aria-hidden /> New pipeline
        </Button>
        {activePipeline && !activePipeline.is_default && (
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDefaultMut.mutate(activePipeline.id)}
              disabled={setDefaultMut.isPending}
            >
              <Star aria-hidden /> Make default
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={async () => {
                if (!(await confirmDialog({
                  title: `Delete "${activePipeline.name}"`,
                  body: 'The pipeline and its stages are removed. Deals must be moved off it first.',
                  confirmLabel: 'Delete pipeline',
                }))) return;
                deletePipelineMut.mutate(activePipeline.id);
              }}
              disabled={deletePipelineMut.isPending}
            >
              <Trash2 aria-hidden /> Delete pipeline
            </Button>
          </>
        )}
      </div>

      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-base font-semibold">Pipeline Stages</h3>
          <p className="text-sm text-muted-foreground mt-0.5">Define the stages deals move through in your sales pipeline.</p>
        </div>
        <div className="flex gap-2">
          {stages.length === 0 && !loadError && !!pipelineId && (
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

      {/* A failed load must not offer to "Seed Defaults" — the stages very
          likely exist, and the empty state's advice is for a fresh workspace. */}
      {loadError ? (
        <ErrorState title="Couldn't load your pipeline stages." error={loadError} onRetry={() => refetch()} />
      ) : stages.length === 0 && !showAdd ? (
        <EmptyState
          icon={Target}
          title="No pipeline stages yet"
          description={'Click "Seed Defaults" to get started quickly, or add your own.'}
        />
      ) : null}

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
              onClick={() => createMut.mutate({ name: newName, color: newColor, is_won: newIsWon, is_lost: newIsLost, pipeline_id: pipelineId ?? undefined })}
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
