import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams, useSearchParams } from 'react-router-dom';
import {
  ArrowLeft, BarChart3, Globe, Hash, LineChart, Lock, PieChart, Table, type LucideIcon,
} from 'lucide-react';
import {
  createReport, deleteReport, exportReportCsv, getReport, listRegistryObjects, listReportFields, updateReport,
  type ObjectSummary, type Report, type ReportChart as ReportChartKind, type ReportConfig,
  type ReportDateBucket, type ReportFieldDescriptor, type ReportVisibility,
} from '../../lib/api';
import { useReportPreview, isRunnableConfig } from './useReportPreview';
import { Button } from '../../components/ui/button';
import { Skeleton } from '../../components/ui/skeleton';
import { usePermissions } from '../../lib/auth';
import { useDocumentTitle } from '../../lib/useDocumentTitle';
import { REPORT_TEMPLATES } from './templates';
import ReportChart from './charts/ReportChart';
import FilterEditor from './builder/FilterEditor';
import ReportShareDialog from './ReportShareDialog';
import ReportComments from './ReportComments';

const CHART_TYPES: { value: ReportChartKind; label: string; icon: LucideIcon }[] = [
  { value: 'bar', label: 'Bar', icon: BarChart3 },
  { value: 'line', label: 'Line', icon: LineChart },
  { value: 'pie', label: 'Pie', icon: PieChart },
  { value: 'donut', label: 'Donut', icon: PieChart },
  { value: 'kpi', label: 'Number', icon: Hash },
  { value: 'table', label: 'Table', icon: Table },
];

const BUCKETS: { value: ReportDateBucket; label: string }[] = [
  { value: 'day', label: 'Day' },
  { value: 'week', label: 'Week' },
  { value: 'month', label: 'Month' },
  { value: 'quarter', label: 'Quarter' },
  { value: 'year', label: 'Year' },
];

const DEFAULT_CONFIG: ReportConfig = { chart: 'bar', aggregate: { fn: 'count' } };

