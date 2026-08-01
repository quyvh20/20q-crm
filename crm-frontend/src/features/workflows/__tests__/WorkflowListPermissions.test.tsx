import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Workflow, WorkflowListResponse } from '../types';

/**
 * Permission gating for WorkflowList.
 *
 * Server truth CHANGED in R2: the reads are no longer open to any member. The list
 * response embeds each workflow's `steps`/`actions`, and a send_webhook step keeps
 * its `headers` map verbatim — which is where an Authorization: Bearer for the
 * called system lives. So the list itself was handing third-party credentials to
 * anyone authenticated, and GET list/detail/runs are all workflows.manage-gated
 * now. Without the capability there is nothing here to render, so the page shows
 * the denied panel rather than a shell whose every request 403s.
 *
 * Run Now keeps its author fallback (run_any OR creator) for callers who CAN see
 * the page — that check is unchanged, it simply no longer has a non-manage
 * audience.
 */

vi.mock('../api', () => ({
  getWorkflows: vi.fn(),
  deleteWorkflow: vi.fn(),
  toggleWorkflow: vi.fn(),
}));

// Mutable permission state so each test drives the caller's capabilities.
const mockPerms = vi.hoisted(() => ({
  user: { id: 'user-1' } as { id: string } | null,
  canRunAny: false,
  canManage: false,
}));
vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({
    user: mockPerms.user,
    hasCapability: (code: string) => code === 'workflows.run_any' && mockPerms.canRunAny,
  }),
  usePermissions: () => ({
    loaded: true,
    can: (code: string) =>
      (code === 'workflows.manage' && mockPerms.canManage) ||
      (code === 'workflows.run_any' && mockPerms.canRunAny),
  }),
}));

// The modal itself is never opened here; keep the real pure helpers
// (canRunWorkflowNow) WorkflowList uses for gating and stub only the component.
vi.mock('../RunNowModal', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../RunNowModal')>();
  return { ...actual, RunNowModal: () => null };
});

import { WorkflowList } from '../WorkflowList';
import { getWorkflows } from '../api';

const mockGetWorkflows = vi.mocked(getWorkflows);

function makeWorkflow(over: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf-1',
    org_id: 'org-1',
    name: 'Welcome Email',
    description: '',
    is_active: true,
    trigger: { type: 'contact_created' },
    conditions: null,
    actions: [],
    action_count: 0,
    version: 1,
    created_by: 'author-x',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    last_run_status: null,
    last_run_at: null,
    ...over,
  };
}

function listResponse(workflows: Workflow[]): WorkflowListResponse {
  return { workflows, total: workflows.length, page: 1, size: 20 };
}

function renderList() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={['/workflows']}>
        <WorkflowList />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPerms.user = { id: 'user-1' };
  mockPerms.canRunAny = false;
  mockPerms.canManage = false;
  mockGetWorkflows.mockResolvedValue(
    listResponse([
      makeWorkflow({ id: 'wf-a', name: 'Welcome Email' }),
      makeWorkflow({ id: 'wf-b', name: 'Deal Won Alert', trigger: { type: 'deal_stage_changed' } }),
    ]),
  );
});

describe('WorkflowList — the whole surface requires workflows.manage', () => {
  // The security assertion. A workflow row carries its own definition, so
  // "readable but read-only" is not a coherent state for this page: showing the
  // list at all discloses any bearer token a send_webhook step holds.
  it('shows nothing but the denied panel without workflows.manage', async () => {
    renderList();

    // The panel renders CAPABILITY_LABELS' friendly name, not the raw code.
    expect(await screen.findByText(/manage workflows/i)).toBeInTheDocument();

    expect(screen.queryByText('Welcome Email')).not.toBeInTheDocument();
    expect(screen.queryByText('Deal Won Alert')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /History/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /New Workflow/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Email Templates/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Run Now/i })).not.toBeInTheDocument();
  });

  it('shows all write affordances for a workflows.manage holder', async () => {
    mockPerms.canManage = true;
    renderList();

    await screen.findByText('Welcome Email');

    expect(screen.getByRole('button', { name: /New Workflow/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Email Templates/i })).toBeInTheDocument();
    expect(screen.getAllByRole('button', { name: /Duplicate/i })).toHaveLength(2);
    // Both fixtures are active, so each row's toggle reads "Deactivate".
    expect(screen.getAllByRole('button', { name: /^Deactivate$/ })).toHaveLength(2);
    expect(screen.getAllByRole('button', { name: /^Delete$/ })).toHaveLength(2);
  });

  // The creator allowance is unchanged server-side (Run Now permits run_any OR
  // being the author, with workflows.manage playing no part). What changed is who
  // can reach the page to use it. Asserted with manage held, so a later refactor
  // cannot quietly widen Run Now to every row.
  it('offers Run Now only on the caller\'s own workflows without run_any', async () => {
    mockPerms.canManage = true;
    mockPerms.user = { id: 'author-me' };
    mockGetWorkflows.mockResolvedValue(
      listResponse([
        makeWorkflow({ id: 'wf-mine', name: 'My Flow', created_by: 'author-me' }),
        makeWorkflow({ id: 'wf-theirs', name: 'Their Flow', created_by: 'author-x' }),
      ]),
    );

    renderList();
    await screen.findByText('My Flow');

    expect(screen.getAllByRole('button', { name: /Run Now/i })).toHaveLength(1);
  });

  it('offers Run Now on every workflow with run_any', async () => {
    mockPerms.canManage = true;
    mockPerms.canRunAny = true;
    renderList();

    await screen.findByText('Welcome Email');
    expect(screen.getAllByRole('button', { name: /Run Now/i })).toHaveLength(2);
  });
});
