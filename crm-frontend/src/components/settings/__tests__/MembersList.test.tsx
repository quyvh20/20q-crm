import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

// Mock the API so the table is exercised without a backend.
// CAPABILITY_LABELS is included because lib/roles (real, unmocked) imports it.
vi.mock('../../../lib/api', () => ({
  getWorkspaceMembers: vi.fn(),
  updateMemberRole: vi.fn(),
  removeMember: vi.fn(),
  suspendMember: vi.fn(),
  reinstateMember: vi.fn(),
  transferOwnership: vi.fn(),
  sendMemberResetLink: vi.fn(),
  listInvitations: vi.fn(),
  resendInvitation: vi.fn(),
  revokeInvitation: vi.fn(),
  getRoleOptions: vi.fn(),
  ReassignmentRequiredError: class extends Error {},
  CAPABILITY_LABELS: {},
}));

// Toggleable capabilities so individual tests can flip members.manage off and
// exercise the read-only badge path.
const authState = vi.hoisted(() => ({ canManage: true, canRoles: true }));
vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({
    user: { id: 'me' },
    isOwner: false,
    hasCapability: (code: string) =>
      code === 'members.manage' ? authState.canManage : authState.canRoles,
  }),
  usePermissions: () => ({ can: () => authState.canRoles }),
}));

import { getWorkspaceMembers, listInvitations, getRoleOptions } from '../../../lib/api';
import type { WorkspaceMember, RoleOption } from '../../../lib/api';
import MembersList from '../MembersList';

const MEMBERS: WorkspaceMember[] = [
  { user_id: 'u-admin', email: 'ada@x.com', first_name: 'Ada', last_name: 'Admin', full_name: 'Ada Admin', role_id: 'r-admin', role: 'admin', status: 'active' },
  { user_id: 'u-rep', email: 'sam@x.com', first_name: 'Sam', last_name: 'Rep', full_name: 'Sam Rep', role_id: 'r-senior', role: 'senior_sales_rep', status: 'active' },
];

const ROLES: RoleOption[] = [
  { id: 'r-admin', name: 'admin', description: 'Full admin', is_system: true, is_owner: false, data_scope: 'all' },
  { id: 'r-senior', name: 'senior_sales_rep', description: '', is_system: false, is_owner: false, data_scope: 'own' },
];

// The role filter lives in the URL (?role=<id>), so the list needs a router.
const renderList = (path = '/settings/members') =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <MembersList />
    </MemoryRouter>,
  );

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  authState.canManage = true;
  authState.canRoles = true;
  vi.mocked(getWorkspaceMembers).mockResolvedValue(structuredClone(MEMBERS));
  vi.mocked(listInvitations).mockResolvedValue([]);
  vi.mocked(getRoleOptions).mockResolvedValue(structuredClone(ROLES));
});

describe('MembersList — role filter & badges', () => {
  it('?role= URL param pre-filters the table to that role', async () => {
    renderList('/settings/members?role=r-senior');

    expect(await screen.findByText('Sam Rep')).toBeInTheDocument();
    expect(screen.queryByText('Ada Admin')).toBeNull();
  });

  it('shows an escapable "Filtered by role" chip when ?role= is set but options failed', async () => {
    // The select hides without options; the chip keeps the filter visible.
    vi.mocked(getRoleOptions).mockRejectedValue(new Error('boom'));
    renderList('/settings/members?role=r-senior');

    expect(await screen.findByText('Filtered by role')).toBeInTheDocument();
    expect(screen.queryByLabelText('Filter by role')).toBeNull();
    expect(screen.queryByText('Ada Admin')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Clear role filter' }));

    expect(screen.queryByText('Filtered by role')).toBeNull();
    expect(await screen.findByText('Ada Admin')).toBeInTheDocument();
    expect(screen.getByText('Sam Rep')).toBeInTheDocument();
  });

  it('title-cases multi-underscore roles on the read-only badge', async () => {
    // Without members.manage the row renders the badge instead of the select.
    authState.canManage = false;
    renderList();

    // Scoped to the member's row — the role-filter options carry the same text.
    // replace('_', ' ') would have shown "senior sales_rep".
    const row = (await screen.findByText('Sam Rep')).closest('tr')!;
    expect(within(row).getByText('Senior Sales Rep')).toBeInTheDocument();
  });
});
