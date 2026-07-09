import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { WorkflowList } from './WorkflowList';
import { getWorkflows, toggleWorkflow, deleteWorkflow } from './api';
import type { Workflow, WorkflowListResponse } from './types';

/**
 * WorkflowList Run Now integration tests — Requirements 8.1, 8.2.
 *
 * Task 8.1 added a "▶ Run Now" button to each per-row action cluster; clicking
 * it sets the modal target to that workflow and renders `RunNowModal`. These
 * tests assert:
 *   - a Run Now control is rendered for EACH workflow row (Req 8.1), and
 *   - clicking a row's Run Now control opens the modal for THAT workflow (Req 8.2).
 *
 * We mock `./api` so the `getWorkflows` effect resolves with fixtures and the
 * other client functions never hit the network. We stub `./RunNowModal` with a
 * lightweight double that surfaces the workflow it was opened for (the modal's
 * own behavior is covered by RunNowModal.test.tsx), keeping these tests focused
 * on WorkflowList's rendering + open-on-click wiring. `react-router-dom`'s
 * `useNavigate` is mocked to a no-op since navigation is not under test here.
 */

vi.mock('./api', () => ({
  getWorkflows: vi.fn(),
  deleteWorkflow: vi.fn(),
  toggleWorkflow: vi.fn(),
}));

// Mutable auth state so individual tests can drive the caller's role/id. WorkflowList
// uses `useAuth` to decide whether to show the Run Now control (owner/admin/manager, or
// the workflow's creator). Defaults to a privileged caller in beforeEach so the existing
// rendering/open-on-click tests see the control on every row.
const mockAuth = vi.hoisted(() => ({
  user: { id: 'user-1' } as { id: string } | null,
  // canRunAny stands in for holding the workflows.run_any capability (P6) — the
  // old owner/admin/manager "privileged" set.
  canRunAny: true,
}));
vi.mock('../../lib/auth', () => ({
  useAuth: () => ({
    user: mockAuth.user,
    hasCapability: (code: string) => code === 'workflows.run_any' && mockAuth.canRunAny,
  }),
}));

// Stable navigate spy; WorkflowList only consumes `useNavigate` from the router.
const mockNavigate = vi.hoisted(() => vi.fn());
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useNavigate: () => mockNavigate };
});

// Lightweight RunNowModal double: renders an identifiable dialog that carries
// the targeted workflow's id and name, plus a Close button wired to onClose.
// This lets us assert WHICH workflow's modal opened without exercising the real
// modal's EntityPicker / confirm flow (covered by RunNowModal.test.tsx).
vi.mock('./RunNowModal', async (importOriginal) => {
  // Keep the real pure helpers (canRunWorkflowNow / entityKindForTrigger) — WorkflowList
  // uses canRunWorkflowNow to gate the control — while stubbing only the modal component.
  const actual = await importOriginal<typeof import('./RunNowModal')>();
  const React = await import('react');
  return {
    ...actual,
    RunNowModal: ({
      workflow,
      onClose,
      onSuccess,
    }: {
      workflow: Workflow;
      onClose: () => void;
      onSuccess: (runId: string) => void;
    }) =>
      React.createElement(
        'div',
        {
          role: 'dialog',
          'data-testid': 'run-now-modal',
          'data-workflow-id': workflow.id,
        },
        React.createElement('span', { 'data-testid': 'run-now-modal-workflow' }, workflow.name),
        React.createElement('button', { type: 'button', onClick: onClose }, 'Close modal'),
        // Stands in for the modal's real "confirm → POST → onSuccess" path so the host's
        // toast/navigation wiring can be exercised without the real submit flow.
        React.createElement(
          'button',
          { type: 'button', onClick: () => onSuccess('run-started-1') },
          'Simulate run started',
        ),
      ),
  };
});

const mockGetWorkflows = vi.mocked(getWorkflows);

