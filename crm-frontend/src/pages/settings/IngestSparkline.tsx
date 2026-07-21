import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis } from 'recharts';
import type { DailyIngestCount } from '../../features/integrations/types';

// Per-source delivery counts over the last 30 days (L6.6).
//
// Stacked bars rather than a line, because the useful question is not "how many
// leads" but "how many of them landed": a line of totals looks identical whether a
// source is writing every lead or failing every one. The stack answers both at once.
//
// Themed with CSS variables rather than the raw hex the two existing charts use — a
// hard-coded palette is invisible in dark mode, and this sits inside a settings page
// that follows the tokens.

const SERIES = [
  { key: 'written', label: 'Written', color: 'hsl(var(--primary))' },
  { key: 'skipped', label: 'Skipped', color: 'hsl(38 92% 50%)' },
  { key: 'failed', label: 'Failed', color: 'hsl(var(--destructive))' },
] as const;

function dayLabel(day: string): string {
  // The API sends YYYY-MM-DD in UTC. Split rather than `new Date(day)`, which parses
  // a bare date as midnight UTC and then renders it in local time — west of Greenwich
  // that shifts every bar to the previous day.
  const [, m, d] = day.split('-');
  return `${d}/${m}`;
}

export default function IngestSparkline({ data }: { data: DailyIngestCount[] }) {
  const total = data.reduce((n, d) => n + d.written + d.failed + d.skipped, 0);

  if (total === 0) {
    // An all-zero chart reads as a broken widget. Say the thing instead.
    return (
      <p className="text-xs text-muted-foreground">
        No deliveries in the last 30 days.
      </p>
    );
  }

  return (
    <div>
      <div className="h-24 w-full">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 4, right: 0, bottom: 0, left: 0 }}>
            <XAxis
              dataKey="day"
              tickFormatter={dayLabel}
              tick={{ fontSize: 10, fill: 'hsl(var(--muted-foreground))' }}
              axisLine={false}
              tickLine={false}
              interval="preserveStartEnd"
              minTickGap={24}
            />
            <Tooltip
              cursor={{ fill: 'hsl(var(--muted))' }}
              labelFormatter={(v) => dayLabel(String(v))}
              contentStyle={{
                background: 'hsl(var(--card))',
                border: '1px solid hsl(var(--border))',
                borderRadius: 8,
                fontSize: 12,
              }}
            />
            {SERIES.map((s) => (
              <Bar key={s.key} dataKey={s.key} name={s.label} stackId="a" fill={s.color} />
            ))}
          </BarChart>
        </ResponsiveContainer>
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-3">
        {SERIES.map((s) => (
          <span key={s.key} className="flex items-center gap-1 text-xs text-muted-foreground">
            <span className="inline-block h-2 w-2 rounded-sm" style={{ background: s.color }} />
            {s.label}
          </span>
        ))}
      </div>
    </div>
  );
}
