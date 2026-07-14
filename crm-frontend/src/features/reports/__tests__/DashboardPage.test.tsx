import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { DashboardWidget, Report, ReportResult } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listDashboardWidgets: vi.fn(),
  addDashboardWidget: vi.fn(),
  updateDashboardWidget: vi.fn(),
  removeDashboardWidget: vi.fn(),
  reorderDashboardWidgets: vi.fn(),
  listReports: vi.fn(),
  runReport: vi.fn(),
  // Read by the setup checklist the dashboard now hosts (U7.5). Its own suite
  // covers it; here it must simply not fire — this user has no setup capability,
  // so every step is gated away and the card renders nothing.
  getWorkspaceMembers: vi.fn(),
  listInvitations: vi.fn(),
  getRoles: vi.fn(),
  getStages: vi.fn(),
  getContacts: vi.fn(),
  updateProfile: vi.fn(),
  createObjectDef: vi.fn(),
  createFieldDef: vi.fn(),
  upsertKBSection: vi.fn(),
}));

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'me' }, activeWorkspace: { org_id: 'o1' }, setUserProfile: vi.fn() }),
  usePermissions: () => ({ can: () => false, canAccess: () => false, loaded: true }),
}));

import {
  listDashboardWidgets, addDashboardWidget, listReports, runReport, reorderDashboardWidgets,
} from '../../../lib/api';
import DashboardPage from '../DashboardPage';

function report(partial: Partial<Report>): Report {
  return {
    id: crypto.randomUUID(), org_id: 'o1', name: 'Untitled', description: '',
    object_slug: 'deal', config: { chart: 'kpi' }, visibility: 'org',
    created_by: 'me', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    ...partial,
  };
}

function widget(rep: Report, partial: Partial<DashboardWidget> = {}): DashboardWidget {
  return {
    id: crypto.randomUUID(), org_id: 'o1', user_id: 'me', report_id: rep.id,
    position: 0, size: 'half', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    report: rep,
    ...partial,
  };
}

const kpiResult: ReportResult = { kind: 'scalar', value: 98765, row_count: 7 };

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/']}>
        <DashboardPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(runReport).mockResolvedValue(kpiResult);
  vi.mocked(listReports).mockResolvedValue([]);
});

describe('DashboardPage', () => {
  it('shows the empty state with a path to Reports', async () => {
    vi.mocked(listDashboardWidgets).mockResolvedValue([]);
    renderPage();
    await waitFor(() => expect(screen.getByText('Your dashboard is empty')).toBeTruthy());
    expect(screen.getByText('Go to Reports')).toBeTruthy();
  });

  it('renders widgets and runs each pinned report', async () => {
    const rep = report({ name: 'Open Pipeline Value' });
    vi.mocked(listDashboardWidgets).mockResolvedValue([widget(rep)]);
    renderPage();

    await waitFor(() => expect(screen.getByText('Open Pipeline Value')).toBeTruthy());
    expect(runReport).toHaveBeenCalledWith(rep.id);
    // The KPI value renders inside the widget card.
    await waitFor(() => expect(screen.getByText('98,765')).toBeTruthy());
  });

  it('pins a report through the add-widget picker', async () => {
    const unpinned = report({ name: 'Deals by Owner' });
    vi.mocked(listDashboardWidgets).mockResolvedValue([]);
    vi.mocked(listReports).mockResolvedValue([unpinned]);
    vi.mocked(addDashboardWidget).mockResolvedValue(widget(unpinned));

    renderPage();
    await waitFor(() => expect(screen.getByText('+ Add widget')).toBeTruthy());
    fireEvent.click(screen.getByText('+ Add widget'));

    const select = await screen.findByLabelText('Report to pin');
    fireEvent.change(select, { target: { value: unpinned.id } });
    fireEvent.click(screen.getByText('Pin to dashboard'));

    await waitFor(() => expect(addDashboardWidget).toHaveBeenCalledWith(unpinned.id));
  });

  it('reorders widgets with the move buttons', async () => {
    const repA = report({ name: 'A' });
    const repB = report({ name: 'B' });
    const wA = widget(repA, { position: 0 });
    const wB = widget(repB, { position: 1 });
    vi.mocked(listDashboardWidgets).mockResolvedValue([wA, wB]);
    vi.mocked(reorderDashboardWidgets).mockResolvedValue(undefined);

    renderPage();
    await waitFor(() => expect(screen.getByText('A')).toBeTruthy());

    // The first widget has no "up"; click its "down" to swap with B.
    fireEvent.click(screen.getAllByLabelText('Move widget down')[0]);
    await waitFor(() => expect(reorderDashboardWidgets).toHaveBeenCalled());
    // mutate() appends a context arg, so assert on the first argument only.
    expect(vi.mocked(reorderDashboardWidgets).mock.calls[0][0]).toEqual([wB.id, wA.id]);
  });
});