// WorkflowList reads search/filter/page from the URL via useSearchParams, so it must
// render inside a router. MemoryRouter lets each test seed the initial query string
// (initialEntries) to exercise deep-link / back-button restoration.
function renderList(opts?: { route?: string }) {
  // Fresh client per render so cached lists don't bleed across tests; retry off
  // so a rejected query surfaces immediately.
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter initialEntries={[opts?.route ?? '/workflows']}>
        <WorkflowList />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

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
    created_by: 'user-1',
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

const TWO_WORKFLOWS: Workflow[] = [
  makeWorkflow({ id: 'wf-contact', name: 'Welcome Email', trigger: { type: 'contact_created' } }),
  makeWorkflow({ id: 'wf-deal', name: 'Deal Won Alert', trigger: { type: 'deal_stage_changed' } }),
];

beforeEach(() => {
  vi.clearAllMocks();
  mockGetWorkflows.mockResolvedValue(listResponse(TWO_WORKFLOWS));
  // Default to a privileged caller so the Run Now control renders on every row.
  mockAuth.user = { id: 'user-1' };
  mockAuth.canRunAny = true;
});

// ── Req 8.1: a Run Now control is displayed for EACH workflow row ──────
describe('WorkflowList — Run Now control per row', () => {
  it('renders one Run Now button for every workflow in the list (Req 8.1)', async () => {
    renderList();

    // Wait for the workflows to load and the rows to render.
    await screen.findByText('Welcome Email');
    expect(screen.getByText('Deal Won Alert')).toBeInTheDocument();

    // Exactly one "Run Now" control per row (the stubbed modal is closed, so it
    // contributes no competing "Run Now" buttons).
    const runNowButtons = screen.getAllByRole('button', { name: /Run Now/i });
    expect(runNowButtons).toHaveLength(2);
  });
});

// ── Req 8.2: activating a row's Run Now opens the modal for THAT workflow ──
describe('WorkflowList — opening the Run Now modal', () => {
  it('opens the modal for the clicked workflow (Req 8.2)', async () => {
    const user = userEvent.setup();
    renderList();

    await screen.findByText('Welcome Email');

    // No modal until a Run Now control is activated.
    expect(screen.queryByTestId('run-now-modal')).not.toBeInTheDocument();

    // Click the FIRST row's Run Now control.
    const runNowButtons = screen.getAllByRole('button', { name: /Run Now/i });
    await user.click(runNowButtons[0]);

    // The modal opens for the first workflow specifically.
    const modal = await screen.findByTestId('run-now-modal');
    expect(modal).toHaveAttribute('data-workflow-id', 'wf-contact');
    expect(screen.getByTestId('run-now-modal-workflow')).toHaveTextContent('Welcome Email');
  });

  it('opens the modal for the SECOND workflow when its Run Now control is clicked (Req 8.2)', async () => {
    const user = userEvent.setup();
    renderList();

    await screen.findByText('Deal Won Alert');

    const runNowButtons = screen.getAllByRole('button', { name: /Run Now/i });
    await user.click(runNowButtons[1]);

    const modal = await screen.findByTestId('run-now-modal');
    expect(modal).toHaveAttribute('data-workflow-id', 'wf-deal');
    expect(screen.getByTestId('run-now-modal-workflow')).toHaveTextContent('Deal Won Alert');
  });

  it('closes the modal when the modal requests it, leaving no dialog open (Req 8.2)', async () => {
    const user = userEvent.setup();
    renderList();

    await screen.findByText('Welcome Email');

    await user.click(screen.getAllByRole('button', { name: /Run Now/i })[0]);
    expect(await screen.findByTestId('run-now-modal')).toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Close modal' }));

    await waitFor(() => expect(screen.queryByTestId('run-now-modal')).not.toBeInTheDocument());
  });
});

// ── DoD: success surfaces a "Run started" toast linking to the run detail ─────
describe('WorkflowList — run-started toast and View run link', () => {
  it('shows a "Run started" toast whose "View run" link navigates to the run detail', async () => {
    const user = userEvent.setup();
    renderList();
    await screen.findByText('Welcome Email');

    // Open the modal for the first workflow, then have it report a created run.
    await user.click(screen.getAllByRole('button', { name: /Run Now/i })[0]);
    await user.click(await screen.findByRole('button', { name: 'Simulate run started' }));

    // A success toast names the workflow and carries a "View run" link.
    expect(await screen.findByText(/Run started for "Welcome Email"/i)).toBeInTheDocument();
    const viewRun = screen.getByRole('button', { name: /View run/i });

    // Clicking it navigates to THIS workflow's run history, highlighting the created run
    // so RunHistory can auto-open its detail.
    await user.click(viewRun);
    expect(mockNavigate).toHaveBeenCalledWith('/workflows/wf-contact/history', {
      state: { highlightRunId: 'run-started-1' },
    });
  });
});

// ── P23: Duplicate opens the builder on a "new" route carrying the source id ──
describe('WorkflowList — Duplicate workflow', () => {
  it('renders a Duplicate control on every row', async () => {
    renderList();
    await screen.findByText('Welcome Email');

    expect(screen.getAllByRole('button', { name: /Duplicate/i })).toHaveLength(2);
  });

  it('navigates to /workflows/new carrying the clicked workflow id in router state', async () => {
    const user = userEvent.setup();
    renderList();
    await screen.findByText('Deal Won Alert');

    // Duplicate the SECOND row — the source id must ride along so the builder clones it.
    await user.click(screen.getAllByRole('button', { name: /Duplicate/i })[1]);

    expect(mockNavigate).toHaveBeenCalledWith('/workflows/new', {
      state: { duplicateFromId: 'wf-deal' },
    });
  });
});

// ── Run Now visibility mirrors backend authorization (creator allowance) ──────
describe('WorkflowList — Run Now visibility by permission', () => {
  it('shows Run Now on every row for a privileged role regardless of creator', async () => {
    // A run_any holder who created neither workflow still sees both controls.
    mockAuth.canRunAny = true;
    mockAuth.user = { id: 'someone-else' };
    mockGetWorkflows.mockResolvedValue(
      listResponse([
        makeWorkflow({ id: 'wf-a', name: 'Alpha', created_by: 'author-x' }),
        makeWorkflow({ id: 'wf-b', name: 'Beta', created_by: 'author-y' }),
      ]),
    );

    renderList();
    await screen.findByText('Alpha');

    expect(screen.getAllByRole('button', { name: /Run Now/i })).toHaveLength(2);
  });

  it('hides Run Now for a non-privileged caller on workflows they did not create', async () => {
    mockAuth.canRunAny = false;
    mockAuth.user = { id: 'viewer-1' };
    mockGetWorkflows.mockResolvedValue(
      listResponse([makeWorkflow({ id: 'wf-foreign', name: 'Not Mine', created_by: 'author-x' })]),
    );

    renderList();
    await screen.findByText('Not Mine');

    expect(screen.queryByRole('button', { name: /Run Now/i })).not.toBeInTheDocument();
  });

  it('shows Run Now to a non-privileged caller only on workflows they created', async () => {
    // A non-run_any caller who created exactly one of the two listed workflows.
    mockAuth.canRunAny = false;
    mockAuth.user = { id: 'viewer-1' };
    mockGetWorkflows.mockResolvedValue(
      listResponse([
        makeWorkflow({ id: 'wf-mine', name: 'My Flow', created_by: 'viewer-1' }),
        makeWorkflow({ id: 'wf-theirs', name: 'Their Flow', created_by: 'author-x' }),
      ]),
    );

    const user = userEvent.setup();
    renderList();
    await screen.findByText('My Flow');

    // Exactly one control — on the workflow the viewer created — and clicking it opens
    // that workflow's modal.
    const runNowButtons = screen.getAllByRole('button', { name: /Run Now/i });
    expect(runNowButtons).toHaveLength(1);

    await user.click(runNowButtons[0]);
    const modal = await screen.findByTestId('run-now-modal');
    expect(modal).toHaveAttribute('data-workflow-id', 'wf-mine');
  });
});

// ── P24: name/description search, combinable with the active filter, URL-backed ──
describe('WorkflowList — search and filtering (P24)', () => {
  const SEARCH_WORKFLOWS: Workflow[] = [
    makeWorkflow({ id: 'wf-vip', name: 'VIP Onboarding', is_active: true }),
    makeWorkflow({ id: 'wf-news', name: 'Newsletter Blast', is_active: false }),
  ];

  // Drive the mocked client like the real server: filter the fixtures by the ?q= term
  // (name, case-insensitive) and the active flag, so the rendered rows genuinely
  // reflect the params the component requested.
  beforeEach(() => {
    mockGetWorkflows.mockImplementation(async (params) => {
      const term = params?.q?.toLowerCase();
      const activeFilter = params?.active;
      let list = SEARCH_WORKFLOWS;
      if (term) list = list.filter((w) => w.name.toLowerCase().includes(term));
      if (activeFilter !== undefined) list = list.filter((w) => w.is_active === activeFilter);
      return listResponse(list);
    });
  });

  it('filters the list to matching workflows when the user types a query', async () => {
    const user = userEvent.setup();
    renderList();

    await screen.findByText('VIP Onboarding');
    expect(screen.getByText('Newsletter Blast')).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText(/search workflows/i), 'vip');

    // After the debounce, the server is queried with the term and the list narrows.
    await waitFor(() =>
      expect(mockGetWorkflows).toHaveBeenCalledWith(expect.objectContaining({ q: 'vip' })),
    );
    await waitFor(() => expect(screen.queryByText('Newsletter Blast')).not.toBeInTheDocument());
    expect(screen.getByText('VIP Onboarding')).toBeInTheDocument();
  });

  it('clearing the search restores the full list', async () => {
    const user = userEvent.setup();
    // Start from a deep-linked, already-narrowed URL — this also proves the term is
    // restored from the query string on load (back-button / shared link).
    renderList({ route: '/workflows?q=vip' });

    await screen.findByText('VIP Onboarding');
    expect(screen.getByPlaceholderText(/search workflows/i)).toHaveValue('vip');
    expect(screen.queryByText('Newsletter Blast')).not.toBeInTheDocument();

    await user.click(screen.getByTitle('Clear search'));

    await waitFor(() => expect(screen.getByText('Newsletter Blast')).toBeInTheDocument());
    expect(screen.getByText('VIP Onboarding')).toBeInTheDocument();
  });

  it('applies the active filter and the search together', async () => {
    const user = userEvent.setup();
    renderList();
    await screen.findByText('VIP Onboarding');

    // Narrow to Active only…
    await user.click(screen.getByRole('button', { name: /^Active$/ }));
    await waitFor(() =>
      expect(mockGetWorkflows).toHaveBeenCalledWith(expect.objectContaining({ active: true })),
    );

    // …then search; both constraints must be sent in the same request.
    await user.type(screen.getByPlaceholderText(/search workflows/i), 'vip');
    await waitFor(() =>
      expect(mockGetWorkflows).toHaveBeenCalledWith(
        expect.objectContaining({ active: true, q: 'vip' }),
      ),
    );

    // The inactive workflow stays filtered out under the combined constraints.
    expect(screen.queryByText('Newsletter Blast')).not.toBeInTheDocument();
  });

  it('restores search, active filter, AND page together from the URL (deep link / back button)', async () => {
    renderList({ route: '/workflows?q=vip&active=true&page=2' });

    await screen.findByText('VIP Onboarding');
    expect(screen.getByPlaceholderText(/search workflows/i)).toHaveValue('vip');
    // All three params are read back on load — exactly what the browser back button
    // relies on when returning to a previously-narrowed list.
    expect(mockGetWorkflows).toHaveBeenCalledWith(
      expect.objectContaining({ q: 'vip', active: true, page: 2 }),
    );
  });

  it('changing the active filter does not clobber the search term (params are independent)', async () => {
    const user = userEvent.setup();
    renderList();
    await screen.findByText('VIP Onboarding');

    await user.type(screen.getByPlaceholderText(/search workflows/i), 'vip');
    await waitFor(() =>
      expect(mockGetWorkflows).toHaveBeenCalledWith(expect.objectContaining({ q: 'vip' })),
    );

    // Toggling the filter keeps the search term — both travel together in the URL.
    await user.click(screen.getByRole('button', { name: /^Inactive$/ }));
    await waitFor(() =>
      expect(mockGetWorkflows).toHaveBeenCalledWith(
        expect.objectContaining({ q: 'vip', active: false }),
      ),
    );
    expect(screen.getByPlaceholderText(/search workflows/i)).toHaveValue('vip');
  });
});

