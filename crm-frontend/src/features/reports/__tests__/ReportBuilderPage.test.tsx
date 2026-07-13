import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Report, ReportFieldDescriptor, ReportResult } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listRegistryObjects: vi.fn(),
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
  listRegistryObjects, listReportFields, previewReport, createReport, getReport,
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
  vi.mocked(listRegistryObjects).mockResolvedValue([
    { slug: 'deal', label: 'Deal', label_plural: 'Deals', icon: '💰', color: '#10B981', is_system: true, field_count: 5, searchable: false },
    { slug: 'contact', label: 'Contact', label_plural: 'Contacts', icon: '👤', color: '#3B82F6', is_system: true, field_count: 5, searchable: true },
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
});
