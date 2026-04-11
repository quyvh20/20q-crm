import { useQuery } from '@tanstack/react-query';
import { getAIUsage } from '../../lib/api';

export default function AIUsageWidget() {
  const { data: usage, isLoading } = useQuery({
    queryKey: ['ai-usage'],
    queryFn: getAIUsage,
    retry: false,
    staleTime: 60_000,
  });

  if (isLoading) {
    return (
      <div className="ai-usage-widget loading">
        <div className="ai-usage-shimmer" />
      </div>
    );
  }

  if (!usage) return null;

  const pct = usage.percent;
  const color = pct >= 90 ? '#ef4444' : pct >= 70 ? '#f59e0b' : '#6366f1';
  const used = (usage.used_tokens / 1000).toFixed(1);
  const limit = (usage.limit_tokens / 1000).toFixed(0);

  return (
    <div className="ai-usage-widget" id="ai-usage-widget">
      <div className="ai-usage-header">
        <span className="ai-usage-label">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" style={{ display: 'inline', marginRight: 4 }}>
            <path d="M21 16V8a2 2 0 0 0-1-1.73l-7-4a2 2 0 0 0-2 0l-7 4A2 2 0 0 0 3 8v8a2 2 0 0 0 1 1.73l7 4a2 2 0 0 0 2 0l7-4A2 2 0 0 0 21 16z"/>
          </svg>
          AI Tokens
        </span>
        <span className="ai-usage-count">
          {used}k / {limit}k
        </span>
      </div>

      {/* Progress bar */}
      <div className="ai-usage-bar-bg">
        <div
          className="ai-usage-bar-fill"
          style={{ width: `${Math.min(pct, 100)}%`, background: color }}
        />
      </div>

      <div className="ai-usage-meta">
        <span style={{ color: pct >= 90 ? '#ef4444' : '#6b7280' }}>
          {pct >= 90 ? '⚠ Near limit' : `${pct}% used`}
        </span>
        <span>Resets {usage.reset_at}</span>
      </div>

      <style>{`
        .ai-usage-widget {
          background: var(--card, #fff);
          border: 1px solid var(--border, #e5e7eb);
          border-radius: 12px;
          padding: 12px 14px;
          margin: 4px 0;
        }
        .ai-usage-widget.loading { min-height: 72px; }
        .ai-usage-shimmer { height: 60px; background: linear-gradient(90deg, #f3f4f6 25%, #e5e7eb 50%, #f3f4f6 75%); background-size: 200% 100%; animation: shimmer 1.5s infinite; border-radius: 8px; }
        @keyframes shimmer { to { background-position: -200% 0; } }
        .ai-usage-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; }
        .ai-usage-label { font-size: 12px; font-weight: 500; color: var(--muted-foreground, #6b7280); display: flex; align-items: center; }
        .ai-usage-count { font-size: 12px; font-weight: 600; color: var(--foreground, #111); }
        .ai-usage-bar-bg { height: 6px; background: var(--muted, #f3f4f6); border-radius: 99px; overflow: hidden; }
        .ai-usage-bar-fill { height: 100%; border-radius: 99px; transition: width 0.6s ease; }
        .ai-usage-meta { display: flex; justify-content: space-between; margin-top: 6px; font-size: 10px; color: #9ca3af; }
      `}</style>
    </div>
  );
}
