import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SuppressionListPage } from '../SuppressionListPage';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

vi.mock('../queries', () => ({
  useSuppressions: vi.fn(() => ({ data: { data: [], meta: { total: 0 } }, isLoading: false })),
  useAddSuppression: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useRemoveSuppression: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
}));

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (code: string) => (code === 'marketing.manage' ? can : false),
    loaded,
  });
}

const renderPage = () => render(<MemoryRouter><SuppressionListPage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('SuppressionListPage gating', () => {
  it('shows a spinner (not the denied panel) while permissions are still loading', () => {
    setPerms(false, false); // capability gates fail closed — must not flash denied
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByText('Email address')).not.toBeInTheDocument();
  });

  it('shows the access-denied panel (with the marketing.manage label) without the capability', () => {
    setPerms(false, true);
    renderPage();
    // The friendly label comes from CAPABILITY_LABELS — proves marketing.manage is wired there.
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
    expect(screen.queryByText('Email address')).not.toBeInTheDocument();
  });

  it('renders the admin surface with the capability', () => {
    setPerms(true, true);
    renderPage();
    expect(screen.getByText('Suppression list')).toBeInTheDocument();
    expect(screen.getByLabelText('Email address')).toBeInTheDocument();
    expect(screen.getByText('No suppressions yet')).toBeInTheDocument();
  });
});
