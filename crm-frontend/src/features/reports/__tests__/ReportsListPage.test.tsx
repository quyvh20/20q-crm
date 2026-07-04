import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Report } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listReports: vi.fn(),
}));
vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ user: { id: 'me' }, hasCapability: () => false }),
}));

import { listReports } from '../../../lib/api';
import ReportsListPage from '../ReportsListPage';

function report(partial: Partial<Report>): Report {
  return {
    id: crypto.randomUUID(), org_id: 'o1', name: 'Untitled', description: '',
    object_slug: 'deal', config: { chart: 'bar' }, visibility: 'private',
    created_by: 'me', created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z',
    ...partial,
  };
}

function LocationProbe() {
  const loc = useLocation();
  return <div data-testid="loc">{loc.pathname + loc.search}</div>;
}

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={['/reports']}>
        <ReportsListPage />
        <LocationProbe />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ReportsListPage', () => {
  it('splits reports into mine vs shared and shows visibility badges', async () => {
    vi.mocked(listReports).mockResolvedValue([
      report({ name: 'My pipeline', created_by: 'me', visibility: 'private' }),
      report({ name: 'Team revenue', created_by: 'someone-else', visibility: 'org' }),
    ]);
    renderPage();

    await waitFor(() => expect(screen.getByText('My pipeline')).toBeTruthy());
    expect(screen.getByText('My reports')).toBeTruthy();
    expect(screen.getByText('Shared with the workspace')).toBeTruthy();
    expect(screen.getByText('Team revenue')).toBeTruthy();
    expect(screen.getByText('Shared')).toBeTruthy();
    expect(screen.getByText('Private')).toBeTruthy();
  });

  it('shows the template gallery and navigates with the template id', async () => {
    vi.mocked(listReports).mockResolvedValue([]);
    renderPage();

    await waitFor(() => expect(screen.getByText(/No reports yet/)).toBeTruthy());
    fireEvent.click(screen.getByText('Pipeline by Stage'));
    expect(screen.getByTestId('loc').textContent).toBe('/reports/new?template=pipeline-by-stage');
  });

  it('navigates to the blank builder from the New report button', async () => {
    vi.mocked(listReports).mockResolvedValue([]);
    renderPage();

    await waitFor(() => expect(screen.getByText('+ New report')).toBeTruthy());
    fireEvent.click(screen.getByText('+ New report'));
    expect(screen.getByTestId('loc').textContent).toBe('/reports/new');
  });
});
