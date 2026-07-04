import {
  ResponsiveContainer, BarChart, Bar, LineChart, Line, PieChart, Pie, Cell,
  XAxis, YAxis, CartesianGrid, Tooltip, Legend,
} from 'recharts';
import type { ReportChart as ReportChartKind, ReportResult } from '../../../lib/api';

// One dispatcher renders every report kind, so the builder preview, the saved
// report viewer, and the dashboard widgets (Phase B) all share one component.

const PALETTE = ['#3B82F6', '#10B981', '#F59E0B', '#8B5CF6', '#EF4444', '#06B6D4', '#EC4899', '#84CC16', '#F97316', '#6366F1'];

const tooltipStyle = {
  background: 'hsl(var(--card))',
  border: '1px solid hsl(var(--border))',
  borderRadius: 12,
  fontSize: 12,
};

function compactNumber(v: number): string {
  if (Math.abs(v) >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`;
  if (Math.abs(v) >= 1_000) return `${(v / 1_000).toFixed(0)}K`;
  return `${v}`;
}

interface Props {
  chart: ReportChartKind;
  result: ReportResult;
  height?: number;
  // Optional key → label map for table headers (falls back to the raw key).
  columnLabels?: Record<string, string>;
}

export default function ReportChart({ chart, result, height = 300, columnLabels }: Props) {
  if (result.kind === 'scalar') return <KpiCard result={result} />;
  if (result.kind === 'rows') return <ReportTable result={result} columnLabels={columnLabels} />;

  const groups = result.groups ?? [];
  if (groups.length === 0) {
    return <div className="flex items-center justify-center text-sm text-muted-foreground" style={{ height }}>No data for this report yet.</div>;
  }
  const data = groups.map((g) => ({ label: g.label, value: g.value, count: g.count }));

  if (chart === 'pie' || chart === 'donut') {
    return (
      <ResponsiveContainer width="100%" height={height}>
        <PieChart>
          <Pie
            data={data}
            dataKey="value"
            nameKey="label"
            innerRadius={chart === 'donut' ? '55%' : 0}
            outerRadius="80%"
            paddingAngle={data.length > 1 ? 2 : 0}
          >
            {data.map((_, i) => <Cell key={i} fill={PALETTE[i % PALETTE.length]} />)}
          </Pie>
          <Legend wrapperStyle={{ fontSize: 12 }} />
          <Tooltip contentStyle={tooltipStyle} formatter={(v) => Number(v).toLocaleString()} />
        </PieChart>
      </ResponsiveContainer>
    );
  }

  if (chart === 'line') {
    return (
      <ResponsiveContainer width="100%" height={height}>
        <LineChart data={data}>
          <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
          <XAxis dataKey="label" tick={{ fontSize: 11 }} stroke="hsl(var(--muted-foreground))" />
          <YAxis tick={{ fontSize: 11 }} stroke="hsl(var(--muted-foreground))" tickFormatter={compactNumber} />
          <Tooltip contentStyle={tooltipStyle} formatter={(v) => Number(v).toLocaleString()} />
          <Line type="monotone" dataKey="value" stroke="#3B82F6" strokeWidth={2} dot={{ fill: '#3B82F6', r: 3 }} />
        </LineChart>
      </ResponsiveContainer>
    );
  }

  // bar (default)
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={data}>
        <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
        <XAxis dataKey="label" tick={{ fontSize: 11 }} stroke="hsl(var(--muted-foreground))" interval={0} angle={data.length > 6 ? -30 : 0} textAnchor={data.length > 6 ? 'end' : 'middle'} height={data.length > 6 ? 70 : 30} />
        <YAxis tick={{ fontSize: 11 }} stroke="hsl(var(--muted-foreground))" tickFormatter={compactNumber} />
        <Tooltip contentStyle={tooltipStyle} formatter={(v) => Number(v).toLocaleString()} />
        <Bar dataKey="value" radius={[6, 6, 0, 0]} barSize={36}>
          {data.map((_, i) => <Cell key={i} fill={PALETTE[i % PALETTE.length]} />)}
        </Bar>
      </BarChart>
    </ResponsiveContainer>
  );
}

function KpiCard({ result }: { result: ReportResult }) {
  return (
    <div className="flex flex-col items-center justify-center py-10">
      <div className="text-5xl font-bold tabular-nums">{result.value.toLocaleString()}</div>
      <div className="mt-2 text-sm text-muted-foreground">{result.row_count.toLocaleString()} record{result.row_count === 1 ? '' : 's'}</div>
    </div>
  );
}

function formatCell(v: unknown): string {
  if (v === null || v === undefined || v === '') return '—';
  if (typeof v === 'boolean') return v ? 'Yes' : 'No';
  if (typeof v === 'number') return v.toLocaleString();
  const s = String(v);
  // Timestamps come back RFC3339; show just the date part.
  if (/^\d{4}-\d{2}-\d{2}T/.test(s)) return s.slice(0, 10);
  return s;
}

function ReportTable({ result, columnLabels }: { result: ReportResult; columnLabels?: Record<string, string> }) {
  const columns = result.columns ?? [];
  const rows = result.rows ?? [];
  if (rows.length === 0) {
    return <div className="flex items-center justify-center py-10 text-sm text-muted-foreground">No records match this report.</div>;
  }
  return (
    <div className="overflow-auto rounded-lg border">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b bg-muted/50 text-left">
            {columns.map((c) => (
              <th key={c} className="px-3 py-2 font-medium">{columnLabels?.[c] ?? c}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, i) => (
            <tr key={(row.id as string) ?? i} className="border-b last:border-0 hover:bg-accent/50">
              {columns.map((c) => (
                <td key={c} className="px-3 py-2">{formatCell(row[c])}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
