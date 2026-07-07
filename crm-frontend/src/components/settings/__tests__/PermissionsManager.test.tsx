import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';

// Mock the API so the grid is exercised without a backend. This is the admin
// surface that configures the OLS RecordService enforces (P5a).
vi.mock('../../../lib/api', () => ({
  getPermissionGrid: vi.fn(),
  setObjectPermission: vi.fn(),
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

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(getPermissionGrid).mockResolvedValue(structuredClone(GRID));
  vi.mocked(setObjectPermission).mockResolvedValue(undefined);
});

describe('PermissionsManager — role × object grid', () => {
  it('renders objects and reflects the matrix for the default (non-owner) role', async () => {
    render(<PermissionsManager />);

    // Defaults to the first editable role (sales_rep), not owner.
    expect(await screen.findByText('Deal')).toBeInTheDocument();
    // "Project" now appears both in the table row and in the no-access nudge
    // banner (sales_rep has no read grant for it), so match at least one.
    expect(screen.getAllByText('Project').length).toBeGreaterThan(0);

    // Existing grants are reflected; an object with no row is default-deny.
    expect((screen.getByLabelText('sales_rep Read Deal') as HTMLInputElement).checked).toBe(true);
    expect((screen.getByLabelText('sales_rep Delete Deal') as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText('sales_rep Read Project') as HTMLInputElement).checked).toBe(false);
  });

  it('toggling a checkbox saves the merged access bits', async () => {
    render(<PermissionsManager />);
    const deleteDeal = await screen.findByLabelText('sales_rep Delete Deal');

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
    render(<PermissionsManager />);
    await screen.findByText('Deal');

    fireEvent.click(screen.getByRole('tab', { name: /owner/ }));

    const ownerRead = await screen.findByLabelText('owner Read Deal');
    expect((ownerRead as HTMLInputElement).checked).toBe(true);
    expect((ownerRead as HTMLInputElement).disabled).toBe(true);
  });
});
