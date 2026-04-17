import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  getStages,
  createStage,
  updateStage,
  deleteStage,
  seedDefaultStages,
  type PipelineStage,
} from '../../lib/api';

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
      <div className="flex items-center justify-between p-3 rounded-xl border bg-card group hover:shadow-sm transition-shadow">
        <div className="flex items-center gap-3">
          <div className="w-3 h-3 rounded-full flex-shrink-0" style={{ backgroundColor: stage.color }} />
          <span className="text-sm font-medium">{stage.name}</span>
          {stage.is_won && (
            <span className="text-[10px] font-bold px-1.5 py-0.5 rounded-full bg-emerald-500/10 text-emerald-500">Won</span>
          )}
          {stage.is_lost && (
            <span className="text-[10px] font-bold px-1.5 py-0.5 rounded-full bg-red-400/10 text-red-400">Lost</span>
          )}
        </div>
        <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
          <button
            onClick={() => setEditing(true)}
            className="p-1.5 rounded-lg hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
            title="Edit"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17 3a2.828 2.828 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/></svg>
          </button>
          <button
            onClick={() => onDelete(stage.id)}
            className="p-1.5 rounded-lg hover:bg-red-500/10 text-muted-foreground hover:text-red-500 transition-colors"
            title="Delete"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="p-3 rounded-xl border bg-accent/30 space-y-3">
      <input
        className="w-full px-3 py-1.5 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        value={name}
        onChange={e => setName(e.target.value)}
        placeholder="Stage name"
        autoFocus
      />
      <div className="flex flex-wrap gap-1.5">
        {COLORS.map(c => (
          <button
            key={c}
            onClick={() => setColor(c)}
            className={`w-6 h-6 rounded-full transition-transform ${color === c ? 'scale-125 ring-2 ring-offset-1 ring-blue-500' : 'hover:scale-110'}`}
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
        <button
          onClick={save}
          className="px-3 py-1.5 rounded-lg bg-blue-600 text-white text-xs font-medium hover:bg-blue-700 transition-colors"
        >
          Save
        </button>
        <button
          onClick={() => { setName(stage.name); setColor(stage.color); setIsWon(stage.is_won); setIsLost(stage.is_lost); setEditing(false); }}
          className="px-3 py-1.5 rounded-lg bg-muted text-xs font-medium hover:bg-accent transition-colors"
        >
          Cancel
        </button>
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
  });

  const deleteMut = useMutation({
    mutationFn: (id: string) => deleteStage(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stages'] }),
  });

  const seedMut = useMutation({
    mutationFn: seedDefaultStages,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['stages'] }),
    onError: (e: Error) => setError(e.message),
  });

  if (isLoading) {
    return <div className="space-y-2">{[...Array(4)].map((_, i) => <div key={i} className="h-12 rounded-xl bg-muted/30 animate-pulse" />)}</div>;
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
            <button
              onClick={() => seedMut.mutate()}
              disabled={seedMut.isPending}
              className="flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm bg-emerald-600 text-white hover:bg-emerald-700 transition-colors disabled:opacity-60"
            >
              {seedMut.isPending ? 'Seeding…' : '✨ Seed Defaults'}
            </button>
          )}
          <button
            onClick={() => setShowAdd(v => !v)}
            className="flex items-center gap-1.5 px-3 py-2 rounded-lg text-sm bg-blue-600 text-white hover:bg-blue-700 transition-colors"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>
            Add Stage
          </button>
        </div>
      </div>

      {error && (
        <div className="p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-red-500 text-sm">{error}</div>
      )}

      {stages.length === 0 && !showAdd && (
        <div className="flex flex-col items-center justify-center border-2 border-dashed rounded-xl border-muted py-12 text-center text-muted-foreground">
          <div className="text-4xl mb-3">🎯</div>
          <p className="font-medium text-foreground">No pipeline stages yet</p>
          <p className="text-sm mt-1">Click "Seed Defaults" to get started quickly, or add your own.</p>
        </div>
      )}

      <div className="space-y-2">
        {stages.map(stage => (
          <StageRow
            key={stage.id}
            stage={stage}
            onSave={(id, data) => updateMut.mutate({ id, data })}
            onDelete={(id) => deleteMut.mutate(id)}
          />
        ))}
      </div>

      {showAdd && (
        <div className="p-4 rounded-xl border bg-accent/20 space-y-3">
          <h4 className="text-sm font-semibold">New Stage</h4>
          <input
            className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
            value={newName}
            onChange={e => setNewName(e.target.value)}
            placeholder="e.g. Discovery Call"
            autoFocus
          />
          <div className="flex flex-wrap gap-1.5">
            {COLORS.map(c => (
              <button
                key={c}
                onClick={() => setNewColor(c)}
                className={`w-6 h-6 rounded-full transition-transform ${newColor === c ? 'scale-125 ring-2 ring-offset-1 ring-blue-500' : 'hover:scale-110'}`}
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
            <button
              disabled={!newName.trim() || createMut.isPending}
              onClick={() => createMut.mutate({ name: newName, color: newColor, is_won: newIsWon, is_lost: newIsLost })}
              className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 transition-colors disabled:opacity-60"
            >
              {createMut.isPending ? 'Creating…' : 'Create Stage'}
            </button>
            <button
              onClick={() => { setShowAdd(false); setError(''); }}
              className="px-4 py-2 rounded-lg bg-muted text-sm font-medium hover:bg-accent transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
