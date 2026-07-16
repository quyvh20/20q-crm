import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Box } from 'lucide-react';
import { getAIUsage } from '../../lib/api';
import { Skeleton } from '@/components/ui';

export default function AIUsageWidget() {
  const { data: usage, isLoading } = useQuery({
    queryKey: ['ai-usage'],
    queryFn: getAIUsage,
    retry: false,
    staleTime: 60_000,
  });

  if (isLoading) {
    return (
      <div className="my-1 rounded-xl border border-border bg-card p-3">
        <Skeleton className="h-14 w-full rounded-lg" />
      </div>
    );
  }

  if (!usage) return null;

  const pct = usage.percent;
  // Semantic meter color: red near the cap, amber when warm, primary otherwise.
  const barColor = pct >= 90 ? 'bg-destructive' : pct >= 70 ? 'bg-amber-500' : 'bg-primary';
  const used = (usage.used_tokens / 1000).toFixed(1);
  const limit = (usage.limit_tokens / 1000).toFixed(0);

  return (
    <div className="my-1 rounded-xl border border-border bg-card p-3" id="ai-usage-widget">
      <div className="mb-2 flex items-center justify-between">
        <span className="flex items-center gap-1 text-xs font-medium text-muted-foreground">
          <Box aria-hidden className="h-3.5 w-3.5" />
          AI Tokens
        </span>
        <span className="text-xs font-semibold text-foreground">
          {used}k / {limit}k
        </span>
      </div>

      {/* Progress bar */}
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div
          className={`h-full rounded-full transition-[width] duration-500 ${barColor}`}
          style={{ width: `${Math.min(pct, 100)}%` }}
        />
      </div>

      <div className="mt-1.5 flex justify-between text-[10px] text-muted-foreground">
        <span className={pct >= 90 ? 'flex items-center gap-0.5 font-medium text-destructive' : undefined}>
          {pct >= 90 ? (
            <>
              <AlertTriangle aria-hidden className="h-2.5 w-2.5" /> Near limit
            </>
          ) : (
            `${pct}% used`
          )}
        </span>
        <span>Resets {usage.reset_at}</span>
      </div>
    </div>
  );
}
