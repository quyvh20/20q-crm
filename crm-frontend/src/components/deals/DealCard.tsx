import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import type { Deal } from '../../lib/api';

interface DealCardProps {
  deal: Deal;
  onClick: () => void;
}

export default function DealCard({ deal, onClick }: DealCardProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: deal.id, data: { deal } });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  };

  const probColor =
    (deal.probability || 0) >= 70 ? 'text-emerald-500 bg-emerald-500/10' :
    (deal.probability || 0) >= 30 ? 'text-amber-500 bg-amber-500/10' :
    'text-red-400 bg-red-400/10';

  const contactName = deal.contact
    ? `${deal.contact.first_name} ${deal.contact.last_name}`
    : null;

  return (
    <div
      ref={setNodeRef}
      style={style}
      {...attributes}
      {...listeners}
      onClick={onClick}
      className="bg-card border rounded-xl p-3.5 cursor-grab active:cursor-grabbing hover:shadow-md transition-shadow group"
    >
      <p className="font-medium text-sm leading-tight mb-2 group-hover:text-blue-500 transition-colors truncate">
        {deal.title}
      </p>

      {contactName && (
        <p className="text-xs text-muted-foreground truncate mb-1.5">
          <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="inline mr-1 -mt-0.5"><path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>
          {contactName}
        </p>
      )}

      <div className="flex items-center justify-between mt-2">
        <span className="text-sm font-semibold">
          ${(deal.value || 0).toLocaleString()}
        </span>
        <span className={`text-[10px] font-bold px-1.5 py-0.5 rounded-full ${probColor}`}>
          {deal.probability || 0}%
        </span>
      </div>

      {deal.expected_close_at && (
        <p className="text-[10px] text-muted-foreground mt-1.5">
          Close: {new Date(deal.expected_close_at).toLocaleDateString()}
        </p>
      )}
    </div>
  );
}
