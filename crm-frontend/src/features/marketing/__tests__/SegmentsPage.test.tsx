import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import SegmentsPage from '../SegmentsPage';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

vi.mock('../segmentsQueries', () => ({
  useSegments: vi.fn(() => ({ data: [], isLoading: false })),
  useCreateSegment: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useDeleteSegment: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
  useSegmentCount: vi.fn(() => ({ data: 0, isLoading: false, isError: false })),
}));

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (code: string) => (code === 'marketing.manage' ? can : false),
    loaded,
  });
}

const renderPage = () => render(<MemoryRouter><SegmentsPage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('SegmentsPage gating', () => {
  it('shows a spinner (not the denied panel) while permissions load', () => {
    setPerms(false, false);
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /new audience/i })).not.toBeInTheDocument();
  });

  it('shows the access-denied panel without the capability', () => {
    setPerms(false, true);
    renderPage();
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /new audience/i })).not.toBeInTheDocument();
  });

  it('renders the audiences surface with the capability', () => {
    setPerms(true, true);
    renderPage();
    expect(screen.getByRole('heading', { name: 'Audiences' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /new audience/i })).toBeInTheDocument();
    expect(screen.getByText('No audiences yet')).toBeInTheDocument();
  });
});
