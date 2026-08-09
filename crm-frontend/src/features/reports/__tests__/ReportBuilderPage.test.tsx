import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Report, ReportFieldDescriptor, ReportResult } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listReportObjects: vi.fn(),
  listReportFields: vi.fn(),
  previewReport: vi.fn(),
  createReport: vi.fn(),
  updateReport: vi.fn(),
  deleteReport: vi.fn(),
  getReport: vi.fn(),
  exportReportCsv: vi.fn(),
}));
vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'me' }, hasCapability: () => false }),
  // U3.7: the page gates Export CSV on can('data.export'). Grant it here so the
  // pre-existing header assertions keep seeing the button.
  usePermissions: () => ({ can: () => true, canAccess: () => true, loaded: true }),
}));

import {
  listReportObjects, listReportFields, previewReport, createReport, getReport,
} from '../../../lib/api';
import ReportBuilderPage from '../ReportBuilderPage';

const dealFields: ReportFieldDescriptor[] = [
  { key: 'title', label: 'Title', type: 'text' },
  { key: 'value', label: 'Value', type: 'number' },
  { key: 'stage', label: 'Stage', type: 'relation' },
  { key: 'closed_at', label: 'Closed At', type: 'date' },
  { key: 'is_won', label: 'Is Won', type: 'boolean' },
];

const groupsResult: ReportResult = {
  kind: 'groups',
  groups: [{ key: 's1', label: 'Negotiation', value: 12000, count: 4 }],
  value: 0,
  row_count: 4,
};

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.pathname}</div>;
}

function renderBuilder(initialEntry: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[initialEntry]}>
        <Routes>
          <Route path="/reports/new" element={<ReportBuilderPage />} />
          <Route path="/reports/:id" element={<ReportBuilderPage />} />
          <Route path="/reports" element={<div>list page</div>} />
        </Routes>
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  // R9.5: the builder reads the REPORT object list, not the registry list —
  // task and activity are reportable and have no object_defs row.
  vi.mocked(listReportObjects).mockResolvedValue([
    { slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981' },
    { slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#3B82F6' },
    { slug: 'task', label: 'Task', label_plural: 'Tasks', icon: '✅', color: '#0EA5E9', report_only: true },
    { slug: 'activity', label: 'Activity', label_plural: 'Activities', icon: '📋', color: '#F59E0B', report_only: true },
  ]);
  vi.mocked(listReportFields).mockResolvedValue(dealFields);
  vi.mocked(previewReport).mockResolvedValue(groupsResult);
});

