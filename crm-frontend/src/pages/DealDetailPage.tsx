import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getDeal, deleteDeal, getActivities, getStages, changeDealStage, updateDeal, type Deal, type Activity, type PipelineStage } from '../lib/api';
import ActivityForm from '../components/deals/ActivityForm';
import { useState } from 'react';

const ACTIVITY_ICONS: Record<string, string> = {
  call: '📞',
  email: '✉️',
  meeting: '🤝',
  note: '📝',
  stage_change: '🔀',
  won: '🏆',
  lost: '💔',
};

// ── Edit Deal Modal ─────────────────────────────────────────────
function EditDealModal({ deal, onClose }: { deal: Deal; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(deal.title);
  const [value, setValue] = useState(String(deal.value));
  const [probability, setProbability] = useState(deal.probability);
  const [closeAt, setCloseAt] = useState(
    deal.expected_close_at ? deal.expected_close_at.slice(0, 10) : ''
  );

  const mutation = useMutation({
    mutationFn: () =>
      updateDeal(deal.id, {
        title,
        value: parseFloat(value) || 0,
        probability,
        expected_close_at: closeAt ? new Date(closeAt).toISOString() : undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deal', deal.id] });
      queryClient.invalidateQueries({ queryKey: ['deals'] });
      onClose();
    },
  });

  const probColor = probability >= 70 ? '#10b981' : probability >= 30 ? '#f59e0b' : '#ef4444';

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="bg-card w-full max-w-md rounded-2xl shadow-xl overflow-hidden animate-in zoom-in-95 duration-200">
        <div className="p-6 space-y-5">
          <h3 className="text-lg font-semibold">Edit Deal</h3>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-muted-foreground">Title</label>
            <input
              id="edit-deal-title"
              className="w-full rounded-lg border bg-muted/30 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              value={title}
              onChange={e => setTitle(e.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-muted-foreground">Value ($)</label>
            <input
              id="edit-deal-value"
              type="number" min={0}
              className="w-full rounded-lg border bg-muted/30 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              value={value}
              onChange={e => setValue(e.target.value)}
            />
          </div>

          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-muted-foreground">Probability</label>
              <span className="text-sm font-bold" style={{ color: probColor }}>{probability}%</span>
            </div>
            <input
              id="edit-deal-probability"
              type="range" min={0} max={100}
              value={probability}
              onChange={e => setProbability(Number(e.target.value))}
              className="w-full accent-blue-500"
            />
            <div className="flex justify-between text-[10px] text-muted-foreground">
              <span>0%</span><span>50%</span><span>100%</span>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-xs font-medium text-muted-foreground">Expected Close Date</label>
            <input
              id="edit-deal-close-date"
              type="date"
              className="w-full rounded-lg border bg-muted/30 px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              value={closeAt}
              onChange={e => setCloseAt(e.target.value)}
            />
          </div>

          {deal.contact && (
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-muted-foreground">Contact</label>
              <div className="rounded-lg border bg-muted/20 px-3 py-2 text-sm text-muted-foreground">
                {deal.contact.first_name} {deal.contact.last_name}
                {deal.contact.email && ` · ${deal.contact.email}`}
              </div>
            </div>
          )}

          <div className="rounded-lg bg-blue-500/10 px-4 py-2.5 flex items-center justify-between">
            <span className="text-xs text-muted-foreground">Expected Revenue preview</span>
            <span className="text-sm font-bold text-blue-400">
              ${Math.round((parseFloat(value) || 0) * probability / 100).toLocaleString()}
            </span>
          </div>
        </div>

        <div className="px-6 py-4 bg-muted/30 flex justify-end gap-3 border-t">
          <button onClick={onClose} className="px-4 py-2 text-sm font-medium rounded-lg hover:bg-muted transition-colors">Cancel</button>
          <button
            id="edit-deal-save"
            onClick={() => mutation.mutate()}
            disabled={mutation.isPending || !title.trim()}
            className="px-4 py-2 text-sm font-medium rounded-lg bg-blue-600 text-white hover:bg-blue-700 transition-colors disabled:opacity-50"
          >
            {mutation.isPending ? 'Saving…' : 'Save Changes'}
          </button>
        </div>
      </div>
    </div>
  );
}

export default function DealDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [showDelete, setShowDelete] = useState(false);
  const [showEdit, setShowEdit] = useState(false);

  const { data: deal, isLoading } = useQuery<Deal>({
    queryKey: ['deal', id],
    queryFn: () => getDeal(id!),
    enabled: !!id,
  });

  const { data: activities = [] } = useQuery<Activity[]>({
    queryKey: ['activities', id],
    queryFn: () => getActivities({ deal_id: id }),
    enabled: !!id,
  });

  const { data: stages = [] } = useQuery<PipelineStage[]>({
    queryKey: ['stages'],
    queryFn: getStages,
  });

  const stageChangeMutation = useMutation({
    mutationFn: ({ stageId }: { stageId: string }) => changeDealStage(id!, stageId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deal', id] });
      queryClient.invalidateQueries({ queryKey: ['activities', id] });
      queryClient.invalidateQueries({ queryKey: ['deals'] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteDeal(id!),
    onSuccess: () => navigate('/deals'),
  });

  const wonStage = stages.find(s => s.is_won);
  const lostStage = stages.find(s => s.is_lost);

  if (isLoading || !deal) {
    return (
      <div className="max-w-6xl mx-auto space-y-4">
        <div className="h-8 w-48 rounded-lg bg-muted/50 animate-pulse" />
        <div className="h-96 rounded-xl bg-muted/30 animate-pulse" />
      </div>
    );
  }

  return (
    <div className="max-w-6xl mx-auto">
      {/* Back button */}
      <button
        onClick={() => navigate('/deals')}
        className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors mb-4"
      >
        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="m12 19-7-7 7-7"/><path d="M19 12H5"/></svg>
        Back to deals
      </button>

      <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
        {/* Left Panel: Deal Info (3 cols) */}
        <div className="lg:col-span-3 space-y-4">
          {/* Title + Status */}
          <div className="rounded-xl border bg-card p-6">
            <div className="flex items-start justify-between mb-4">
              <div>
                <div className="flex items-center gap-4">
                  <h1 className="text-2xl font-bold">{deal.title}</h1>
                  <button
                    id="edit-deal-btn"
                    onClick={() => setShowEdit(true)}
                    className="flex items-center gap-1.5 px-3 py-1 rounded-lg bg-blue-600/10 text-blue-600 text-xs font-semibold hover:bg-blue-600 hover:text-white transition-all border border-blue-600/20"
                    title="Edit deal"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/><path d="m15 5 4 4"/></svg>
                    Edit
                  </button>
                </div>
                {deal.contact && (
                  <p className="text-sm text-muted-foreground mt-1">
                    Contact: {deal.contact.first_name} {deal.contact.last_name}
                    {deal.contact.email && ` · ${deal.contact.email}`}
                  </p>
                )}
                {deal.company && (
                  <p className="text-sm text-muted-foreground">
                    Company: {deal.company.name}
                  </p>
                )}
              </div>
              <div className="flex items-center gap-2">
                {deal.is_won && (
                  <span className="px-3 py-1 rounded-full bg-emerald-500/10 text-emerald-500 text-xs font-bold">WON</span>
                )}
                {deal.is_lost && (
                  <span className="px-3 py-1 rounded-full bg-red-500/10 text-red-500 text-xs font-bold">LOST</span>
                )}
                {!deal.is_won && !deal.is_lost && deal.stage && (
                  <span
                    className="px-3 py-1 rounded-full text-xs font-bold text-white"
                    style={{ backgroundColor: deal.stage.color }}
                  >
                    {deal.stage.name}
                  </span>
                )}
              </div>
            </div>

            {/* Value + Probability */}
            <div className="grid grid-cols-3 gap-4 mb-6">
              <div className="rounded-lg bg-muted/30 p-3">
                <p className="text-xs text-muted-foreground mb-1">Value</p>
                <p className="text-xl font-bold">${deal.value.toLocaleString()}</p>
              </div>
              <div className="rounded-lg bg-muted/30 p-3">
                <p className="text-xs text-muted-foreground mb-1">Probability</p>
                <p className="text-xl font-bold">{deal.probability}%</p>
              </div>
              <div className="rounded-lg bg-muted/30 p-3">
                <p className="text-xs text-muted-foreground mb-1">Expected Revenue</p>
                <p className="text-xl font-bold">${Math.round(deal.value * deal.probability / 100).toLocaleString()}</p>
              </div>
            </div>

            {deal.expected_close_at && (
              <p className="text-sm text-muted-foreground mb-4">
                Expected close: {new Date(deal.expected_close_at).toLocaleDateString()}
              </p>
            )}

            {/* Stage Selector */}
            {!deal.is_won && !deal.is_lost && (
              <div className="mb-4">
                <label className="text-xs font-medium text-muted-foreground mb-2 block">Move to stage</label>
                <div className="flex flex-wrap gap-1.5">
                  {stages.filter(s => !s.is_won && !s.is_lost).map(s => (
                    <button
                      key={s.id}
                      onClick={() => stageChangeMutation.mutate({ stageId: s.id })}
                      disabled={deal.stage_id === s.id || stageChangeMutation.isPending}
                      className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                        deal.stage_id === s.id
                          ? 'bg-blue-600 text-white'
                          : 'bg-muted/50 hover:bg-muted text-muted-foreground hover:text-foreground'
                      } disabled:opacity-50`}
                    >
                      {s.name}
                    </button>
                  ))}
                </div>
              </div>
            )}

            {/* Won / Lost / Delete Actions */}
            {!deal.is_won && !deal.is_lost && (
              <div className="flex gap-2 pt-4 border-t">
                {wonStage && (
                  <button
                    onClick={() => stageChangeMutation.mutate({ stageId: wonStage.id })}
                    disabled={stageChangeMutation.isPending}
                    className="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-emerald-600 text-white text-sm font-medium hover:bg-emerald-700 transition-colors disabled:opacity-50"
                  >
                    🏆 Mark Won
                  </button>
                )}
                {lostStage && (
                  <button
                    onClick={() => stageChangeMutation.mutate({ stageId: lostStage.id })}
                    disabled={stageChangeMutation.isPending}
                    className="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-red-600 text-white text-sm font-medium hover:bg-red-700 transition-colors disabled:opacity-50"
                  >
                    💔 Mark Lost
                  </button>
                )}
                <div className="flex-1" />
                <button
                  onClick={() => setShowDelete(true)}
                  className="px-3 py-2 rounded-lg text-sm text-red-500 hover:bg-red-500/10 transition-colors"
                >
                  Delete
                </button>
              </div>
            )}
          </div>
        </div>

        {/* Right Panel: Activity Timeline (2 cols) */}
        <div className="lg:col-span-2 space-y-4">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground">Activity Timeline</h2>

          <ActivityForm dealId={id} />

          <div className="space-y-3">
            {activities.length === 0 && (
              <p className="text-sm text-muted-foreground text-center py-8">No activities yet</p>
            )}
            {activities.map(a => (
              <div key={a.id} className="flex gap-3 items-start">
                <div className="shrink-0 h-8 w-8 rounded-full bg-muted/50 flex items-center justify-center text-sm">
                  {ACTIVITY_ICONS[a.type] || '📋'}
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium">{a.title || a.type}</p>
                  {a.body && (
                    <p className="text-xs text-muted-foreground mt-0.5 line-clamp-2">{a.body}</p>
                  )}
                  <div className="flex items-center gap-2 mt-1">
                    <span className="text-[10px] text-muted-foreground">
                      {new Date(a.occurred_at).toLocaleDateString()} {new Date(a.occurred_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                    </span>
                    {a.duration_minutes && (
                      <span className="text-[10px] text-muted-foreground">· {a.duration_minutes}min</span>
                    )}
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted/50 text-muted-foreground capitalize">{a.type}</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {/* Edit modal */}
      {showEdit && deal && <EditDealModal deal={deal} onClose={() => setShowEdit(false)} />}

      {/* Delete confirmation modal */}
      {showDelete && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
          <div className="bg-card w-full max-w-md rounded-2xl shadow-xl overflow-hidden animate-in zoom-in-95 duration-200">
            <div className="p-6">
              <h3 className="text-lg font-semibold mb-2">Delete Deal</h3>
              <p className="text-muted-foreground text-sm">
                Are you sure you want to delete "{deal.title}"? This cannot be undone.
              </p>
            </div>
            <div className="px-6 py-4 bg-muted/30 flex justify-end gap-3 border-t">
              <button
                onClick={() => setShowDelete(false)}
                className="px-4 py-2 text-sm font-medium rounded-lg hover:bg-muted transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={() => deleteMutation.mutate()}
                disabled={deleteMutation.isPending}
                className="px-4 py-2 text-sm font-medium rounded-lg bg-red-600 text-white hover:bg-red-700 transition-colors disabled:opacity-50"
              >
                Delete
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
