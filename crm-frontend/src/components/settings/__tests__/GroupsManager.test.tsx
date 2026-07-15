import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { UserGroup, WorkspaceMember } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listGroups: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  deleteGroup: vi.fn(),
  addGroupMember: vi.fn(),
  removeGroupMember: vi.fn(),
  getWorkspaceMembers: vi.fn(),
}));

import {
  listGroups, createGroup, addGroupMember, getWorkspaceMembers,
} from '../../../lib/api';
import GroupsManager from '../GroupsManager';

function member(partial: Partial<WorkspaceMember>): WorkspaceMember {
  return {
    user_id: crypto.randomUUID(), email: 'x@y.z', first_name: 'X', last_name: 'Y',
    full_name: 'X Y', role_id: 'role-sales', role: 'sales_rep', status: 'active', ...partial,
  };
}
function group(partial: Partial<UserGroup>): UserGroup {
  return { id: crypto.randomUUID(), name: 'G', description: '', member_count: 0, members: [], created_at: '2026-01-01T00:00:00Z', ...partial };
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.mocked(getWorkspaceMembers).mockResolvedValue([member({ full_name: 'Alice A', email: 'alice@x.com' })]);
});

// Groups + members are react-query caches (U7.3), so renders need a provider.
const renderManager = () => {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <GroupsManager />
    </QueryClientProvider>,
  );
};

describe('GroupsManager', () => {
  it('lists groups with member counts and an empty state', async () => {
    vi.mocked(listGroups).mockResolvedValue([]);
    renderManager();
    await waitFor(() => expect(screen.getByText(/No groups yet/)).toBeTruthy());
  });

  it('creates a group', async () => {
    vi.mocked(listGroups).mockResolvedValue([]);
    vi.mocked(createGroup).mockResolvedValue(group({ name: 'West Region' }));
    renderManager();

    await waitFor(() => expect(screen.getByLabelText('New group name')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('New group name'), { target: { value: 'West Region' } });
    fireEvent.click(screen.getByText('Create group'));
    await waitFor(() => expect(createGroup).toHaveBeenCalledWith('West Region'));
    // The list is re-read rather than patched locally.
    await waitFor(() => expect(listGroups).toHaveBeenCalledTimes(2));
  });

  it('renders a zero-member group whose members arrived as null (Go nil slice)', async () => {
    // The backend marshals a nil Members slice as `"members": null` for a group
    // with no members (user_group_repository.List builds it from a map miss).
    // Unguarded, g.members.map threw during the first render and the app-wide
    // error boundary white-screened /settings/groups on load.
    vi.mocked(listGroups).mockResolvedValue([
      group({ name: 'Empty Team', member_count: 0, members: null as any }),
    ]);
    renderManager();

    await waitFor(() => expect(screen.getByText('Empty Team')).toBeTruthy());
    expect(screen.getByText(/0 members/)).toBeTruthy();
  });

  it('adds a member by toggling the checkbox', async () => {
    const g = group({ name: 'Leadership', member_count: 0, members: [] });
    vi.mocked(listGroups).mockResolvedValue([g]);
    vi.mocked(addGroupMember).mockResolvedValue(undefined);
    renderManager();

    await waitFor(() => expect(screen.getByText('Leadership')).toBeTruthy());
    fireEvent.click(screen.getByText('Manage members'));
    const cb = await screen.findByRole('checkbox');
    fireEvent.click(cb);
    await waitFor(() => expect(addGroupMember).toHaveBeenCalledWith(g.id, expect.any(String)));
  });
});
