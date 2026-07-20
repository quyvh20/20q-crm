import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Invitation, PipelineStage, RoleDetail, WorkspaceMember } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  getWorkspaceMembers: vi.fn(),
  listInvitations: vi.fn(),
  getRoles: vi.fn(),
  getStages: vi.fn(),
  getContacts: vi.fn(),
  updateProfile: vi.fn(),
  createObjectDef: vi.fn(),
  createFieldDef: vi.fn(),
  upsertKBSection: vi.fn(),
}));

// Capabilities are swapped per test — the whole point of the card is that it shows
// a person only the steps they're allowed to do.
let caps = new Set<string>();
let objectAccess = true;
const setUserProfile = vi.fn();
let onboardingCompleted = false;

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({
    user: { id: 'u1', onboarding_completed: onboardingCompleted },
    activeWorkspace: { org_id: 'org-1', org_name: 'Acme' },
    setUserProfile,
  }),
  usePermissions: () => ({
    can: (code: string) => caps.has(code),
    canAccess: () => objectAccess,
    loaded: true,
  }),
}));

import {
  getContacts, getRoles, getStages, getWorkspaceMembers, listInvitations, updateProfile,
} from '../../../lib/api';
import SetupChecklist from '../SetupChecklist';
import { openSetupChecklist, resetSetupChecklistSession } from '../checklistState';
import { isPipelineCustomized, SEEDED_STAGE_NAMES } from '../useSetupChecklist';

const ALL_CAPS = ['members.invite', 'roles.manage', 'pipeline.manage'];

function member(id: string): WorkspaceMember {
  return {
    user_id: id, email: `${id}@acme.test`, first_name: 'A', last_name: 'B', full_name: 'A B',
    role_id: 'r1', role: 'admin', status: 'active',
  };
}

function seededStages(): PipelineStage[] {
  return SEEDED_STAGE_NAMES.map((name, i) => ({
    id: `s${i}`, org_id: 'org-1', name, position: i, color: '#000', is_won: false, is_lost: false,
  }));
}

function role(partial: Partial<RoleDetail>): RoleDetail {
  return {
    id: 'r1', name: 'admin', description: '', is_system: true, is_owner: false,
    data_scope: 'all', capabilities: [], member_count: 1, ...partial,
  };
}

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SetupChecklist />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  localStorage.clear();
  act(() => resetSetupChecklistSession());
  caps = new Set(ALL_CAPS);
  objectAccess = true;
  onboardingCompleted = false;

  // Default fixture: a brand-new workspace — one member, no invites, only built-in
  // roles, the untouched seeded pipeline, no contacts. Nothing is done.
  vi.mocked(getWorkspaceMembers).mockResolvedValue([member('u1')]);
  vi.mocked(listInvitations).mockResolvedValue([]);
  vi.mocked(getRoles).mockResolvedValue([role({})]);
  vi.mocked(getStages).mockResolvedValue(seededStages());
  vi.mocked(getContacts).mockResolvedValue({ contacts: [], meta: { next_cursor: '', has_more: false } as never });
  vi.mocked(updateProfile).mockResolvedValue({} as never);
});

afterEach(() => {
  act(() => resetSetupChecklistSession());
});

