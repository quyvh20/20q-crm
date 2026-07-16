import { FileText, Pencil, SquareCheck, TriangleAlert, UserPlus, type LucideIcon } from 'lucide-react';
import type { ConfirmPayload } from './chatTypes';
import { Button } from '../ui/button';

interface Props {
  payload: ConfirmPayload;
  onConfirm: (payload: ConfirmPayload) => void;
  onCancel: () => void;
}

const toolLabels: Record<string, { icon: LucideIcon; label: string }> = {
  update_deal: { icon: Pencil, label: 'Update Deal' },
  create_task: { icon: SquareCheck, label: 'Create Task' },
  log_activity: { icon: FileText, label: 'Log Activity' },
  create_contact: { icon: UserPlus, label: 'Create Contact' },
};

export default function ConfirmBanner({ payload, onConfirm, onCancel }: Props) {
  const tool = toolLabels[payload.tool];
  const Icon = tool?.icon ?? TriangleAlert;
  return (
    <div className="mb-1.5 rounded-xl border border-amber-500/50 bg-amber-500/10 px-3.5 py-2.5">
      <div className="mb-1.5 flex items-center gap-1.5">
        <TriangleAlert aria-hidden className="h-4 w-4 text-amber-600 dark:text-amber-400" />
        <span className="inline-flex items-center gap-1.5 text-[13px] font-bold text-amber-700 dark:text-amber-300">
          <Icon aria-hidden className="h-3.5 w-3.5" />
          {tool?.label ?? payload.tool}
        </span>
      </div>
      <p className="mb-2.5 text-xs text-muted-foreground">{payload.summary}</p>
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={onCancel}>Cancel</Button>
        <Button size="sm" onClick={() => onConfirm(payload)}>Confirm</Button>
      </div>
    </div>
  );
}
