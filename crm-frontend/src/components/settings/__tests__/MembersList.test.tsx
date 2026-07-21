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
  // A faithful stand-in, not a bare `class extends Error {}`: the component reads
  // `.owned` and `.routingSources` off this, so a stub without them would make the
  // reassignment dialog untestable while still satisfying `instanceof`.
  ReassignmentRequiredError: class extends Error {
    code = 'REASSIGNMENT_REQUIRED';
    owned: { contacts: number; deals: number; custom: number };
    routingSources: string[];
    constructor(message: string, owned: any, routingSources: string[] = []) {
      super(message);
      this.name = 'ReassignmentRequiredError';
      this.owned = owned;
      this.routingSources = routingSources;
    }
  },
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

import { getWorkspaceMembers, listInvitations, getRoleOptions, removeMember, ReassignmentRequiredError } from '../../../lib/api';
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

// Offboarding a member clears every lead source that was routing NEW leads to them.
// That is invisible in the data — their records get a new owner, their lead pipes get
// none — so the disclosure is the whole feature. Both paths are covered because the
// commonest case is the one WITHOUT the 409: a rep who is on a rotation but owns no
// records yet never triggers the reassignment dialog at all.
describe('MembersList — lead-routing disclosure on removal', () => {
  it('warns in the reassignment dialog which lead sources the member owns', async () => {
    vi.mocked(removeMember).mockRejectedValueOnce(
      new (ReassignmentRequiredError as any)(
        'still owns records',
        { contacts: 3, deals: 2, custom: 1 },
        ['Website form', 'Google Ads'],
      ),
    );
    renderList();

    const row = (await screen.findByText('Sam Rep')).closest('tr')!;
    fireEvent.click(within(row).getByTitle('Remove Member'));
    // The row button confirms first; the 409 comes back from that attempt.
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));

    // Radix renders in a portal, so query the screen rather than the container.
    expect(await screen.findByText(/Website form, Google Ads/)).toBeInTheDocument();
    expect(screen.getByText(/capture leads with no owner/)).toBeInTheDocument();
  });

  it('includes custom-object records in the dialog counts', async () => {
    // The parser dropped `custom` since U6.3, so the dialog under-reported the impact
    // in the very place the admin decides what happens to the data.
    vi.mocked(removeMember).mockRejectedValueOnce(
      new (ReassignmentRequiredError as any)(
        'still owns records',
        { contacts: 3, deals: 2, custom: 4 },
        [],
      ),
    );
    renderList();

    const row = (await screen.findByText('Sam Rep')).closest('tr')!;
    fireEvent.click(within(row).getByTitle('Remove Member'));
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));

    expect(await screen.findByText(/3 contacts, 2 deals and 4 other records/)).toBeInTheDocument();
  });

  it('warns AFTER a clean removal when the member owned no records but did own lead sources', async () => {
    // No 409 at all — this member owns nothing, so before this the removal was
    // completely silent while their lead sources went ownerless.
    vi.mocked(removeMember).mockResolvedValueOnce({
      routing_sources_cleared: ['Website form'],
    });
    renderList();

    const row = (await screen.findByText('Sam Rep')).closest('tr')!;
    fireEvent.click(within(row).getByTitle('Remove Member'));
    // The plain remove goes through the confirm dialog first.
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));

    const notice = await screen.findByText(/capture leads with no owner/);
    expect(notice.textContent).toContain('Website form');
  });

  it('says nothing about routing when the member owned no lead sources', async () => {
    vi.mocked(removeMember).mockResolvedValueOnce({ routing_sources_cleared: [] });
    renderList();

    const row = (await screen.findByText('Sam Rep')).closest('tr')!;
    fireEvent.click(within(row).getByTitle('Remove Member'));
    fireEvent.click(await screen.findByRole('button', { name: 'Remove' }));

    // The control half: a banner that appears on every removal is noise, and noise
    // is how the real warning stops being read.
    await screen.findByText('Ada Admin');
    expect(screen.queryByText(/capture leads with no owner/)).toBeNull();
  });
});
