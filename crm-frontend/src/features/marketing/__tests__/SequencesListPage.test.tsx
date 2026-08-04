import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import SequencesListPage from '../SequencesListPage';
import type { Workflow, WorkflowStep } from '../../workflows/types';

/**
 * SequencesListPage — the marketing-send gate on the drip-sequence picker.
 *
 * R5 deploy 1 removed the flat `actions` mirror from the wire, so the page's
 * `hasMarketingSend` was rewritten from a one-level `.some()` over `actions` into a
 * recursive walk of the canonical `steps` tree. Its failure mode is silent: a walk
 * that returns false for everything leaves every workflow reporting "no marketing
 * send step" with Enroll permanently disabled, and no error anywhere. These tests
 * pin the walk through the UI it drives — including the If/Else NO branch, which is
 * exactly what a flat read of the root list could never see.
 */

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

vi.mock('../sequencesQueries', () => ({
  useSequences: vi.fn(() => ({ data: [], isLoading: false, error: null, refetch: vi.fn() })),
  useCreateSequence: vi.fn(() => ({ mutate: vi.fn(), isPending: false })),
}));

vi.mock('../segmentsQueries', () => ({
  useSegments: vi.fn(() => ({ data: [{ id: 'seg-1', name: 'Newsletter readers' }] })),
}));

// The workflow list the picker renders. Mutable so each test can seed a different
// step tree without re-mocking the module.
const mockWorkflows = vi.hoisted(() => ({ list: [] as unknown[] }));
vi.mock('../../workflows/queries', () => ({
  useWorkflowsList: vi.fn(() => ({
    data: { workflows: mockWorkflows.list, total: mockWorkflows.list.length, page: 1, size: 20 },
  })),
}));

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (code: string) => (code === 'marketing.manage' ? can : false),
    loaded,
  });
}

// ── step-tree fixtures ────────────────────────────────────────────────────────

function marketingSend(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'send_email', params: { channel: 'marketing', template_id: 'tpl-1' } } };
}

function transactionalSend(id: string): WorkflowStep {
  return { id, type: 'action', action: { id, type: 'send_email', params: { channel: 'transactional', to: 'ops@example.com' } } };
}

function delayStep(id: string): WorkflowStep {
  return { id, type: 'delay', delay: { duration_sec: 86400 } };
}

function ifElse(id: string, yesSteps: WorkflowStep[], noSteps: WorkflowStep[]): WorkflowStep {
  return {
    id,
    type: 'condition',
    condition: { op: 'AND', rules: [{ field: 'contact.email', operator: 'is_not_empty' }] },
    yes_steps: yesSteps,
    no_steps: noSteps,
  };
}

function makeWorkflow(over: Partial<Workflow> = {}): Workflow {
  return {
    id: 'wf-1',
    org_id: 'org-1',
    name: 'Nurture drip',
    description: '',
    is_active: true,
    // 'schedule' is the only trigger that does not auto-enroll, so the picker's amber
    // double-send warning stays out of the way of the assertions below.
    trigger: { type: 'schedule', params: { cron: '0 9 * * *' } },
    conditions: null,
    steps: [],
    action_count: 1,
    version: 1,
    created_by: 'user-1',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    last_run_status: null,
    last_run_at: null,
    ...over,
  };
}

const renderPage = () => render(<MemoryRouter><SequencesListPage /></MemoryRouter>);

/**
 * Render the page, open the enrollment modal, select `wf`, and (unless told not to)
 * also select a segment — so the Enroll button's disabled state reflects the
 * marketing verdict alone rather than a half-filled form.
 */
async function pickWorkflow(wf: Workflow, opts: { segment?: boolean } = {}) {
  mockWorkflows.list = [wf];
  const user = userEvent.setup();
  renderPage();
  // Two "Enroll a segment" buttons render (page header + empty state); either opens
  // the same modal.
  await user.click(screen.getAllByRole('button', { name: /enroll a segment/i })[0]);
  fireEvent.change(await screen.findByLabelText(/sequence \(workflow\)/i), { target: { value: wf.id } });
  if (opts.segment !== false) {
    fireEvent.change(screen.getByLabelText(/audience segment/i), { target: { value: 'seg-1' } });
  }
}

const BLOCKED = /no marketing send step/i;
const enrollButton = () => screen.getByRole('button', { name: /^enroll$/i });

beforeEach(() => {
  vi.clearAllMocks();
  mockWorkflows.list = [];
  setPerms(true, true);
});
afterEach(cleanup);

