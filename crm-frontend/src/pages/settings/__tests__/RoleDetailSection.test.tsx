import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import type { RoleAccess } from '../../../lib/api';

// RoleDetailSection is the single-pivot role page (U3.1/U3.2): one merged
// payload (identity + capabilities + effective object/field access + layouts).
// The API and auth are mocked; lib/roles is real and imports CAPABILITY_LABELS
// from the api module, so the mock must export it.
vi.mock('../../../lib/api', () => ({
  getRoleAccess: vi.fn(),
  getRolesCatalog: vi.fn(),
  setRoleCapabilities: vi.fn(),
  updateRole: vi.fn(),
  ALL_CAPABILITIES: ['roles.manage', 'members.manage'],
  CAPABILITY_LABELS: { 'roles.manage': 'Manage roles', 'members.manage': 'Manage members' },
}));

const hasCapability = vi.fn((cap: string) => cap === 'members.manage');
vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ hasCapability }),
}));

import { getRoleAccess, getRolesCatalog, setRoleCapabilities, updateRole } from '../../../lib/api';
import RoleDetailSection from '../RoleDetailSection';

const CATALOG = {
  capabilities: [
    { code: 'roles.manage', label: 'Manage roles', description: 'Edit roles and access grids', group: 'Administration', sensitive: true },
    { code: 'members.manage', label: 'Manage members', description: 'Suspend and remove members', group: 'Administration', sensitive: false },
  ],
  groups: ['Administration'],
};

const SALES_ACCESS: RoleAccess = {
  role: {
    id: 'r1',
    name: 'sales_rep',
    description: 'Works deals',
    is_system: false,
    is_owner: false,
    data_scope: 'own',
    capabilities: ['members.manage'],
    member_count: 2,
  },
  objects: [
    {
      slug: 'deal', label: 'Deal', icon: '💰', is_system: true,
      read: true, create: true, edit: true, delete: false,
      restricted_fields: [{ key: 'amount', label: 'Amount', level: 'hidden' }],
    },
    {
      slug: 'contact', label: 'Contact', icon: '👤', is_system: true,
      read: true, create: false, edit: false, delete: false,
      restricted_fields: [{ key: 'email', label: 'Email', level: 'read' }],
    },
  ],
  layouts: [{ object_slug: 'deal', layout_id: 'l1', layout_name: 'Sales view' }],
};

const renderAt = (id = 'r1') =>
  render(
    <MemoryRouter initialEntries={[`/settings/roles/${id}`]}>
      <Routes>
        <Route path="/settings/roles/:id" element={<RoleDetailSection />} />
      </Routes>
    </MemoryRouter>,
  );

beforeEach(() => {
  vi.mocked(getRoleAccess).mockResolvedValue(SALES_ACCESS);
  vi.mocked(getRolesCatalog).mockResolvedValue(CATALOG);
  vi.mocked(setRoleCapabilities).mockReset().mockResolvedValue(undefined);
  vi.mocked(updateRole).mockReset().mockResolvedValue(undefined);
  hasCapability.mockClear();
});
afterEach(cleanup);

