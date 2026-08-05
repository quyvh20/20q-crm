import { useQuery } from '@tanstack/react-query';
import { getForecast, type ForecastRow } from '../../lib/api';
import { formatCurrency, formatDate } from '../../lib/format';
import { useWorkspaceFormat } from '../../lib/useWorkspaceFormat';
import { Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Line, ComposedChart } from 'recharts';

/**
 * Build a full 12-month window starting from the current month.
 *
 * `locale` is threaded in rather than baked: the axis used to be hardcoded to
 * 'en-US', so a workspace set to de-DE or ja-JP still read "Jan 26".
 */
function buildFullYear(apiData: ForecastRow[], locale?: string) {
  const lookup = new Map(apiData.map(r => [r.month, r]));
  const now = new Date();
  const months: { month: string; label: string; revenue: number; deals_count: number }[] = [];

  for (let i = 0; i < 12; i++) {
    const d = new Date(now.getFullYear(), now.getMonth() + i, 1);
    const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`;
    const label = formatDate(d, { locale, month: 'short', year: '2-digit' });
    const row = lookup.get(key);
    months.push({
      month: key,
      label,
      revenue: row ? Math.round(row.expected_revenue) : 0,
      deals_count: row ? row.deals_count : 0,
    });
  }
  return months;
}

export default function ForecastChart() {
  const fmt = useWorkspaceFormat();
  const { data: forecast = [], isLoading } = useQuery<ForecastRow[]>({
    queryKey: ['forecast'],
    queryFn: getForecast,
  });

  if (isLoading) {
    return <div className="h-64 rounded-xl bg-muted/50 animate-pulse" />;
  }

  const formatted = buildFullYear(forecast, fmt.locale);
  const maxRevenue = Math.max(...formatted.map(f => f.revenue), 1);

  return (
    <div className="rounded-xl border bg-card p-4">
      <h3 className="text-sm font-semibold mb-4">Revenue Forecast (12 months)</h3>
      <ResponsiveContainer width="100%" height={280}>
        <ComposedChart data={formatted}>
          <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
          <XAxis dataKey="label" tick={{ fontSize: 11 }} stroke="hsl(var(--muted-foreground))" />
          <YAxis
            yAxisId="left"
            tick={{ fontSize: 11 }}
            stroke="hsl(var(--muted-foreground))"
            // Compact notation replaces the hand-rolled `$${v/1000}K`: Intl picks
            // the right symbol AND the right abbreviation per locale ("$12K",
            // "12 k €", "12 Tr ₫") instead of assuming every currency divides
            // into thousands the English way.
            tickFormatter={(v: number) => formatCurrency(v, { ...fmt, notation: 'compact', maximumFractionDigits: 0 })}
            domain={[0, Math.ceil(maxRevenue * 1.2 / 1000) * 1000]}
          />
          <YAxis
            yAxisId="right"
            orientation="right"
            tick={{ fontSize: 11 }}
            stroke="hsl(var(--muted-foreground))"
            allowDecimals={false}
          />
          <Tooltip
            contentStyle={{
              background: 'hsl(var(--card))',
              border: '1px solid hsl(var(--border))',
              borderRadius: 12,
              fontSize: 12,
            }}
            formatter={(value, name) => [
              name === 'revenue' ? formatCurrency(value, fmt) : value,
              name === 'revenue' ? 'Expected Revenue' : 'Deals',
            ]}
          />
          <Bar yAxisId="left" dataKey="revenue" fill="#3B82F6" radius={[6, 6, 0, 0]} barSize={32} />
          <Line yAxisId="right" type="monotone" dataKey="deals_count" stroke="#F59E0B" strokeWidth={2} dot={{ fill: '#F59E0B', r: 3 }} />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
