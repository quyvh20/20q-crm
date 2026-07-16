import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { User } from 'lucide-react';
import { Badge } from '@/components/ui';
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

  const probVariant =
    (deal.probability || 0) >= 70 ? 'success' :
    (deal.probability || 0) >= 30 ? 'warning' :
    'destructive';

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
      <p className="font-medium text-sm leading-tight mb-2 group-hover:text-primary transition-colors truncate">
        {deal.title}
      </p>

      {contactName && (
        <p className="text-xs text-muted-foreground truncate mb-1.5">
          <User aria-hidden className="inline mr-1 -mt-0.5 h-3 w-3" />
          {contactName}
        </p>
      )}

      <div className="flex items-center justify-between mt-2">
        <span className="text-sm font-semibold">
          ${(deal.value || 0).toLocaleString()}
        </span>
        <Badge variant={probVariant} className="text-[10px] font-bold">
          {deal.probability || 0}%
        </Badge>
      </div>

      {deal.expected_close_at && (
        <p className="text-[10px] text-muted-foreground mt-1.5">
          Close: {new Date(deal.expected_close_at).toLocaleDateString()}
        </p>
      )}
    </div>
  );
}
