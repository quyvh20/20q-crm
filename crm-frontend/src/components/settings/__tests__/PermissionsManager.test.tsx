import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';

// Mock the API so the grid is exercised without a backend. This is the admin
// surface that configures the OLS RecordService enforces (P5a).
// CAPABILITY_LABELS is included because lib/roles (real, unmocked) imports it.
vi.mock('../../../lib/api', () => ({
  getPermissionGrid: vi.fn(),
  setObjectPermission: vi.fn(),
  CAPABILITY_LABELS: {},
}));

import { getPermissionGrid, setObjectPermission } from '../../../lib/api';
import PermissionsManager from '../PermissionsManager';

const GRID = {
  roles: [
    { id: 'r-owner', name: 'owner', is_system: true, is_owner: true },
    { id: 'r-sales', name: 'sales_rep', is_system: true, is_owner: false },
  ],
  objects: [
    { slug: 'deal', label: 'Deal', icon: '💰', is_system: true },
    { slug: 'project', label: 'Project', icon: '📦', is_system: false },
  ],
  matrix: [
    { role_id: 'r-sales', object_slug: 'deal', read: true, create: true, edit: true, delete: false },
  ],
};

// The component reads/writes ?role= via useSearchParams, so it needs a router.
const renderPage = () =>
  render(
    <MemoryRouter initialEntries={['/settings/object-access']}>
      <PermissionsManager />
    </MemoryRouter>,
  );

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  localStorage.clear(); // zero-access dismissals persist per browser
  vi.mocked(getPermissionGrid).mockResolvedValue(structuredClone(GRID));
  vi.mocked(setObjectPermission).mockResolvedValue(undefined);
});

describe('PermissionsManager — role × object grid', () => {
  it('renders objects and reflects the matrix for the default (non-owner) role', async () => {
    renderPage();

    // Defaults to the first editable role (sales_rep), not owner.
    expect(await screen.findByText('Deal')).toBeInTheDocument();
    expect(screen.getAllByText('Project').length).toBeGreaterThan(0);

    // Existing grants are reflected; an object with no row means no access.
    expect((screen.getByLabelText('Sales Rep Read Deal') as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText('Sales Rep Delete Deal') as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText('Sales Rep Read Project') as HTMLInputElement).checked).toBe(false);
  });

  it('toggling a checkbox saves the merged access bits', async () => {
    renderPage();
    const deleteDeal = await screen.findByLabelText('Sales Rep Delete Deal');

    fireEvent.click(deleteDeal);

    await waitFor(() => expect(setObjectPermission).toHaveBeenCalledTimes(1));
    expect(setObjectPermission).toHaveBeenCalledWith({
      role_id: 'r-sales',
      object_slug: 'deal',
      can_read: true,
      can_create: true,
      can_edit: true,
      can_delete: true, // the toggled bit, merged onto the existing grants
    });
  });

  it('locks the owner row — checked and not editable (owner bypasses OLS)', async () => {
    renderPage();
    await screen.findByText('Deal');

    fireEvent.click(screen.getByRole('tab', { name: /Owner/ }));

    const ownerRead = await screen.findByLabelText('Owner Read Deal');
    expect((ownerRead as HTMLInputElement).checked).toBe(true);
    expect((ownerRead as HTMLInputElement).disabled).toBe(true);
  });

  it('names who cannot see what in the zero-access banner, and Dismiss hides that pair', async () => {
    renderPage();
    await screen.findByText('Deal');

    // sales_rep has no read grant for Project → the banner says so by name
    // (the <strong>Sales Rep</strong> inside the warning line, not the tab).
    const line = screen.getByText(/has no access to/);
    expect(within(line).getByText('Sales Rep')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Review Sales Rep access' })).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss Sales Rep access warning' }));
    expect(screen.queryByText(/has no access to/)).toBeNull();
    // The dismissal is remembered per (role, object) pair.
    expect(JSON.parse(localStorage.getItem('zeroAccessDismissed') || '[]')).toContain('r-sales:project');
  });

  it('deep link ?role= preselects that role tab', async () => {
    render(
      <MemoryRouter initialEntries={['/settings/object-access?role=r-owner']}>
        <PermissionsManager />
      </MemoryRouter>,
    );
    await screen.findByText('Deal');

    // The preselect runs in an effect after the grid lands — wait for it.
    await waitFor(() =>
      expect(screen.getByRole('tab', { name: /Owner/ })).toHaveAttribute('aria-selected', 'true'),
    );
  });

  it('Review on a zero-access item jumps to that role tab and writes ?role=', async () => {
    // Probe the router state so the deep-link write is observable.
    const LocationProbe = () => <div data-testid="location-search">{useLocation().search}</div>;
    // Start on the owner tab so the jump actually changes the selection.
    render(
      <MemoryRouter initialEntries={['/settings/object-access?role=r-owner']}>
        <PermissionsManager />
        <LocationProbe />
      </MemoryRouter>,
    );
    await screen.findByText('Deal');
    await waitFor(() =>
      expect(screen.getByRole('tab', { name: /Owner/ })).toHaveAttribute('aria-selected', 'true'),
    );

    fireEvent.click(screen.getByRole('button', { name: 'Review Sales Rep access' }));

    expect(screen.getByRole('tab', { name: 'Sales Rep' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('location-search').textContent).toContain('role=r-sales');
  });
});
