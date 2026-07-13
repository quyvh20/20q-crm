import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { Workflow, WorkflowListResponse } from '../types';

/**
 * U3 permission gating for WorkflowList. Server truth: GET list/detail/runs are
 * open to any member, but every workflow WRITE (create, toggle, delete — and
 * Duplicate, which opens the builder on a create) requires workflows.manage,
 * and ALL email-templates routes 403 even on GET without it. The list must stay
 * readable while those affordances hide for a non-manager, and Run Now must keep
 * its author fallback (run_any OR creator) independent of workflows.manage.
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

describe('WorkflowList — write affordances hidden without workflows.manage', () => {
  it('keeps the list readable but hides New Workflow, Email Templates, Duplicate, toggle, and Delete', async () => {
    renderList();

    // The rows themselves render — GET list is open to any member.
    await screen.findByText('Welcome Email');
    expect(screen.getByText('Deal Won Alert')).toBeInTheDocument();

    // Read-only affordances survive (run history GETs are open too).
    expect(screen.getAllByRole('button', { name: /History/i })).toHaveLength(2);

    // Every write affordance is gone.
    expect(screen.queryByRole('button', { name: /New Workflow/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Email Templates/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Duplicate/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^(Deactivate|Activate)$/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /^Delete$/ })).not.toBeInTheDocument();
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

  it('keeps Run Now for the author even without run_any or manage (creator allowance)', async () => {
    // Server truth: Run Now allows workflows.run_any OR being the author —
    // workflows.manage plays no part, so hiding the write affordances must not
    // take the author's Run Now with it.
    mockPerms.user = { id: 'viewer-1' };
    mockGetWorkflows.mockResolvedValue(
      listResponse([
        makeWorkflow({ id: 'wf-mine', name: 'My Flow', created_by: 'viewer-1' }),
        makeWorkflow({ id: 'wf-theirs', name: 'Their Flow', created_by: 'author-x' }),
      ]),
    );

    renderList();
    await screen.findByText('My Flow');

    // Exactly one control — on the workflow the viewer created.
    expect(screen.getAllByRole('button', { name: /Run Now/i })).toHaveLength(1);
  });
});
