import { useQuery } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { BarChart3, Hash, LineChart, PieChart, Table, type LucideIcon } from 'lucide-react';
import { listReports, type Report, type ReportChart } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { Badge } from '../../components/ui/badge';
import { Button } from '../../components/ui/button';
import { EmptyState } from '../../components/ui/empty-state';
import { PageHeader } from '../../components/ui/page-header';
import { Skeleton } from '../../components/ui/skeleton';
import { REPORT_TEMPLATES } from './templates';

const CHART_ICONS: Record<ReportChart, LucideIcon> = {
  bar: BarChart3, line: LineChart, pie: PieChart, donut: PieChart, kpi: Hash, table: Table,
};

function ChartIcon({ chart }: { chart?: ReportChart }) {
  const Icon = (chart && CHART_ICONS[chart]) || BarChart3;
  return (
    <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-muted">
      <Icon aria-hidden className="h-4 w-4 text-muted-foreground" />
    </span>
  );
}

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
    <div className="mx-auto w-full max-w-5xl space-y-8">
      <PageHeader
        className="mb-0"
        title="Reports"
        description="Charts and tables over your CRM data — always filtered to what each viewer may see."
        actions={<Button onClick={() => navigate('/reports/new')}>+ New report</Button>}
      />

      {isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : reports.length === 0 ? (
        <EmptyState
          icon={BarChart3}
          title="No reports yet"
          description="Start from a template below, or build one from scratch."
        />
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
              className="rounded-xl border border-border bg-card p-4 text-left transition-colors hover:border-primary/50 hover:bg-accent"
            >
              <ChartIcon chart={t.config.chart} />
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
      <div className="overflow-hidden rounded-xl border border-border">
        {reports.map((r) => (
          <Link
            key={r.id}
            to={`/reports/${r.id}`}
            className="flex items-center gap-3 border-b border-border px-4 py-3 last:border-0 hover:bg-accent"
          >
            <ChartIcon chart={r.config?.chart} />
            <div className="min-w-0 flex-1">
              <div className="truncate font-medium">{r.name}</div>
              {r.description && <div className="truncate text-xs text-muted-foreground">{r.description}</div>}
            </div>
            <Badge variant="outline">{r.object_slug}</Badge>
            {r.visibility === 'org'
              ? <Badge>Shared</Badge>
              : <Badge variant="secondary" className="text-muted-foreground">Private</Badge>}
          </Link>
        ))}
      </div>
    </section>
  );
}
