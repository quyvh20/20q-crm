import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from 'react-router-dom';
import {
  addDashboardWidget, listDashboardWidgets, listReports, removeDashboardWidget,
  reorderDashboardWidgets, runReport, updateDashboardWidget,
  type DashboardWidget, type Report, type ReportResult,
} from '../../lib/api';
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
    <div className="mx-auto max-w-6xl space-y-6">
      <SetupChecklist />

      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Dashboard</h1>
          <p className="text-sm text-muted-foreground">Your pinned reports, refreshed on every visit.</p>
        </div>
        <button
          onClick={() => setAdding((v) => !v)}
          className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90"
        >
          + Add widget
        </button>
      </div>

      {adding && (
        <AddWidgetPicker
          pinnedReportIds={widgets.map((w) => w.report_id)}
          onAdd={(id) => addMutation.mutate(id)}
          adding={addMutation.isPending}
        />
      )}

      {isLoading ? (
        <div className="grid gap-4 md:grid-cols-2">
          <div className="h-72 animate-pulse rounded-xl bg-muted/50" />
          <div className="h-72 animate-pulse rounded-xl bg-muted/50" />
        </div>
      ) : widgets.length === 0 ? (
        <div className="rounded-xl border border-dashed p-12 text-center">
          <div className="text-4xl">📊</div>
          <h2 className="mt-3 font-semibold">Your dashboard is empty</h2>
          <p className="mx-auto mt-1 max-w-md text-sm text-muted-foreground">
            Pin any saved report here to see it every time you open the app.
            Build one in <Link to="/reports" className="text-primary underline">Reports</Link> — there are ready-made templates to start from.
          </p>
          <button
            onClick={() => navigate('/reports')}
            className="mt-4 rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90"
          >
            Go to Reports
          </button>
        </div>
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
      <div className="rounded-xl border bg-card p-4 text-sm text-muted-foreground">
        Every report you can see is already pinned. <Link to="/reports/new" className="text-primary underline">Build a new one</Link>.
      </div>
    );
  }
  return (
    <div className="flex items-center gap-2 rounded-xl border bg-card p-4">
      <select
        aria-label="Report to pin"
        value={selected}
        onChange={(e) => setSelected(e.target.value)}
        className="flex-1 rounded-md border bg-background px-2 py-2 text-sm"
      >
        <option value="">Choose a report…</option>
        {available.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
      </select>
      <button
        onClick={() => selected && onAdd(selected)}
        disabled={!selected || adding}
        className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
      >
        {adding ? 'Pinning…' : 'Pin to dashboard'}
      </button>
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

  const btn = 'rounded px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-accent hover:text-foreground';

  return (
    <div className={`rounded-xl border bg-card p-4 ${widget.size === 'full' ? 'md:col-span-2' : ''}`}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <Link to={`/reports/${widget.report_id}`} className="truncate text-sm font-semibold hover:underline">
          {report?.name ?? 'Report'}
        </Link>
        <div className="flex shrink-0 items-center gap-1">
          {onMoveUp && <button className={btn} onClick={onMoveUp} title="Move up" aria-label="Move widget up">↑</button>}
          {onMoveDown && <button className={btn} onClick={onMoveDown} title="Move down" aria-label="Move widget down">↓</button>}
          <button className={btn} onClick={onResize} title={widget.size === 'half' ? 'Expand' : 'Shrink'} aria-label="Toggle widget size">
            {widget.size === 'half' ? '⤢' : '⤡'}
          </button>
          <button className={btn} onClick={onRemove} title="Remove from dashboard" aria-label="Remove widget">✕</button>
        </div>
      </div>
      {isLoading ? (
        <div className="h-56 animate-pulse rounded-lg bg-muted/50" />
      ) : isError ? (
        <div className="flex h-56 items-center justify-center px-6 text-center text-sm text-red-600">{error.message}</div>
      ) : data && report ? (
        <ReportChart chart={report.config.chart} result={data} height={widget.size === 'full' ? 320 : 240} />
      ) : null}
    </div>
  );
}