describe('ReportBuilderPage', () => {
  it('prefills from a template and previews it live (debounced)', async () => {
    renderBuilder('/reports/new?template=pipeline-by-stage');

    const nameInput = await screen.findByLabelText('Report name') as HTMLInputElement;
    expect(nameInput.value).toBe('Pipeline by Stage');

    // The debounced preview eventually runs the template's config server-side.
    await waitFor(() => expect(previewReport).toHaveBeenCalled(), { timeout: 3000 });
    const [slug, config] = vi.mocked(previewReport).mock.calls[0];
    expect(slug).toBe('deal');
    expect(config.chart).toBe('bar');
    expect(config.group_by?.field).toBe('stage');
    expect(config.aggregate).toEqual({ fn: 'sum', field: 'value' });

    // The preview result renders (record count caption).
    await waitFor(() => expect(screen.getByText('4 records')).toBeTruthy());
  });

  it('saves a new report and navigates to its page', async () => {
    const saved: Report = {
      id: 'r-123', org_id: 'o1', name: 'Pipeline by Stage', description: '',
      object_slug: 'deal', config: { chart: 'bar', group_by: { field: 'stage' }, aggregate: { fn: 'sum', field: 'value' } },
      visibility: 'private', created_by: 'me',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    };
    vi.mocked(createReport).mockResolvedValue(saved);
    vi.mocked(getReport).mockResolvedValue(saved);

    renderBuilder('/reports/new?template=pipeline-by-stage');
    await screen.findByLabelText('Report name');

    fireEvent.click(screen.getByText('Save report'));
    await waitFor(() => expect(createReport).toHaveBeenCalled());
    const input = vi.mocked(createReport).mock.calls[0][0];
    expect(input.name).toBe('Pipeline by Stage');
    expect(input.object_slug).toBe('deal');
    expect(input.visibility).toBe('private');

    await waitFor(() => expect(screen.getByTestId('loc').textContent).toBe('/reports/r-123'));
  });

  it('refuses to save without a name', async () => {
    renderBuilder('/reports/new');
    await screen.findByLabelText('Report name');

    fireEvent.click(screen.getByText('Save report'));
    expect(createReport).not.toHaveBeenCalled();
  });

  it('loads an existing report and disables editing for non-managers', async () => {
    const theirs: Report = {
      id: 'r-9', org_id: 'o1', name: 'Team revenue', description: '',
      object_slug: 'deal', config: { chart: 'kpi', aggregate: { fn: 'sum', field: 'value' } },
      visibility: 'org', created_by: 'someone-else',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    };
    vi.mocked(getReport).mockResolvedValue(theirs);
    vi.mocked(previewReport).mockResolvedValue({ kind: 'scalar', value: 250000, row_count: 12 });

    renderBuilder('/reports/r-9');

    const nameInput = await screen.findByLabelText('Report name') as HTMLInputElement;
    await waitFor(() => expect(nameInput.value).toBe('Team revenue'));
    // hasCapability mocks to false and the caller isn't the creator: read-only.
    expect(nameInput.disabled).toBe(true);
    expect(screen.queryByText('Save changes')).toBeNull();
    expect(screen.queryByText('Delete')).toBeNull();
    // But the report still runs for them.
    await waitFor(() => expect(screen.getByText('250,000')).toBeTruthy(), { timeout: 3000 });
  });

  // R9.5. The whole point of the phase: tasks and activities are selectable in
  // the builder even though they have no object_defs row, no record page and no
  // nav entry. If the picker ever goes back to the registry list they vanish.
  it('offers the report-only objects and loads their field catalog on select', async () => {
    renderBuilder('/reports/new');

    const picker = await screen.findByLabelText('Report object') as HTMLSelectElement;
    await waitFor(() => expect(picker.options.length).toBe(4));
    const slugs = Array.from(picker.options).map((o) => o.value);
    expect(slugs).toContain('task');
    expect(slugs).toContain('activity');

    vi.mocked(listReportFields).mockResolvedValue([
      { key: 'user_id', label: 'Logged By', type: 'relation' },
      { key: 'type', label: 'Type', type: 'select', options: ['call', 'stage_change'] },
    ]);
    fireEvent.change(picker, { target: { value: 'activity' } });
    await waitFor(() => expect(listReportFields).toHaveBeenCalledWith('activity'));
  });

  // R9.5 made this list OLS-filtered, which means it can legitimately NOT
  // contain the default object. A <select> whose value matches no <option>
  // renders the FIRST option as selected, so without reconciliation a Support
  // rep denied `deal` would see "Contacts" selected while every query on the
  // page still asked for `deal` — the picker lying about what it is showing.
  it('shows the denied object as selected-but-unavailable instead of mislabelling it', async () => {
    vi.mocked(listReportObjects).mockResolvedValue([
      { slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: 'C', color: '#3B82F6' },
    ]);
    renderBuilder('/reports/new');

    const picker = await screen.findByLabelText('Report object') as HTMLSelectElement;
    // The picker must agree with what the page is querying. 'deal' is the
    // initial default and is not in the OLS-filtered list, so it is rendered as
    // a disabled option — NOT silently replaced by the first readable object,
    // which is what a <select> does on its own when its value matches nothing.
    await waitFor(() => expect(screen.getByText(/no access/)).toBeInTheDocument());
    expect(picker.value).toBe('deal');
    expect(within(picker).getByText(/Contacts/)).toBeInTheDocument();
  });

  // An EXISTING report must NOT be silently retargeted: rewriting object_slug
  // and letting Save persist it would rebuild someone's saved report against a
  // different table. Show the truth instead.
  it('an existing report over a denied object shows the denial instead of switching', async () => {
    vi.mocked(listReportObjects).mockResolvedValue([
      { slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: 'C', color: '#3B82F6' },
    ]);
    vi.mocked(getReport).mockResolvedValue({
      id: 'r-42', org_id: 'o1', name: 'Team pipeline', description: '',
      object_slug: 'deal', config: { chart: 'bar', group_by: { field: 'stage' }, aggregate: { fn: 'count' } },
      visibility: 'org', created_by: 'me', access_level: 'manage',
      created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    } as Report);

    renderBuilder('/reports/r-42');

    const picker = await screen.findByLabelText('Report object') as HTMLSelectElement;
    await waitFor(() => expect(picker.value).toBe('deal'));
    expect(await screen.findByText(/read/)).toBeInTheDocument();
  });
});
