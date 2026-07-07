import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import type { UserGroup, WorkspaceMember } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listGroups: vi.fn(),
  createGroup: vi.fn(),
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

describe('GroupsManager', () => {
  it('lists groups with member counts and an empty state', async () => {
    vi.mocked(listGroups).mockResolvedValue([]);
    render(<GroupsManager />);
    await waitFor(() => expect(screen.getByText(/No groups yet/)).toBeTruthy());
  });

  it('creates a group', async () => {
    vi.mocked(listGroups).mockResolvedValue([]);
    vi.mocked(createGroup).mockResolvedValue(group({ name: 'West Region' }));
    render(<GroupsManager />);

    await waitFor(() => expect(screen.getByLabelText('New group name')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('New group name'), { target: { value: 'West Region' } });
    fireEvent.click(screen.getByText('Create group'));
    await waitFor(() => expect(createGroup).toHaveBeenCalledWith('West Region'));
  });

  it('adds a member by toggling the checkbox', async () => {
    const g = group({ name: 'Leadership', member_count: 0, members: [] });
    vi.mocked(listGroups).mockResolvedValue([g]);
    vi.mocked(addGroupMember).mockResolvedValue(undefined);
    render(<GroupsManager />);

    await waitFor(() => expect(screen.getByText('Leadership')).toBeTruthy());
    fireEvent.click(screen.getByText('Manage members'));
    const cb = await screen.findByRole('checkbox');
    fireEvent.click(cb);
    await waitFor(() => expect(addGroupMember).toHaveBeenCalledWith(g.id, expect.any(String)));
  });
});
