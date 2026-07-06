import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Report, ReportShareView, WorkspaceMember, RoleDetail, UserGroup } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listReportShares: vi.fn(),
  addReportShare: vi.fn(),
  removeReportShare: vi.fn(),
  updateReport: vi.fn(),
  getWorkspaceMembers: vi.fn(),
  getRoles: vi.fn(),
  listGroups: vi.fn(),
}));

vi.mock('../../../lib/auth', () => ({ useAuth: vi.fn() }));

import {
  listReportShares, addReportShare, removeReportShare, updateReport, getWorkspaceMembers, getRoles, listGroups,
} from '../../../lib/api';
import { useAuth } from '../../../lib/auth';
import ReportShareDialog from '../ReportShareDialog';

// The dialog only reads user?.id off the auth context; a minimal stub suffices.
const authAs = (id: string) => ({ user: { id } }) as unknown as ReturnType<typeof useAuth>;

const report = (partial: Partial<Report> = {}): Report => ({
  id: 'rep1', org_id: 'org1', name: 'Deals by stage', description: '', object_slug: 'deal',
  config: { chart: 'bar', aggregate: { fn: 'count' } }, visibility: 'private',
  created_at: '2026-01-01T00:00:00Z', updated_at: '2026-01-01T00:00:00Z', ...partial,
});

const member = (id: string, name: string): WorkspaceMember => ({
  user_id: id, email: `${name}@x.com`, first_name: name, last_name: '', full_name: name, role: 'sales_rep', status: 'active',
});
const role = (id: string, name: string): RoleDetail => ({ id, name, is_system: true, is_owner: false, data_scope: 'all', capabilities: [], member_count: 1 });
const grp = (id: string, name: string): UserGroup => ({ id, name, description: '', member_count: 0, members: [], created_at: '2026-01-01T00:00:00Z' });
const share = (partial: Partial<ReportShareView>): ReportShareView => ({
  id: crypto.randomUUID(), target_type: 'user', target_id: crypto.randomUUID(), target_name: 'Someone', level: 'view', created_at: '2026-01-01T00:00:00Z', ...partial,
});

// The dialog calls useQueryClient (to invalidate report caches on visibility
// change), so renders need a QueryClientProvider.
const renderDialog = (r: Report = report()) =>
  render(
    <QueryClientProvider client={new QueryClient()}>
      <ReportShareDialog report={r} onClose={() => {}} />
    </QueryClientProvider>,
  );

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  // Current user defaults to someone outside the member list so the other tests
  // still see both Alice and Bob as candidates.
  vi.mocked(useAuth).mockReturnValue(authAs('me'));
  vi.mocked(getWorkspaceMembers).mockResolvedValue([member('u1', 'Alice'), member('u2', 'Bob')]);
  vi.mocked(getRoles).mockResolvedValue([role('r1', 'sales_rep')]);
  vi.mocked(listGroups).mockResolvedValue([grp('g1', 'West Region')]);
});

describe('ReportShareDialog', () => {
  it('shows the empty state, then adds a user share at edit', async () => {
    vi.mocked(listReportShares).mockResolvedValue([]);
    vi.mocked(addReportShare).mockResolvedValue(undefined);
    renderDialog();

    await waitFor(() => expect(screen.getByText(/Not shared with anyone yet/)).toBeTruthy());
    fireEvent.change(screen.getByLabelText('Share target'), { target: { value: 'u1' } });
    fireEvent.change(screen.getByLabelText('Access level'), { target: { value: 'edit' } });
    fireEvent.click(screen.getByText('Add'));
    await waitFor(() => expect(addReportShare).toHaveBeenCalledWith('rep1', 'user', 'u1', 'edit'));
  });

  it('never offers the current user as a People share target', async () => {
    // Log in as u1 (Alice); she must not appear in the picker (sharing to self
    // is meaningless — the owner already has full access), but Bob should.
    vi.mocked(useAuth).mockReturnValue(authAs('u1'));
    vi.mocked(listReportShares).mockResolvedValue([]);
    renderDialog();

    const select = screen.getByLabelText('Share target') as HTMLSelectElement;
    await waitFor(() => expect([...select.options].some((o) => o.textContent === 'Bob')).toBe(true));
    expect([...select.options].some((o) => o.textContent === 'Alice')).toBe(false);
  });

  it('switches the target tab to Groups and lists group candidates', async () => {
    vi.mocked(listReportShares).mockResolvedValue([]);
    renderDialog();
    await waitFor(() => expect(screen.getByText('Groups')).toBeTruthy());
    fireEvent.click(screen.getByText('Groups'));
    const select = screen.getByLabelText('Share target') as HTMLSelectElement;
    await waitFor(() => expect([...select.options].some((o) => o.textContent === 'West Region')).toBe(true));
  });

  it('lists existing shares and removes one', async () => {
    // Use a name not in the member picker so the share row is unambiguous.
    const s = share({ target_name: 'West Region', target_type: 'group', level: 'view' });
    vi.mocked(listReportShares).mockResolvedValue([s]);
    vi.mocked(removeReportShare).mockResolvedValue(undefined);
    renderDialog();

    await waitFor(() => expect(screen.getByLabelText('Remove West Region')).toBeTruthy());
    fireEvent.click(screen.getByLabelText('Remove West Region'));
    await waitFor(() => expect(removeReportShare).toHaveBeenCalledWith('rep1', s.id));
  });

  it('changes general access to workspace via updateReport', async () => {
    vi.mocked(listReportShares).mockResolvedValue([]);
    vi.mocked(updateReport).mockResolvedValue(report({ visibility: 'org' }));
    renderDialog();

    await waitFor(() => expect(screen.getByLabelText('General access')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('General access'), { target: { value: 'org' } });
    await waitFor(() => expect(updateReport).toHaveBeenCalledWith('rep1', expect.objectContaining({ visibility: 'org' })));
  });
});
