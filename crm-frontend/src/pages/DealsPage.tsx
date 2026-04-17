import { useState, useMemo } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  DndContext,
  DragOverlay,
  PointerSensor,
  useSensor,
  useSensors,
  type DragStartEvent,
  type DragEndEvent,
} from '@dnd-kit/core';
import { getDeals, getStages, changeDealStage, seedDefaultStages, type Deal, type PipelineStage } from '../lib/api';
import KanbanColumn from '../components/deals/KanbanColumn';
import DealCard from '../components/deals/DealCard';
import DealFormModal from '../components/deals/DealFormModal';
import ForecastChart from '../components/deals/ForecastChart';
import { useNavigate } from 'react-router-dom';

export default function DealsPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const [showAddModal, setShowAddModal] = useState(false);
  const [defaultStageId, setDefaultStageId] = useState<string | undefined>();
  const [activeDeal, setActiveDeal] = useState<Deal | null>(null);
  const [showForecast, setShowForecast] = useState(false);

  const { data: stages = [], isLoading: stagesLoading } = useQuery<PipelineStage[]>({
    queryKey: ['stages'],
    queryFn: getStages,
  });

  const { data: dealsData, isLoading: dealsLoading } = useQuery({
    queryKey: ['deals'],
    queryFn: () => getDeals({ limit: 200 }),
  });

  const deals = dealsData?.deals || [];

  const stageMutation = useMutation({
    mutationFn: ({ dealId, stageId }: { dealId: string; stageId: string }) =>
      changeDealStage(dealId, stageId),
    onMutate: async ({ dealId, stageId }) => {
      await queryClient.cancelQueries({ queryKey: ['deals'] });
      const prev = queryClient.getQueryData(['deals']);
      queryClient.setQueryData(['deals'], (old: typeof dealsData) => {
        if (!old) return old;
        return {
          ...old,
          deals: old.deals.map((d: Deal) =>
            d.id === dealId ? { ...d, stage_id: stageId } : d
          ),
        };
      });
      return { prev };
    },
    onError: (_err, _vars, context) => {
      if (context?.prev) queryClient.setQueryData(['deals'], context.prev);
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['deals'] });
    },
  });

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 8 } })
  );

  const dealsByStage = useMemo(() => {
    const map: Record<string, Deal[]> = {};
    stages.forEach(s => { map[s.id] = []; });
    deals.forEach(d => {
      let stageId = d.stage_id;
      // Fall back to the first available stage if stage_id is missing or deleted
      if (!stageId || !map[stageId]) {
        stageId = stages.length > 0 ? stages[0].id : null;
      }
      if (stageId && map[stageId]) {
        map[stageId].push(d);
      }
    });
    return map;
  }, [stages, deals]);

  const totalPipelineValue = deals.reduce((s, d) => s + (d.value || 0), 0);

  const handleDragStart = (event: DragStartEvent) => {
    const deal = deals.find(d => d.id === event.active.id);
    setActiveDeal(deal || null);
  };

  const handleDragEnd = (event: DragEndEvent) => {
    setActiveDeal(null);
    const { active, over } = event;
    if (!over) return;

    const deal = deals.find(d => d.id === active.id);
    if (!deal) return;

    const targetStageId = over.id as string;
    // If dropped on a stage column and it's different from current
    if (targetStageId !== deal.stage_id && stages.some(s => s.id === targetStageId)) {
      stageMutation.mutate({ dealId: deal.id, stageId: targetStageId });
    }
  };

  const handleAddDeal = (stageId: string) => {
    setDefaultStageId(stageId);
    setShowAddModal(true);
  };

  if (stagesLoading || dealsLoading) {
    return (
      <div className="space-y-4">
        <div className="h-10 w-48 rounded-lg bg-muted/50 animate-pulse" />
        <div className="flex gap-4 overflow-x-auto">
          {[...Array(5)].map((_, i) => (
            <div key={i} className="min-w-[280px] h-96 rounded-xl bg-muted/30 animate-pulse" />
          ))}
        </div>
      </div>
    );
  }

  return (
    <div className="h-full flex flex-col">
      {/* Header */}
      <div className="flex items-center justify-between mb-4 shrink-0">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Deals</h1>
          <p className="text-sm text-muted-foreground">
            Pipeline value: <span className="font-semibold text-foreground">${totalPipelineValue.toLocaleString()}</span>
            {' · '}{deals.length} deals
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowForecast(v => !v)}
            className={`px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
              showForecast ? 'bg-blue-600 text-white' : 'bg-muted hover:bg-accent'
            }`}
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="inline mr-1.5 -mt-0.5"><path d="M3 3v18h18"/><path d="m19 9-5 5-4-4-3 3"/></svg>
            Forecast
          </button>
          <button
            onClick={() => { setDefaultStageId(stages[0]?.id); setShowAddModal(true); }}
            className="flex items-center gap-1.5 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 transition-colors"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>
            Add Deal
          </button>
        </div>
      </div>

      {/* Forecast */}
      {showForecast && (
        <div className="mb-4 shrink-0">
          <ForecastChart />
        </div>
      )}

      {/* Kanban Board */}
      <div className="flex-1 overflow-x-auto pb-4">
        {stages.length === 0 ? (
          <div className="flex flex-col items-center justify-center h-full border-2 border-dashed rounded-xl border-muted mr-4">
            <div className="text-center text-muted-foreground p-8">
              <div className="text-5xl mb-4">🎯</div>
              <p className="text-lg font-medium mb-1 text-foreground">No pipeline stages found</p>
              <p className="text-sm mb-5">Create stages in Settings → Pipeline, or seed the defaults now.</p>
              <button
                onClick={() => {
                  seedDefaultStages().then(() => queryClient.invalidateQueries({ queryKey: ['stages'] }));
                }}
                className="px-4 py-2 rounded-lg bg-blue-600 text-white text-sm font-medium hover:bg-blue-700 transition-colors"
              >
                ✨ Seed Default Stages
              </button>
            </div>
          </div>
        ) : (
          <DndContext sensors={sensors} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
            <div className="flex gap-4 h-full">
              {stages.map(stage => (
                <KanbanColumn
                  key={stage.id}
                  stage={stage}
                  deals={dealsByStage[stage.id] || []}
                  onDealClick={(deal) => navigate(`/deals/${deal.id}`)}
                  onAddDeal={handleAddDeal}
                />
              ))}
            </div>

            <DragOverlay>
              {activeDeal ? (
                <div className="opacity-90 rotate-3 scale-105">
                  <DealCard deal={activeDeal} onClick={() => {}} />
                </div>
              ) : null}
            </DragOverlay>
          </DndContext>
        )}
      </div>

      {/* Add Deal Modal */}
      <DealFormModal
        isOpen={showAddModal}
        onClose={() => setShowAddModal(false)}
        stages={stages}
        defaultStageId={defaultStageId}
      />
    </div>
  );
}
