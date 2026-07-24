import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SenderProfilePage } from '../SenderProfilePage';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (c: string) => (c === 'marketing.manage' ? can : false),
    loaded,
  });
}

const renderPage = () => render(<MemoryRouter><SenderProfilePage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('SenderProfilePage gating', () => {
  it('shows a spinner while permissions load (no flash-denied, no query fired)', () => {
    setPerms(false, false);
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /save sender profile/i })).not.toBeInTheDocument();
  });

  it('denies without marketing.manage', () => {
    setPerms(false, true);
    renderPage();
    expect(screen.queryByRole('button', { name: /save sender profile/i })).not.toBeInTheDocument();
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
  });
});