describe('SetupChecklist — completion derived from workspace data', () => {
  it('shows all four steps undone on a brand-new workspace', async () => {
    renderCard();
    expect(await screen.findByText('Set up your workspace')).toBeInTheDocument();
    expect(screen.getByText('0 of 4 done. Pick up wherever you left off.')).toBeInTheDocument();
    expect(screen.getByLabelText('Invite your team — not done yet')).toBeInTheDocument();
    expect(screen.getByLabelText('Set up roles & permissions — not done yet')).toBeInTheDocument();
    expect(screen.getByLabelText('Build your pipeline — not done yet')).toBeInTheDocument();
    expect(screen.getByLabelText('Import your contacts — not done yet')).toBeInTheDocument();
  });

  it('ticks "invite" once a second member exists', async () => {
    vi.mocked(getWorkspaceMembers).mockResolvedValue([member('u1'), member('u2')]);
    renderCard();
    expect(await screen.findByLabelText('Invite your team — done')).toBeInTheDocument();
  });

  it('ticks "invite" on a still-pending invitation — you already did the work', async () => {
    vi.mocked(listInvitations).mockResolvedValue([
      { id: 'i1', email: 'new@acme.test', role_id: 'r1', role: 'rep', status: 'pending', expires_at: '', created_at: '' } as Invitation,
    ]);
    renderCard();
    expect(await screen.findByLabelText('Invite your team — done')).toBeInTheDocument();
  });

  it('does NOT tick "invite" on an expired or revoked invitation', async () => {
    vi.mocked(listInvitations).mockResolvedValue([
      { id: 'i1', email: 'a@x.test', role_id: 'r1', role: 'rep', status: 'expired', expires_at: '', created_at: '' } as Invitation,
      { id: 'i2', email: 'b@x.test', role_id: 'r1', role: 'rep', status: 'revoked', expires_at: '', created_at: '' } as Invitation,
    ]);
    renderCard();
    expect(await screen.findByLabelText('Invite your team — not done yet')).toBeInTheDocument();
  });

  it('ticks "roles" only once a custom (non built-in) role exists', async () => {
    vi.mocked(getRoles).mockResolvedValue([role({}), role({ id: 'r2', name: 'Sales rep', is_system: false })]);
    renderCard();
    expect(await screen.findByLabelText('Set up roles & permissions — done')).toBeInTheDocument();
  });

  it('ticks "pipeline" once the seeded stages have been changed', async () => {
    const custom = seededStages();
    custom[1] = { ...custom[1], name: 'Discovery' };
    vi.mocked(getStages).mockResolvedValue(custom);
    renderCard();
    expect(await screen.findByLabelText('Build your pipeline — done')).toBeInTheDocument();
  });

  it('ticks "contacts" once the workspace has any contact', async () => {
    vi.mocked(getContacts).mockResolvedValue({
      contacts: [{ id: 'c1' }] as never,
      meta: { next_cursor: '', has_more: false } as never,
    });
    renderCard();
    expect(await screen.findByLabelText('Import your contacts — done')).toBeInTheDocument();
  });

  it('hides itself once every visible step is done', async () => {
    vi.mocked(getWorkspaceMembers).mockResolvedValue([member('u1'), member('u2')]);
    vi.mocked(getRoles).mockResolvedValue([role({ id: 'r2', is_system: false })]);
    vi.mocked(getStages).mockResolvedValue(
      seededStages().map((s, i) => (i === 0 ? { ...s, name: 'Sourced' } : s)),
    );
    vi.mocked(getContacts).mockResolvedValue({
      contacts: [{ id: 'c1' }] as never,
      meta: { next_cursor: '', has_more: false } as never,
    });

    renderCard();
    await waitFor(() => expect(getContacts).toHaveBeenCalled());
    await waitFor(() => expect(screen.queryByText('Set up your workspace')).not.toBeInTheDocument());
  });
});

