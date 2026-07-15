import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

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

const authState = vi.hoisted(() => ({ canManage: true, canRoles: true }));
vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({
    user: { id: 'me' },
    isOwner: true,
    hasCapability: (code: string) =>
      code === 'members.manage' ? authState.canManage : authState.canRoles,
  }),
  usePermissions: () => ({ can: () => authState.canRoles }),
}));

import { getWorkspaceMembers, listInvitations, getRoleOptions } from '../../../lib/api';
import MembersList from '../MembersList';

// Adversarial member shapes — nulls where the TS type promises strings, plus the
// unusual-but-real statuses (invited/suspended/deleted) local seed data omits.
const NASTY_MEMBERS: any[] = [
  { user_id: 'me', email: 'me@x.com', first_name: 'Me', last_name: 'Owner', full_name: 'Me Owner', role_id: 'r-owner', role: 'owner', status: 'active' },
  // invited, not yet accepted: null names + null email is plausible for a pending row
  { user_id: 'u1', email: null, first_name: null, last_name: null, full_name: null, role_id: 'r-rep', role: null, status: 'invited' },
  { user_id: 'u2', email: 'sus@x.com', first_name: 'Sus', last_name: null, full_name: null, role_id: null, role: 'sales_rep', status: 'suspended' },
  { user_id: 'u3', email: 'del@x.com', first_name: 'Del', last_name: 'Eted', full_name: 'Del Eted', role_id: 'r-rep', role: 'sales_rep', status: 'deleted' },
  { user_id: 'u4', email: 'ok@x.com', first_name: 'O', last_name: 'K', full_name: 'O K', role_id: 'r-rep', role: 'sales_rep', status: 'active' },
];

const NASTY_INVITES: any[] = [
  { id: 'i1', email: 'pending@x.com', role_id: 'r-rep', role: 'sales_rep', status: 'pending', expires_at: '2099-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z' },
  // expired + null expires_at + null role
  { id: 'i2', email: 'expired@x.com', role_id: null, role: null, status: 'expired', expires_at: null, created_at: null },
];

const NASTY_ROLES: any[] = [
  { id: 'r-owner', name: 'owner', description: '', is_system: true, is_owner: true, data_scope: 'all' },
  { id: 'r-rep', name: 'senior_sales_rep', description: '', is_system: false, is_owner: false, data_scope: 'own' },
];

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
  vi.mocked(getWorkspaceMembers).mockResolvedValue(structuredClone(NASTY_MEMBERS));
  vi.mocked(listInvitations).mockResolvedValue(structuredClone(NASTY_INVITES));
  vi.mocked(getRoleOptions).mockResolvedValue(structuredClone(NASTY_ROLES));
});

describe('MembersList — adversarial data', () => {
  it('renders nasty members + invitations without throwing', async () => {
    renderList();
    // Wait for load to settle (owner row appears).
    expect(await screen.findByText('Me Owner')).toBeInTheDocument();
    // Pending-invitations table exercised.
    expect(screen.getByText('pending@x.com')).toBeInTheDocument();
    expect(screen.getByText('expired@x.com')).toBeInTheDocument();
  });

  it('does not throw when searching over a member with a null email', async () => {
    renderList();
    await screen.findByText('Me Owner');
    const search = screen.getByLabelText('Search members');
    // This exercises line 63: m.email.toLowerCase() — unguarded for null email.
    fireEvent.change(search, { target: { value: 'ok' } });
    expect(screen.getByText('O K')).toBeInTheDocument();
  });

  it('does not crash on load when a role/name arrives as a non-string', async () => {
    // A malformed payload delivering a number where prettyRole expects a string
    // used to throw on .split during the initial render (white-screen on load).
    vi.mocked(getWorkspaceMembers).mockResolvedValue([
      { user_id: 'me', email: 'me@x.com', first_name: 'Me', last_name: 'O', full_name: 'Me O', role_id: 'r-owner', role: 'owner', status: 'active' },
      { user_id: 'u9', email: 'x@x.com', first_name: 'X', last_name: 'Y', full_name: 'X Y', role_id: 'r-weird', role: 123 as any, status: 'active' },
    ] as any);
    vi.mocked(getRoleOptions).mockResolvedValue([
      { id: 'r-owner', name: 'owner', description: '', is_system: true, is_owner: true, data_scope: 'all' },
      { id: 'r-weird', name: 456 as any, description: '', is_system: false, is_owner: false, data_scope: 'own' },
    ] as any);
    renderList();
    expect(await screen.findByText('Me O')).toBeInTheDocument();
    expect(screen.getByText('X Y')).toBeInTheDocument();
  });
});
