import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import CampaignsListPage from '../CampaignsListPage';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

vi.mock('../campaignsQueries', () => ({
  useCampaigns: vi.fn(() => ({ data: [], isLoading: false })),
  useCreateCampaign: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useDeleteCampaign: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
}));

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (code: string) => (code === 'marketing.manage' ? can : false),
    loaded,
  });
}

const renderPage = () => render(<MemoryRouter><CampaignsListPage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('CampaignsListPage gating', () => {
  it('shows a spinner (not denied) while permissions load', () => {
    setPerms(false, false);
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /new campaign/i })).not.toBeInTheDocument();
  });

  it('shows the access-denied panel without the capability', () => {
    setPerms(false, true);
    renderPage();
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /new campaign/i })).not.toBeInTheDocument();
  });

  it('renders the campaigns surface with the capability', () => {
    setPerms(true, true);
    renderPage();
    expect(screen.getByRole('heading', { name: 'Campaigns' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /new campaign/i })).toBeInTheDocument();
    expect(screen.getByText('No campaigns yet')).toBeInTheDocument();
  });
});