describe('SequencesListPage gating', () => {
  it('shows a spinner (not the denied panel) while permissions load', () => {
    setPerms(false, false);
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /enroll a segment/i })).not.toBeInTheDocument();
  });

  it('shows the access-denied panel without the capability', () => {
    setPerms(false, true);
    renderPage();
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /enroll a segment/i })).not.toBeInTheDocument();
  });

  it('renders the sequences surface with the capability', () => {
    renderPage();
    expect(screen.getByRole('heading', { name: 'Sequences' })).toBeInTheDocument();
    expect(screen.getByText('No sequence enrollments yet')).toBeInTheDocument();
  });
});

describe('SequencesListPage — marketing-send detection over the steps tree', () => {
  it('accepts a marketing send at the top level', async () => {
    await pickWorkflow(makeWorkflow({ steps: [delayStep('d1'), marketingSend('a1')] }));

    expect(screen.queryByText(BLOCKED)).not.toBeInTheDocument();
    expect(enrollButton()).toBeEnabled();
  });

  it('finds a marketing send nested in an If/Else NO branch (a flat scan would miss it)', async () => {
    // Root holds only a delay and a terminal condition; the ONLY marketing send is two
    // levels down the No side. This is the case the pre-R5 `actions.some()` got for
    // free from the server-side flattening, and that the recursion has to re-earn.
    await pickWorkflow(makeWorkflow({
      steps: [
        delayStep('d1'),
        ifElse(
          'c1',
          [transactionalSend('a-yes')],
          [ifElse('c2', [delayStep('d2')], [marketingSend('a-deep')])],
        ),
      ],
    }));

    expect(screen.queryByText(BLOCKED)).not.toBeInTheDocument();
    expect(enrollButton()).toBeEnabled();
  });

  it('finds a marketing send nested in an If/Else YES branch', async () => {
    await pickWorkflow(makeWorkflow({
      steps: [ifElse('c1', [marketingSend('a-yes')], [transactionalSend('a-no')])],
    }));

    expect(screen.queryByText(BLOCKED)).not.toBeInTheDocument();
  });

  it('blocks a workflow whose only sends are transactional, not marketing', async () => {
    await pickWorkflow(makeWorkflow({
      steps: [transactionalSend('a1'), ifElse('c1', [transactionalSend('a2')], [delayStep('d1')])],
    }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
    expect(enrollButton()).toBeDisabled();
  });

  it('blocks a workflow with no email action anywhere in the tree', async () => {
    await pickWorkflow(makeWorkflow({
      steps: [
        delayStep('d1'),
        { id: 'a1', type: 'action', action: { id: 'a1', type: 'create_task', params: { title: 'Call them' } } },
      ],
    }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
    expect(enrollButton()).toBeDisabled();
  });

  it('blocks a workflow with an empty steps array', async () => {
    await pickWorkflow(makeWorkflow({ steps: [] }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
    expect(enrollButton()).toBeDisabled();
  });

  it('blocks a workflow whose steps field is absent from the response', async () => {
    await pickWorkflow(makeWorkflow({ steps: undefined }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
  });

  it('blocks a workflow whose steps arrived as JSON null (Go nil slice)', async () => {
    await pickWorkflow(makeWorkflow({ steps: null as unknown as WorkflowStep[] }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
  });

  it('blocks — and does not crash the page — on a malformed steps value', async () => {
    // Junk in the `steps` position must degrade to "not enrollable", never throw: this
    // component sits under no error boundary, so an exception white-screens the whole
    // route instead of greying out one option in a picker.
    await pickWorkflow(makeWorkflow({ steps: '{}' as unknown as WorkflowStep[] }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
    // Still a live, fully-rendered dialog — the walk degraded, it did not throw.
    expect(screen.getByRole('dialog', { name: 'Enroll a segment' })).toBeInTheDocument();
  });

  it('blocks on malformed nodes inside an otherwise well-formed tree', async () => {
    // A null element, an action step carrying no action, a send whose params are null,
    // and non-array branches — every one of these is a dereference the walk performs.
    await pickWorkflow(makeWorkflow({
      steps: [
        null,
        { id: 's1', type: 'action' },
        { id: 's2', type: 'action', action: { id: 's2', type: 'send_email', params: null } },
        { id: 'c1', type: 'condition', yes_steps: null, no_steps: 'nope' },
      ] as unknown as WorkflowStep[],
    }));

    expect(screen.getByText(BLOCKED)).toBeInTheDocument();
    // Still a live, fully-rendered dialog — the walk degraded, it did not throw.
    expect(screen.getByRole('dialog', { name: 'Enroll a segment' })).toBeInTheDocument();
  });

  it('keeps Enroll disabled until a segment is chosen, even on an eligible workflow', async () => {
    await pickWorkflow(makeWorkflow({ steps: [marketingSend('a1')] }), { segment: false });

    expect(screen.queryByText(BLOCKED)).not.toBeInTheDocument();
    expect(enrollButton()).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/audience segment/i), { target: { value: 'seg-1' } });

    expect(enrollButton()).toBeEnabled();
  });
});
