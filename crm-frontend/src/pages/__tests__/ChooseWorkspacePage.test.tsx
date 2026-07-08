import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, waitFor } from '@testing-library/react';
import type { Workspace } from '../../lib/api';

// getWorkspaces feeds the full membership list (incl. suspended) for the chooser's
// disabled cards; useAuth provides the active-only seed + switch/logout.
vi.mock('../../lib/api', () => ({
  getWorkspaces: vi.fn(),
}));

const switchWorkspace = vi.fn();
vi.mock('../../lib/auth', () => ({
  useAuth: () => ({
    workspaces: [
      { org_id: 'a', org_name: 'Acme', role: 'admin', status: 'active', member_count: 3 },
    ] as Workspace[],
    switchWorkspace,
    defaultOrgId: null,
    logout: vi.fn(),
  }),
}));

import { getWorkspaces } from '../../lib/api';
import ChooseWorkspacePage from '../ChooseWorkspacePage';

const mockGetWorkspaces = getWorkspaces as unknown as ReturnType<typeof vi.fn>;

describe('ChooseWorkspacePage', () => {
  beforeEach(() => {
    sessionStorage.clear();
    mockGetWorkspaces.mockReset();
  });
  afterEach(cleanup);

  it('renders suspended memberships as disabled cards, separate from active ones', async () => {
    mockGetWorkspaces.mockResolvedValue([
      { org_id: 'a', org_name: 'Acme', role: 'admin', status: 'active', member_count: 3 },
      { org_id: 'b', org_name: 'Globex', role: 'sales_rep', status: 'suspended', member_count: 8 },
    ] as Workspace[]);

    render(<ChooseWorkspacePage />);

    // Active card is clickable (a button); suspended is not.
    expect(screen.getByRole('button', { name: /Acme/ })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText('Globex')).toBeInTheDocument());
    expect(screen.getByText('No longer active')).toBeInTheDocument();
    expect(screen.getByText('Suspended')).toBeInTheDocument();
    // The suspended org is NOT rendered as a clickable button.
    expect(screen.queryByRole('button', { name: /Globex/ })).not.toBeInTheDocument();
  });

  it('shows a "no longer have access" banner naming the lost workspace, then clears the signal', async () => {
    sessionStorage.setItem('lost_workspace_name', 'Initech');
    mockGetWorkspaces.mockResolvedValue([
      { org_id: 'a', org_name: 'Acme', role: 'admin', status: 'active', member_count: 3 },
    ] as Workspace[]);

    render(<ChooseWorkspacePage />);

    expect(screen.getByText(/no longer have access to/i)).toBeInTheDocument();
    expect(screen.getByText('Initech')).toBeInTheDocument();
    // Read-once: the signal is cleared so it doesn't linger to a later visit.
    expect(sessionStorage.getItem('lost_workspace_name')).toBeNull();
  });

  it('falls back to the active-only auth list when the full fetch fails', async () => {
    mockGetWorkspaces.mockRejectedValue(new Error('offline'));

    render(<ChooseWorkspacePage />);

    // The seeded active workspace still renders; no crash, no suspended section.
    expect(screen.getByRole('button', { name: /Acme/ })).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText('No longer active')).not.toBeInTheDocument());
  });
});
