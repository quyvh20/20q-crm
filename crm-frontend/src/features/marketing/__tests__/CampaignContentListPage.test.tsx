import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { CampaignContentListPage } from '../CampaignContentListPage';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

vi.mock('../contentQueries', () => ({
  useContentList: vi.fn(() => ({ data: [], isLoading: false, isError: false })),
  useRemoveContent: () => ({ mutate: vi.fn(), isPending: false }),
}));
import { useContentList } from '../contentQueries';

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (c: string) => (c === 'marketing.manage' ? can : false),
    loaded,
  });
}

const renderPage = () => render(<MemoryRouter><CampaignContentListPage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('CampaignContentListPage gating', () => {
  it('shows a spinner while permissions load (no flash-denied)', () => {
    setPerms(false, false);
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByText('Email content')).not.toBeInTheDocument();
  });

  it('denies without marketing.manage (with the friendly label)', () => {
    setPerms(false, true);
    renderPage();
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
  });

  it('renders the list surface with the capability', () => {
    setPerms(true, true);
    (useContentList as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ data: [], isLoading: false, isError: false });
    renderPage();
    expect(screen.getByText('Email content')).toBeInTheDocument();
    expect(screen.getAllByText('New content').length).toBeGreaterThan(0); // header + empty-state actions
    expect(screen.getByText('No email content yet')).toBeInTheDocument();
  });

  it('shows an error state (not empty) when the list fetch fails', () => {
    setPerms(true, true);
    (useContentList as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ data: undefined, isLoading: false, isError: true });
    renderPage();
    expect(screen.getByText(/Couldn’t load email content/i)).toBeInTheDocument();
  });
});
