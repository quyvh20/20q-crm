import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { listReports, type Report, type ReportChart } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { REPORT_TEMPLATES } from './templates';

const CHART_ICONS: Record<ReportChart, string> = {
  bar: '📊', line: '📈', pie: '🥧', donut: '🍩', kpi: '🔢', table: '📋',
};

export default function ReportsListPage() {
  const navigate = useNavigate();
  const { user } = useAuth();
  const { data: reports = [], isLoading } = useQuery<Report[]>({
    queryKey: ['reports'],
    queryFn: listReports,
  });

  const mine = reports.filter((r) => r.created_by === user?.id);
  const shared = reports.filter((r) => r.created_by !== user?.id);

  return (
    <div className="mx-auto max-w-5xl space-y-8">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Reports</h1>
          <p className="text-sm text-muted-foreground">Charts and tables over your CRM data — always filtered to what each viewer may see.</p>
        </div>
        <button
          onClick={() => navigate('/reports/new')}
          className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90"
        >
          + New report
        </button>
      </div>

      {isLoading ? (
        <div className="h-40 animate-pulse rounded-xl bg-muted/50" />
      ) : reports.length === 0 ? (
        <div className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">
          No reports yet — start from a template below, or build one from scratch.
        </div>
      ) : (
        <div className="space-y-6">
          {mine.length > 0 && <ReportGroup title="My reports" reports={mine} />}
          {shared.length > 0 && <ReportGroup title="Shared with the workspace" reports={shared} />}
        </div>
      )}

      <section>
        <h2 className="mb-3 text-sm font-semibold text-muted-foreground">Start from a template</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {REPORT_TEMPLATES.map((t) => (
            <button
              key={t.id}
              onClick={() => navigate(`/reports/new?template=${t.id}`)}
              className="rounded-xl border bg-card p-4 text-left transition-colors hover:border-primary/50 hover:bg-accent"
            >
              <div className="text-2xl">{t.icon}</div>
              <div className="mt-2 font-medium">{t.name}</div>
              <div className="mt-1 text-xs text-muted-foreground">{t.description}</div>
            </button>
          ))}
        </div>
      </section>
    </div>
  );
}

function ReportGroup({ title, reports }: { title: string; reports: Report[] }) {
  return (
    <section>
      <h2 className="mb-3 text-sm font-semibold text-muted-foreground">{title}</h2>
      <div className="overflow-hidden rounded-xl border">
        {reports.map((r) => (
          <Link
            key={r.id}
            to={`/reports/${r.id}`}
            className="flex items-center gap-3 border-b px-4 py-3 last:border-0 hover:bg-accent"
          >
            <span className="text-xl">{CHART_ICONS[r.config?.chart] ?? '📊'}</span>
            <div className="min-w-0 flex-1">
              <div className="truncate font-medium">{r.name}</div>
              {r.description && <div className="truncate text-xs text-muted-foreground">{r.description}</div>}
            </div>
            <span className="rounded-full border px-2 py-0.5 text-xs text-muted-foreground">{r.object_slug}</span>
            {r.visibility === 'org'
              ? <span className="rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary">Shared</span>
              : <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">Private</span>}
          </Link>
        ))}
      </div>
    </section>
  );
}