describe('RoleDetailSection — one role, one pivot', () => {
  it('renders identity, member-count link, effective access bits, field limits and layout', async () => {
    renderAt();

    expect(await screen.findByRole('heading', { name: 'Sales Rep' })).toBeInTheDocument();
    expect(screen.getByText('Works deals')).toBeInTheDocument();

    // Member count cross-links into Members pre-filtered to this role (U3.3).
    const membersLink = screen.getByRole('link', { name: /2 members/ });
    expect(membersLink).toHaveAttribute('href', '/settings/members?role=r1');

    // OLS bits render as accessible check/deny cells.
    expect(screen.getByLabelText('Can read Deal')).toBeInTheDocument();
    expect(screen.getByLabelText('Cannot delete Deal')).toBeInTheDocument();
    expect(screen.getByLabelText('Cannot edit Contact')).toBeInTheDocument();

    // FLS restrictions joined to labels, in plain words.
    expect(screen.getByText('Amount · Hidden')).toBeInTheDocument();
    expect(screen.getByText('Email · Read-only')).toBeInTheDocument();

    // Layout routing column: assigned name for deal, Default for contact.
    expect(screen.getByText('Sales view')).toBeInTheDocument();
    expect(screen.getByText('Default')).toBeInTheDocument();

    // Grid cross-links preselect this role where the target supports ?role=;
    // layouts are assigned on the ObjectsManager's Layouts tab.
    expect(screen.getByRole('link', { name: 'Edit object access' })).toHaveAttribute('href', '/settings/object-access?role=r1');
    expect(screen.getByRole('link', { name: 'Edit field access' })).toHaveAttribute('href', '/settings/field-access?role=r1');
    expect(screen.getByRole('link', { name: 'Edit layouts' })).toHaveAttribute('href', '/settings/objects');

    // Capability descriptions are visible text, not tooltips (U3.5), and the
    // sensitive flag is a chip, not an emoji.
    expect(screen.getByText('Edit roles and access grids')).toBeInTheDocument();
    expect(screen.getByText('Sensitive')).toBeInTheDocument();
  });

  it('toggling a capability saves the updated set', async () => {
    renderAt();
    await screen.findByRole('heading', { name: 'Sales Rep' });

    // Grant roles.manage (currently unchecked).
    const checkbox = screen.getByRole('checkbox', { name: /Manage roles/ });
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);

    await waitFor(() =>
      expect(setRoleCapabilities).toHaveBeenCalledWith('r1', ['members.manage', 'roles.manage']),
    );
  });

  it('changing the data scope persists via updateRole', async () => {
    renderAt();
    await screen.findByRole('heading', { name: 'Sales Rep' });

    const scope = screen.getByLabelText('Which records members with this role can see') as HTMLSelectElement;
    expect(scope.value).toBe('own');
    fireEvent.change(scope, { target: { value: 'all' } });

    await waitFor(() => expect(updateRole).toHaveBeenCalledWith('r1', { data_scope: 'all' }));
    expect((screen.getByLabelText('Which records members with this role can see') as HTMLSelectElement).value).toBe('all');
  });

  it('removing roles.manage asks for confirmation, and cancel aborts the save', async () => {
    vi.mocked(getRoleAccess).mockResolvedValue({
      ...SALES_ACCESS,
      role: { ...SALES_ACCESS.role, capabilities: ['members.manage', 'roles.manage'] },
    });
    renderAt();
    await screen.findByRole('heading', { name: 'Sales Rep' });

    // Unchecking roles.manage names the blast radius before saving anything.
    fireEvent.click(screen.getByRole('checkbox', { name: /Manage roles/ }));

    const dialog = await screen.findByRole('dialog');
    expect(dialog.textContent).toContain('Remove permission management?');
    expect(dialog.textContent).toContain('2 members holding the "Sales Rep" role');
    expect(setRoleCapabilities).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(setRoleCapabilities).not.toHaveBeenCalled();
    expect(screen.getByRole('checkbox', { name: /Manage roles/ })).toBeChecked();
  });

  it('renames a custom role in place and saves name + description', async () => {
    renderAt();
    await screen.findByRole('heading', { name: 'Sales Rep' });

    fireEvent.click(screen.getByRole('button', { name: 'Edit role name and description' }));
    fireEvent.change(screen.getByLabelText('Role name'), { target: { value: 'Account Exec' } });
    fireEvent.change(screen.getByLabelText('Role description'), { target: { value: 'Owns accounts' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));

    await waitFor(() =>
      expect(updateRole).toHaveBeenCalledWith('r1', { name: 'Account Exec', description: 'Owns accounts' }),
    );
    // Back to display mode with the new identity, no refetch needed.
    expect(await screen.findByRole('heading', { name: 'Account Exec' })).toBeInTheDocument();
    expect(screen.getByText('Owns accounts')).toBeInTheDocument();
  });

  it('renders a built-in non-owner role read-only with the duplicate-to-customize copy', async () => {
    vi.mocked(getRoleAccess).mockResolvedValue({
      ...SALES_ACCESS,
      role: { ...SALES_ACCESS.role, id: 'r2', name: 'viewer', is_system: true, data_scope: 'all' },
    });
    renderAt('r2');
    await screen.findByRole('heading', { name: 'Viewer' });

    expect(screen.getByText(/duplicate this role from the roles list/)).toBeInTheDocument();
    // No rename affordance, no scope select (static text instead), disabled checkboxes.
    expect(screen.queryByRole('button', { name: 'Edit role name and description' })).toBeNull();
    expect(screen.queryByLabelText('Which records members with this role can see')).toBeNull();
    // The scope summary is tri-state since U6.1 (own / team / all).
    expect(screen.getByText('All records in the workspace.')).toBeInTheDocument();
    expect(screen.getByRole('checkbox', { name: /Manage roles/ })).toBeDisabled();
    expect(screen.getByRole('checkbox', { name: /Manage members/ })).toBeDisabled();
  });

  it('shows the empty-access warning when a role can read nothing', async () => {
    vi.mocked(getRoleAccess).mockResolvedValue({
      ...SALES_ACCESS,
      objects: SALES_ACCESS.objects.map((o) => ({ ...o, read: false, create: false, edit: false, delete: false })),
    });
    renderAt();
    await screen.findByRole('heading', { name: 'Sales Rep' });

    const banner = screen.getByText(/can't see any objects yet/).closest('div')!;
    expect(within(banner).getByRole('link', { name: 'Grant access' })).toHaveAttribute(
      'href',
      '/settings/object-access?role=r1',
    );
  });

  it('renders the owner role as locked full access with no edit affordances', async () => {
    vi.mocked(getRoleAccess).mockResolvedValue({
      role: {
        id: 'r0', name: 'owner', description: '', is_system: true, is_owner: true,
        data_scope: 'all', capabilities: ['roles.manage', 'members.manage'], member_count: 1,
      },
      objects: [{
        slug: 'deal', label: 'Deal', icon: '💰', is_system: true,
        read: true, create: true, edit: true, delete: true, restricted_fields: [],
      }],
      layouts: [],
    });
    renderAt('r0');
    await screen.findByRole('heading', { name: 'Owner' });

    expect(screen.getByText('Full access')).toBeInTheDocument();
    // No zero-access warning, no editable scope select, checkboxes disabled+checked.
    expect(screen.queryByText(/can't see any objects yet/)).toBeNull();
    expect(screen.queryByLabelText('Which records members with this role can see')).toBeNull();
    const checkbox = screen.getByRole('checkbox', { name: /Manage roles/ });
    expect(checkbox).toBeChecked();
    expect(checkbox).toBeDisabled();
  });

  it('shows the load error with a way back when the role does not exist', async () => {
    vi.mocked(getRoleAccess).mockRejectedValue(new Error('role not found'));
    renderAt('nope');

    expect(await screen.findByText('role not found')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /All roles/ })).toHaveAttribute('href', '/settings/roles');
  });
});
