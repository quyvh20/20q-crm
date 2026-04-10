import { useQuery } from '@tanstack/react-query';
import { getForecast, type ForecastRow } from '../../lib/api';
import { Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer, Line, ComposedChart } from 'recharts';

export default function ForecastChart() {
  const { data: forecast = [], isLoading } = useQuery<ForecastRow[]>({
    queryKey: ['forecast'],
    queryFn: getForecast,
  });

  if (isLoading) {
    return <div className="h-64 rounded-xl bg-muted/50 animate-pulse" />;
  }

  if (forecast.length === 0) {
    return (
      <div className="h-64 rounded-xl border bg-card flex items-center justify-center">
        <p className="text-sm text-muted-foreground">No forecast data — add deals with expected close dates</p>
      </div>
    );
  }

  const formatMonth = (m: string) => {
    const [year, month] = m.split('-');
    const date = new Date(Number(year), Number(month) - 1);
    return date.toLocaleDateString('en-US', { month: 'short', year: '2-digit' });
  };

  const formatted = forecast.map(r => ({
    ...r,
    label: formatMonth(r.month),
    revenue: Math.round(r.expected_revenue),
  }));

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
            tickFormatter={(v: number) => `$${(v / 1000).toFixed(0)}K`}
          />
          <YAxis
            yAxisId="right"
            orientation="right"
            tick={{ fontSize: 11 }}
            stroke="hsl(var(--muted-foreground))"
          />
          <Tooltip
            contentStyle={{
              background: 'hsl(var(--card))',
              border: '1px solid hsl(var(--border))',
              borderRadius: 12,
              fontSize: 12,
            }}
            formatter={(value, name) => [
              name === 'revenue' ? `$${Number(value).toLocaleString()}` : value,
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