describe('SetupChecklist — capability gating', () => {
  it('never shows a step the user has no permission to do', async () => {
    caps = new Set(['pipeline.manage']);
    objectAccess = false;

    renderCard();
    expect(await screen.findByText('Build your pipeline')).toBeInTheDocument();
    expect(screen.queryByText('Invite your team')).not.toBeInTheDocument();
    expect(screen.queryByText('Set up roles & permissions')).not.toBeInTheDocument();
    expect(screen.queryByText('Import your contacts')).not.toBeInTheDocument();
    expect(screen.getByText('0 of 1 done. Pick up wherever you left off.')).toBeInTheDocument();
  });

  it('does not even probe the endpoints behind gated-away steps', async () => {
    caps = new Set(['pipeline.manage']);
    objectAccess = false;

    renderCard();
    await waitFor(() => expect(getStages).toHaveBeenCalled());
    expect(getWorkspaceMembers).not.toHaveBeenCalled();
    expect(listInvitations).not.toHaveBeenCalled();
    expect(getRoles).not.toHaveBeenCalled();
    expect(getContacts).not.toHaveBeenCalled();
  });

  it('renders nothing at all when the user may do none of the steps', async () => {
    caps = new Set();
    objectAccess = false;

    const { container } = renderCard();
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  // Gated on org.settings, matching POST /api/templates/:slug/apply. It was
  // objects.manage back when a template only created objects; a template now also
  // installs pipeline stages, knowledge base and automations, and a gate looser
  // than the endpoint's would show a button whose every click 403s.
  it('offers the template action only to someone who can manage workspace settings', async () => {
    renderCard();
    await screen.findByText('Set up your workspace');
    expect(screen.queryByText('Start from a template')).not.toBeInTheDocument();

    // objects.manage alone is no longer enough.
    cleanup();
    caps = new Set([...ALL_CAPS, 'objects.manage']);
    renderCard();
    await screen.findByText('Set up your workspace');
    expect(screen.queryByText('Start from a template')).not.toBeInTheDocument();

    cleanup();
    caps = new Set([...ALL_CAPS, 'org.settings']);
    renderCard();
    expect(await screen.findByText('Start from a template')).toBeInTheDocument();
  });
});

describe('SetupChecklist — dismiss and restore', () => {
  it('hides on dismiss and persists it PER-WORKSPACE without writing the global flag', async () => {
    renderCard();
    fireEvent.click(await screen.findByText('Hide'));

    expect(screen.queryByText('Set up your workspace')).not.toBeInTheDocument();
    expect(localStorage.getItem('setup_checklist_dismissed:org-1')).toBe('true');
    // Regression (bug-hunt finding): dismissing in one workspace must NOT set the
    // global `onboarding_completed` flag — that flag suppresses the checklist in
    // EVERY workspace, so it would hide the card in the user's other workspaces,
    // including brand-new empty ones where it's most useful. Dismissal is per-org.
    expect(updateProfile).not.toHaveBeenCalled();
  });

  it('stays hidden on re-render after a dismiss', async () => {
    renderCard();
    fireEvent.click(await screen.findByText('Hide'));
    cleanup();

    const { container } = renderCard();
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it('comes back when the user reopens it from the account menu', async () => {
    localStorage.setItem('setup_checklist_dismissed:org-1', 'true');
    renderCard();
    await waitFor(() => expect(screen.queryByText('Set up your workspace')).not.toBeInTheDocument());

    act(() => openSetupChecklist());
    expect(await screen.findByText('Set up your workspace')).toBeInTheDocument();
  });

  it('is hidden for an established user who already finished the old wizard — but still reopenable', async () => {
    onboardingCompleted = true;
    renderCard();
    await waitFor(() => expect(screen.queryByText('Set up your workspace')).not.toBeInTheDocument());

    act(() => openSetupChecklist());
    expect(await screen.findByText('Set up your workspace')).toBeInTheDocument();
    // Re-hiding must not re-PATCH a flag the server already has.
    fireEvent.click(screen.getByText('Hide'));
    expect(updateProfile).not.toHaveBeenCalled();
  });

  it('honors the legacy localStorage key written by the retired wizard', async () => {
    localStorage.setItem('onboarding_completed', 'true');
    const { container } = renderCard();
    await waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it('shows the done state when reopened after everything is finished', async () => {
    vi.mocked(getWorkspaceMembers).mockResolvedValue([member('u1'), member('u2')]);
    vi.mocked(getRoles).mockResolvedValue([role({ id: 'r2', is_system: false })]);
    vi.mocked(getStages).mockResolvedValue(
      seededStages().map((s, i) => (i === 0 ? { ...s, name: 'Sourced' } : s)),
    );
    vi.mocked(getContacts).mockResolvedValue({
      contacts: [{ id: 'c1' }] as never,
      meta: { next_cursor: '', has_more: false } as never,
    });
    act(() => openSetupChecklist());

    renderCard();
    expect(await screen.findByText("You're all set — every step below is done.")).toBeInTheDocument();
  });
});

describe('isPipelineCustomized', () => {
  const stage = (name: string, position: number): Pick<PipelineStage, 'name' | 'position'> => ({ name, position });

  it('is false for the untouched seed a new workspace ships with', () => {
    expect(isPipelineCustomized(SEEDED_STAGE_NAMES.map(stage))).toBe(false);
  });

  it('ignores the order the server returns the stages in', () => {
    const shuffled = [...SEEDED_STAGE_NAMES.map(stage)].reverse();
    expect(isPipelineCustomized(shuffled)).toBe(false);
  });

  it('is true when a stage is renamed, added or removed', () => {
    const renamed = SEEDED_STAGE_NAMES.map(stage);
    renamed[2] = stage('Demo', 2);
    expect(isPipelineCustomized(renamed)).toBe(true);
    expect(isPipelineCustomized([...SEEDED_STAGE_NAMES.map(stage), stage('Closed Lost', 5)])).toBe(true);
    expect(isPipelineCustomized(SEEDED_STAGE_NAMES.slice(0, 4).map(stage))).toBe(true);
  });

  it('is true when the seeded stages are reordered', () => {
    const reordered = SEEDED_STAGE_NAMES.map(stage);
    reordered[0] = stage('Lead In', 1);
    reordered[1] = stage('Qualified', 0);
    expect(isPipelineCustomized(reordered)).toBe(true);
  });

  it('is false for an empty pipeline — there is nothing to have customized', () => {
    expect(isPipelineCustomized([])).toBe(false);
  });
});