// ── A3.4: toggle/delete route through React Query mutations ────────────────────
describe('WorkflowList — toggle and delete mutations', () => {
  it('toggling a row calls the toggle API for that workflow', async () => {
    const user = userEvent.setup();
    vi.mocked(toggleWorkflow).mockResolvedValue(makeWorkflow({ id: 'wf-contact', is_active: false }));
    renderList();
    await screen.findByText('Welcome Email');

    // Both fixtures start active, so each row's toggle reads "Deactivate".
    await user.click(screen.getAllByRole('button', { name: /^Deactivate$/ })[0]);

    await waitFor(() => expect(toggleWorkflow).toHaveBeenCalledWith('wf-contact'));
  });

  it('deletes the workflow when the confirm dialog is accepted', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(deleteWorkflow).mockResolvedValue(undefined);
    renderList();
    await screen.findByText('Welcome Email');

    await user.click(screen.getAllByRole('button', { name: /^Delete$/ })[0]);

    await waitFor(() => expect(deleteWorkflow).toHaveBeenCalledWith('wf-contact'));
  });

  it('does not delete when the confirm dialog is dismissed', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    renderList();
    await screen.findByText('Welcome Email');

    await user.click(screen.getAllByRole('button', { name: /^Delete$/ })[0]);

    expect(deleteWorkflow).not.toHaveBeenCalled();
  });
});
