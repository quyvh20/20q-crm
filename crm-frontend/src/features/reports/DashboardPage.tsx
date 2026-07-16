import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowDown, ArrowUp, BarChart3, Maximize2, Minimize2, Plus, X } from 'lucide-react';
import {
  addDashboardWidget, listDashboardWidgets, listReports, removeDashboardWidget,
  reorderDashboardWidgets, runReport, updateDashboardWidget,
  type DashboardWidget, type Report, type ReportResult,
} from '../../lib/api';
import { Button } from '../../components/ui/button';
import { EmptyState } from '../../components/ui/empty-state';
import { PageHeader } from '../../components/ui/page-header';
import { Skeleton } from '../../components/ui/skeleton';
import ReportChart from './charts/ReportChart';
import SetupChecklist from '../onboarding/SetupChecklist';

// The home page: every user's own grid of pinned reports. Each widget runs its
// report through the normal run endpoint, so two users pinning the same shared
// report can legitimately see different numbers (OLS/FLS/data scope).
//
// It also hosts the setup checklist (U7.5) — the returnable successor to the
// blocking welcome wizard. The card renders itself away once its steps are done or
// the user hides it, so an established workspace sees exactly what it sees today.
export default function DashboardPage() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);

  const { data: widgets = [], isLoading } = useQuery<DashboardWidget[]>({
    queryKey: ['dashboard-widgets'],
    queryFn: listDashboardWidgets,
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['dashboard-widgets'] });

  const addMutation = useMutation({
    mutationFn: (reportId: string) => addDashboardWidget(reportId),
    onSuccess: () => { invalidate(); setAdding(false); },
  });
  const removeMutation = useMutation({ mutationFn: removeDashboardWidget, onSuccess: invalidate });
  const resizeMutation = useMutation({
    mutationFn: ({ id, size }: { id: string; size: 'half' | 'full' }) => updateDashboardWidget(id, size),
    onSuccess: invalidate,
  });
  const reorderMutation = useMutation({ mutationFn: reorderDashboardWidgets, onSuccess: invalidate });

  const move = (index: number, dir: -1 | 1) => {
    const target = index + dir;
    if (target < 0 || target >= widgets.length) return;
    const ids = widgets.map((w) => w.id);
    [ids[index], ids[target]] = [ids[target], ids[index]];
    reorderMutation.mutate(ids);
  };

  return (
    <div className="mx-auto w-full max-w-6xl space-y-6">
      <SetupChecklist />

      <PageHeader
        className="mb-0"
        title="Dashboard"
        description="Your pinned reports, refreshed on every visit."
        actions={
          <Button variant="outline" size="sm" onClick={() => setAdding((v) => !v)}>
            <Plus aria-hidden />
            Add widget
          </Button>
        }
      />

      {adding && (
        <AddWidgetPicker
          pinnedReportIds={widgets.map((w) => w.report_id)}
          onAdd={(id) => addMutation.mutate(id)}
          adding={addMutation.isPending}
        />
      )}

      {isLoading ? (
        <div className="grid gap-4 md:grid-cols-2">
          <Skeleton className="h-72 rounded-xl" />
          <Skeleton className="h-72 rounded-xl" />
        </div>
      ) : widgets.length === 0 ? (
        <EmptyState
          icon={BarChart3}
          title="Your dashboard is empty"
          description={
            <>
              Pin any saved report here to see it every time you open the app.
              Build one in <Link to="/reports" className="text-primary underline">Reports</Link> — there are ready-made templates to start from.
            </>
          }
          action={<Button onClick={() => navigate('/reports')}>Go to Reports</Button>}
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {widgets.map((w, i) => (
            <WidgetCard
              key={w.id}
              widget={w}
              onMoveUp={i > 0 ? () => move(i, -1) : undefined}
              onMoveDown={i < widgets.length - 1 ? () => move(i, 1) : undefined}
              onResize={() => resizeMutation.mutate({ id: w.id, size: w.size === 'half' ? 'full' : 'half' })}
              onRemove={() => removeMutation.mutate(w.id)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function AddWidgetPicker({ pinnedReportIds, onAdd, adding }: {
  pinnedReportIds: string[];
  onAdd: (reportId: string) => void;
  adding: boolean;
}) {
  const [selected, setSelected] = useState('');
  const { data: reports = [] } = useQuery<Report[]>({ queryKey: ['reports'], queryFn: listReports });
  const available = reports.filter((r) => !pinnedReportIds.includes(r.id));

  if (available.length === 0) {
    return (
      <div className="rounded-xl border border-border bg-card p-4 text-sm text-muted-foreground">
        Every report you can see is already pinned. <Link to="/reports/new" className="text-primary underline">Build a new one</Link>.
      </div>
    );
  }
  return (
    <div className="flex items-center gap-2 rounded-xl border border-border bg-card p-4">
      <select
        aria-label="Report to pin"
        value={selected}
        onChange={(e) => setSelected(e.target.value)}
        className="h-9 flex-1 rounded-lg border border-input bg-background px-2 text-sm focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30"
      >
        <option value="">Choose a report…</option>
        {available.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
      </select>
      <Button onClick={() => selected && onAdd(selected)} disabled={!selected || adding}>
        {adding ? 'Pinning…' : 'Pin to dashboard'}
      </Button>
    </div>
  );
}

function WidgetCard({ widget, onMoveUp, onMoveDown, onResize, onRemove }: {
  widget: DashboardWidget;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  onResize: () => void;
  onRemove: () => void;
}) {
  const report = widget.report;
  const { data, isLoading, isError, error } = useQuery<ReportResult, Error>({
    queryKey: ['report-run', widget.report_id],
    queryFn: () => runReport(widget.report_id),
    staleTime: 60_000,
  });

  const btn = 'h-7 w-7 text-muted-foreground hover:text-foreground';

  return (
    <div className={`rounded-xl border border-border bg-card p-4 ${widget.size === 'full' ? 'md:col-span-2' : ''}`}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <Link to={`/reports/${widget.report_id}`} className="truncate text-sm font-semibold hover:underline">
          {report?.name ?? 'Report'}
        </Link>
        <div className="flex shrink-0 items-center gap-1">
          {onMoveUp && (
            <Button variant="ghost" size="icon" className={btn} onClick={onMoveUp} title="Move up" aria-label="Move widget up">
              <ArrowUp aria-hidden />
            </Button>
          )}
          {onMoveDown && (
            <Button variant="ghost" size="icon" className={btn} onClick={onMoveDown} title="Move down" aria-label="Move widget down">
              <ArrowDown aria-hidden />
            </Button>
          )}
          <Button variant="ghost" size="icon" className={btn} onClick={onResize} title={widget.size === 'half' ? 'Expand' : 'Shrink'} aria-label="Toggle widget size">
            {widget.size === 'half' ? <Maximize2 aria-hidden /> : <Minimize2 aria-hidden />}
          </Button>
          <Button variant="ghost" size="icon" className={btn} onClick={onRemove} title="Remove from dashboard" aria-label="Remove widget">
            <X aria-hidden />
          </Button>
        </div>
      </div>
      {isLoading ? (
        <Skeleton className="h-56 rounded-lg" />
      ) : isError ? (
        <div className="flex h-56 items-center justify-center px-6 text-center text-sm text-destructive">{error.message}</div>
      ) : data && report ? (
        <ReportChart chart={report.config.chart} result={data} height={widget.size === 'full' ? 320 : 240} />
      ) : null}
    </div>
  );
}