// Builds and edits one report: config panel on the left, live server-side
// preview on the right (the preview endpoint applies the caller's OLS/FLS and
// data scope, so what you see while building is what viewers of your role
// would get).
export default function ReportBuilderPage() {
  const { id } = useParams<{ id: string }>();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();

  const template = !id ? REPORT_TEMPLATES.find((t) => t.id === searchParams.get('template')) : undefined;

  const [name, setName] = useState(template?.name ?? '');
  const [description, setDescription] = useState('');
  const [visibility, setVisibility] = useState<ReportVisibility>('private');
  const [objectSlug, setObjectSlug] = useState(template?.objectSlug ?? 'deal');
  const [config, setConfig] = useState<ReportConfig>(template?.config ?? DEFAULT_CONFIG);
  const [nameError, setNameError] = useState(false);
  const [loaded, setLoaded] = useState(!id);

  const { data: existing } = useQuery<Report>({
    queryKey: ['report', id],
    queryFn: () => getReport(id!),
    enabled: Boolean(id),
  });

  // Tab title from the SAVED report (U7.2) — `existing.name` from react-query,
  // never the `name` useState above, which is bound to the name input and would
  // retitle the tab on every keystroke while the report is being named.
  useDocumentTitle(id ? existing?.name : 'New Report');

  // Populate once from the saved definition (not on every refetch).
  useEffect(() => {
    if (existing && !loaded) {
      setName(existing.name);
      setDescription(existing.description);
      setVisibility(existing.visibility);
      setObjectSlug(existing.object_slug);
      setConfig(existing.config ?? DEFAULT_CONFIG);
      setLoaded(true);
    }
  }, [existing, loaded]);

  const { data: objects = [] } = useQuery<ObjectSummary[]>({
    queryKey: ['registry-objects'],
    queryFn: listRegistryObjects,
  });

  const { data: fields = [] } = useQuery<ReportFieldDescriptor[]>({
    queryKey: ['report-fields', objectSlug],
    queryFn: () => listReportFields(objectSlug),
    enabled: Boolean(objectSlug),
  });

  const numberFields = useMemo(() => fields.filter((f) => f.type === 'number'), [fields]);
  const fieldLabels = useMemo(() => Object.fromEntries(fields.map((f) => [f.key, f.label])), [fields]);

  const preview = useReportPreview(loaded ? objectSlug : undefined, config);

  const [showShare, setShowShare] = useState(false);
  // A new report is fully editable by its creator. An existing one uses the
  // server-resolved access level: edit/manage may modify; only manage may
  // delete or change who it's shared with.
  const level = existing?.access_level;
  const canManage = !existing || level === 'edit' || level === 'manage';
  const canShare = level === 'manage';
  const canDelete = level === 'manage';
  // CSV export is capability-gated server-side (data.export) — hide the button
  // instead of letting it 403 (U3.7).
  const { can } = usePermissions();
  const canExport = can('data.export');

  const saveMutation = useMutation({
    mutationFn: async () => {
      const input = { name: name.trim(), description, object_slug: objectSlug, visibility, config };
      return existing ? updateReport(existing.id, input) : createReport(input);
    },
    onSuccess: (rep) => {
      queryClient.invalidateQueries({ queryKey: ['reports'] });
      queryClient.invalidateQueries({ queryKey: ['report', rep.id] });
      if (!existing) navigate(`/reports/${rep.id}`, { replace: true });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteReport(existing!.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['reports'] });
      navigate('/reports');
    },
  });

  const handleSave = () => {
    if (!name.trim()) {
      setNameError(true);
      return;
    }
    setNameError(false);
    saveMutation.mutate();
  };

  const handleExport = async () => {
    if (!existing) return;
    const blob = await exportReportCsv(existing.id);
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${existing.name || 'report'}.csv`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  };

  const patchConfig = (patch: Partial<ReportConfig>) => setConfig((c) => ({ ...c, ...patch }));

  const setChart = (chart: ReportChartKind) => {
    setConfig((c) => {
      const next: ReportConfig = { ...c, chart };
      if (chart === 'kpi') next.group_by = undefined;
      if (chart === 'table' && (!next.columns || next.columns.length === 0)) {
        next.columns = fields.slice(0, 4).map((f) => f.key);
      }
      if (chart !== 'table' && (!next.aggregate || !next.aggregate.fn)) {
        next.aggregate = { fn: 'count' };
      }
      return next;
    });
  };

  const groupField = fields.find((f) => f.key === config.group_by?.field);

  return (
    <div className="mx-auto max-w-7xl space-y-4">
      {/* Header: name, visibility, save/export/delete */}
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" onClick={() => navigate('/reports')} className="text-muted-foreground">
          <ArrowLeft aria-hidden />
          Reports
        </Button>
        <input
          aria-label="Report name"
          value={name}
          onChange={(e) => { setName(e.target.value); if (e.target.value.trim()) setNameError(false); }}
          placeholder="Untitled report"
          className={`min-w-56 flex-1 rounded-lg border bg-background px-3 py-2 text-lg font-semibold focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/30 ${nameError ? 'border-destructive' : 'border-input'}`}
          disabled={!canManage}
        />
        {existing && canShare && (
          <>
            <span className="inline-flex items-center gap-1.5 rounded-lg border border-border px-2.5 py-2 text-sm text-muted-foreground">
              {existing.visibility === 'org'
                ? <><Globe aria-hidden className="h-3.5 w-3.5" /> Workspace</>
                : <><Lock aria-hidden className="h-3.5 w-3.5" /> Private</>}
            </span>
            <Button variant="outline" onClick={() => setShowShare(true)}>Share</Button>
          </>
        )}
        {existing && canExport && (
          <Button variant="outline" onClick={handleExport}>Export CSV</Button>
        )}
        {canManage && (
          <Button onClick={handleSave} disabled={saveMutation.isPending}>
            {saveMutation.isPending ? 'Saving…' : existing ? 'Save changes' : 'Save report'}
          </Button>
        )}
        {existing && canDelete && (
          <Button
            variant="outline"
            onClick={() => { if (window.confirm(`Delete report "${existing.name}"?`)) deleteMutation.mutate(); }}
            className="border-destructive/40 text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            Delete
          </Button>
        )}
      </div>
      {showShare && existing && <ReportShareDialog report={existing} onClose={() => setShowShare(false)} />}
      {saveMutation.isError && <div className="text-sm text-destructive">{(saveMutation.error as Error).message}</div>}

      <div className="grid gap-4 lg:grid-cols-[400px_1fr]">
        {/* Config panel */}
        <div className="space-y-5 rounded-xl border bg-card p-4">
          <Section label="Object">
            <select
              aria-label="Report object"
              value={objectSlug}
              onChange={(e) => {
                setObjectSlug(e.target.value);
                // A new object means a new field set — reset the query parts.
                setConfig((c) => ({ chart: c.chart, aggregate: { fn: 'count' } }));
              }}
              className="w-full rounded-md border bg-background px-2 py-2 text-sm"
              disabled={!canManage}
            >
              {objects.map((o) => <option key={o.slug} value={o.slug}>{o.icon} {o.label_plural}</option>)}
            </select>
          </Section>

          <Section label="Chart type">
            <div className="grid grid-cols-3 gap-2">
              {CHART_TYPES.map((c) => (
                <button
                  key={c.value}
                  type="button"
                  onClick={() => setChart(c.value)}
                  disabled={!canManage}
                  className={`inline-flex items-center justify-center gap-1.5 rounded-lg border px-2 py-2 text-sm transition-colors ${config.chart === c.value ? 'border-primary bg-primary/10 font-medium' : 'border-border hover:bg-accent'}`}
                >
                  <c.icon aria-hidden className="h-4 w-4" />
                  {c.label}
                </button>
              ))}
            </div>
          </Section>

          {config.chart !== 'kpi' && config.chart !== 'table' && (
            <Section label="Group by">
              <div className="flex gap-2">
                <select
                  aria-label="Group by field"
                  value={config.group_by?.field ?? ''}
                  onChange={(e) => patchConfig({ group_by: e.target.value ? { field: e.target.value } : undefined })}
                  className="flex-1 rounded-md border bg-background px-2 py-2 text-sm"
                  disabled={!canManage}
                >
                  <option value="">Choose a field…</option>
                  {fields.map((f) => <option key={f.key} value={f.key}>{f.label}</option>)}
                </select>
                {groupField?.type === 'date' && (
                  <select
                    aria-label="Date bucket"
                    value={config.group_by?.bucket ?? 'month'}
                    onChange={(e) => patchConfig({ group_by: { field: config.group_by!.field, bucket: e.target.value as ReportDateBucket } })}
                    className="w-28 rounded-md border bg-background px-2 py-2 text-sm"
                    disabled={!canManage}
                  >
                    {BUCKETS.map((b) => <option key={b.value} value={b.value}>{b.label}</option>)}
                  </select>
                )}
              </div>
            </Section>
          )}

          {config.chart !== 'table' && (
            <Section label="Measure">
              <div className="flex gap-2">
                <select
                  aria-label="Aggregate function"
                  value={config.aggregate?.fn ?? 'count'}
                  onChange={(e) => {
                    const fn = e.target.value as NonNullable<ReportConfig['aggregate']>['fn'];
                    patchConfig({
                      aggregate: fn === 'count'
                        ? { fn }
                        : { fn, field: config.aggregate?.field ?? numberFields[0]?.key },
                    });
                  }}
                  className="w-36 rounded-md border bg-background px-2 py-2 text-sm"
                  disabled={!canManage}
                >
                  <option value="count">Count</option>
                  <option value="sum">Sum of</option>
                  <option value="avg">Average of</option>
                  <option value="min">Min of</option>
                  <option value="max">Max of</option>
                </select>
                {config.aggregate?.fn && config.aggregate.fn !== 'count' && (
                  <select
                    aria-label="Aggregate field"
                    value={config.aggregate?.field ?? ''}
                    onChange={(e) => patchConfig({ aggregate: { fn: config.aggregate!.fn, field: e.target.value } })}
                    className="flex-1 rounded-md border bg-background px-2 py-2 text-sm"
                    disabled={!canManage}
                  >
                    {numberFields.length === 0 && <option value="">No number fields</option>}
                    {numberFields.map((f) => <option key={f.key} value={f.key}>{f.label}</option>)}
                  </select>
                )}
              </div>
            </Section>
          )}

          {config.chart === 'table' && (
            <Section label="Columns">
              <div className="space-y-1">
                {fields.map((f) => {
                  const checked = config.columns?.includes(f.key) ?? false;
                  return (
                    <label key={f.key} className="flex items-center gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={checked}
                        disabled={!canManage}
                        onChange={(e) => {
                          const cols = new Set(config.columns ?? []);
                          if (e.target.checked) cols.add(f.key); else cols.delete(f.key);
                          // Preserve catalog order for stable headers.
                          patchConfig({ columns: fields.filter((x) => cols.has(x.key)).map((x) => x.key) });
                        }}
                      />
                      {f.label}
                    </label>
                  );
                })}
              </div>
            </Section>
          )}

          <Section label="Filters">
            <FilterEditor
              fields={fields}
              value={config.filters}
              onChange={(g) => patchConfig({ filters: g })}
            />
          </Section>

          {config.chart !== 'kpi' && config.chart !== 'table' && (
            <Section label="Sort & limit">
              <div className="flex gap-2">
                <select
                  aria-label="Sort by"
                  value={`${config.sort?.by ?? 'default'}:${config.sort?.dir ?? 'desc'}`}
                  onChange={(e) => {
                    const [by, dir] = e.target.value.split(':');
                    patchConfig({ sort: by === 'default' ? undefined : { by, dir: dir as 'asc' | 'desc' } });
                  }}
                  className="flex-1 rounded-md border bg-background px-2 py-2 text-sm"
                  disabled={!canManage}
                >
                  <option value="default:desc">Default order</option>
                  <option value="value:desc">Highest value first</option>
                  <option value="value:asc">Lowest value first</option>
                  <option value="label:asc">Label A→Z</option>
                  <option value="label:desc">Label Z→A</option>
                </select>
                <input
                  aria-label="Group limit"
                  type="number"
                  min={1}
                  max={100}
                  placeholder="Top N"
                  value={config.limit ?? ''}
                  onChange={(e) => patchConfig({ limit: e.target.value === '' ? undefined : Number(e.target.value) })}
                  className="w-24 rounded-md border bg-background px-2 py-2 text-sm"
                  disabled={!canManage}
                />
              </div>
            </Section>
          )}
        </div>

        {/* Live preview + discussion */}
        <div className="space-y-4">
        <div className="rounded-xl border bg-card p-4">
          <div className="mb-3 flex items-center justify-between">
            <h3 className="text-sm font-semibold">{name.trim() || 'Preview'}</h3>
            {preview.data && (
              <span className="text-xs text-muted-foreground">
                {preview.isFetching ? 'Updating…' : `${preview.data.row_count.toLocaleString()} record${preview.data.row_count === 1 ? '' : 's'}`}
              </span>
            )}
          </div>
          {!isRunnableConfig(config) ? (
            <div className="flex h-72 items-center justify-center text-sm text-muted-foreground">
              {config.chart === 'table' ? 'Pick at least one column to preview.' : 'Pick a "Group by" field to preview.'}
            </div>
          ) : preview.isError ? (
            <div className="flex h-72 items-center justify-center px-8 text-center text-sm text-destructive">{preview.error.message}</div>
          ) : preview.isLoading ? (
            <Skeleton className="h-72 rounded-lg" />
          ) : preview.data ? (
            <ReportChart chart={config.chart} result={preview.data} height={380} columnLabels={fieldLabels} />
          ) : null}
        </div>
        {existing && (
          <ReportComments reportId={existing.id} canComment={['comment', 'edit', 'manage'].includes(level ?? '')} />
        )}
        </div>
      </div>
    </div>
  );
}

function Section({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">{label}</div>
      {children}
    </div>
  );
}
