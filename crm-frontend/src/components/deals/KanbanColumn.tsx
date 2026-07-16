import { useDroppable } from '@dnd-kit/core';
import { SortableContext, verticalListSortingStrategy } from '@dnd-kit/sortable';
import { Plus } from 'lucide-react';
import DealCard from './DealCard';
import type { Deal, PipelineStage } from '../../lib/api';

interface KanbanColumnProps {
  stage: PipelineStage;
  deals: Deal[];
  onDealClick: (deal: Deal) => void;
  onAddDeal: (stageId: string) => void;
}

export default function KanbanColumn({ stage, deals, onDealClick, onAddDeal }: KanbanColumnProps) {
  const { setNodeRef, isOver } = useDroppable({ id: stage.id });

  const totalValue = deals.reduce((sum, d) => sum + (d.value || 0), 0);

  return (
    <div
      ref={setNodeRef}
      className={`flex flex-col min-w-[280px] w-[280px] rounded-xl bg-muted/30 border transition-colors ${
        isOver ? 'border-primary/50 bg-primary/5' : ''
      }`}
    >
      {/* Column header */}
      <div className="px-3 py-3 border-b" style={{ borderTopColor: stage.color, borderTopWidth: 3, borderTopLeftRadius: 12, borderTopRightRadius: 12 }}>
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <h3 className="text-sm font-semibold">{stage.name}</h3>
            <span className="text-[10px] bg-muted px-1.5 py-0.5 rounded-full font-medium text-muted-foreground">
              {deals.length}
            </span>
          </div>
          <button
            onClick={() => onAddDeal(stage.id)}
            className="h-6 w-6 rounded-md hover:bg-accent flex items-center justify-center text-muted-foreground hover:text-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            title="Add deal"
          >
            <Plus aria-hidden className="h-3.5 w-3.5" />
          </button>
        </div>
        <p className="text-xs text-muted-foreground mt-1">
          ${totalValue.toLocaleString()}
        </p>
      </div>

      {/* Deal cards */}
      <div className="flex-1 p-2 space-y-2 overflow-y-auto max-h-[calc(100vh-240px)]">
        <SortableContext items={deals.map(d => d.id)} strategy={verticalListSortingStrategy}>
          {deals.map(deal => (
            <DealCard
              key={deal.id}
              deal={deal}
              onClick={() => onDealClick(deal)}
            />
          ))}
        </SortableContext>
        {deals.length === 0 && (
          <div className="text-center py-8 text-muted-foreground/50">
            <p className="text-xs">No deals</p>
          </div>
        )}
      </div>
    </div>
  );
}
