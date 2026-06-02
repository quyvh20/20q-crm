import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { RunHistory } from './RunHistory';
import { getWorkflow, getWorkflowRuns, getRunDetail } from './api';
import type { Workflow, WorkflowRun, ActionLog, RunDetailResponse } from './types';

/**
 * RunHistory — "Run Now" deep-link tests.
 *
 * The "Run started" toast (WorkflowList) and the builder's Run Now navigate to
 * /workflows/:id/history with `state: { highlightRunId }`. RunHistory consumes that
 * state to auto-open the specific run's detail (load its action logs) once the run
 * appears in the loaded list — turning "go to history" into "go to that run's detail".
 *
 * We mock `./api` so no network call occurs, and drive the route via MemoryRouter so
 * useParams (the workflow id) and useLocation (the highlight state) resolve.
 */

vi.mock('./api', () => ({
  getWorkflow: vi.fn(),
  getWorkflowRuns: vi.fn(),
  getRunDetail: vi.fn(),
}));

const mockGetWorkflow = vi.mocked(getWorkflow);
const mockGetWorkflowRuns = vi.mocked(getWorkflowRuns);
const mockGetRunDetail = vi.mocked(getRunDetail);

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

function makeRun(over: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    id: 'run-1',
    workflow_id: 'wf-1',
    workflow_version: 1,
    org_id: 'org-1',
    status: 'completed',
    trigger_context: {},
    current_action_idx: 0,
    completed_actions: null,
    retry_count: 0,
    created_at: '2024-02-01T00:00:00Z',
    ...over,
  };
}

function makeLog(over: Partial<ActionLog> = {}): ActionLog {
  return {
    id: 'log-1',
    run_id: 'run-2',
    action_idx: 0,
    action_type: 'send_email',
    status: 'success',
    attempt_no: 1,
    duration_ms: 12,
    created_at: '2024-02-01T00:00:01Z',
    ...over,
  };
}

function renderHistory(state?: { highlightRunId?: string }) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: '/workflows/wf-1/history', state }]}>
      <Routes>
        <Route path="/workflows/:id/history" element={<RunHistory />} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  // jsdom doesn't implement scrollIntoView — provide a no-op so the highlight effect
  // doesn't throw.
  Element.prototype.scrollIntoView = vi.fn();

  mockGetWorkflow.mockResolvedValue(makeWorkflow());
  mockGetWorkflowRuns.mockResolvedValue({
    runs: [
      makeRun({ id: 'run-1', status: 'completed' }),
      makeRun({ id: 'run-2', status: 'completed' }),
    ],
    total: 2,
  });
  mockGetRunDetail.mockResolvedValue({
    run: makeRun({ id: 'run-2' }),
    action_logs: [makeLog({ id: 'log-1', run_id: 'run-2', action_type: 'send_email' })],
  } as RunDetailResponse);
});

describe('RunHistory — Run Now deep link (highlightRunId)', () => {
  it('auto-opens the highlighted run’s detail on arrival', async () => {
    renderHistory({ highlightRunId: 'run-2' });

    // The highlighted run's detail is fetched without any click...
    await waitFor(() => expect(mockGetRunDetail).toHaveBeenCalledWith('run-2'));
    // ...and its action log is rendered (detail expanded).
    expect(await screen.findByText(/Step 1: Send Email/i)).toBeInTheDocument();
  });

  it('does not auto-open any run when no highlightRunId is supplied', async () => {
    renderHistory();

    // Runs load, but nothing is expanded and no detail is fetched.
    await waitFor(() => expect(mockGetWorkflowRuns).toHaveBeenCalled());
    await waitFor(() => expect(screen.getByText('Welcome Email')).toBeInTheDocument());
    expect(mockGetRunDetail).not.toHaveBeenCalled();
  });

  it('ignores a highlightRunId that is not in the loaded run list', async () => {
    renderHistory({ highlightRunId: 'run-does-not-exist' });

    await waitFor(() => expect(mockGetWorkflowRuns).toHaveBeenCalled());
    // Give the highlight effect a chance to (not) fire.
    await new Promise((r) => setTimeout(r, 50));
    expect(mockGetRunDetail).not.toHaveBeenCalled();
  });
});

// ── P19 live polling: a run transitions pending → running → completed ─────────
describe('RunHistory — live polling status transitions (P19)', () => {
  it('reflects a run going pending → running → completed and stops polling once terminal', async () => {
    vi.useFakeTimers();
    try {
      mockGetWorkflow.mockResolvedValue(makeWorkflow());
      // Each fetch (mount + every 5s poll) returns the next status for the same run.
      mockGetWorkflowRuns
        .mockResolvedValueOnce({ runs: [makeRun({ id: 'run-1', status: 'pending' })], total: 1 })
        .mockResolvedValueOnce({ runs: [makeRun({ id: 'run-1', status: 'running' })], total: 1 })
        .mockResolvedValueOnce({ runs: [makeRun({ id: 'run-1', status: 'completed' })], total: 1 });

      renderHistory();

      // Initial fetch → pending.
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(screen.getByText('pending')).toBeInTheDocument();

      // After 5s the poll re-fetches → running.
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText('running')).toBeInTheDocument();

      // After another 5s → completed.
      await act(async () => { await vi.advanceTimersByTimeAsync(5000); });
      expect(screen.getByText('completed')).toBeInTheDocument();

      // Polling stops once no run is pending/running: no further fetches after terminal.
      const callsAtTerminal = mockGetWorkflowRuns.mock.calls.length;
      await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
      expect(mockGetWorkflowRuns.mock.calls.length).toBe(callsAtTerminal);
    } finally {
      vi.useRealTimers();
    }
  });
});
