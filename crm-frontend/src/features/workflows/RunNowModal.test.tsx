import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { RunNowModal, canRunWorkflowNow, builderRunNowAvailability } from './RunNowModal';
import { runNowWorkflow } from './api';
import type { Workflow } from './types';

/**
 * RunNowModal tests — Requirements 8.3, 8.4, 10.2, 10.3, 10.4, 10.5.
 *
 * The modal owns the confirmation flow: it shows a real-side-effect warning,
 * hosts an EntityPicker constrained to the workflow's compatible entity kind,
 * and submits via `runNowWorkflow` once an entity is actively selected.
 *
 * We mock `./api` so no real network call occurs, and we stub `./EntityPicker`
 * with a lightweight double (its own selection behavior is covered by
 * EntityPicker.test.tsx). The stub exposes the `kind` it received and a single
 * button that drives an explicit selection, so these tests stay focused on the
 * modal's confirm / dismiss / in-flight / error logic.
 */

vi.mock('./api', () => ({
  runNowWorkflow: vi.fn(),
}));

// Lightweight EntityPicker double: surfaces the `kind` the modal derived from
// the trigger type and a button that performs an explicit selection (mirroring
// the real picker's explicit-click contract). The selected id differs by kind
// so the contact_id / deal_id payload can be asserted.
vi.mock('./EntityPicker', async () => {
  const React = await import('react');
  return {
    EntityPicker: ({
      kind,
      onSelect,
    }: {
      kind: 'contact' | 'deal';
      onSelect: (e: { id: string; label: string }) => void;
    }) =>
      React.createElement(
        'div',
        { 'data-testid': 'entity-picker' },
        React.createElement('span', { 'data-testid': 'picker-kind' }, kind),
        React.createElement(
          'button',
          {
            type: 'button',
            onClick: () =>
              onSelect(
                kind === 'contact'
                  ? { id: 'contact-123', label: 'Ada Lovelace' }
                  : { id: 'deal-xyz', label: 'Acme Expansion' },
              ),
          },
          'Select sample entity',
        ),
      ),
  };
});

const mockRunNowWorkflow = vi.mocked(runNowWorkflow);

/** A deferred promise so tests can control when `runNowWorkflow` settles. */
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
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

beforeEach(() => {
  vi.clearAllMocks();
  // Safe default so a test that doesn't stub the call never rejects unexpectedly.
  mockRunNowWorkflow.mockResolvedValue({ id: 'run-default', status: 'pending' });
});

// ── Req 8.3: real-side-effect warning is shown ────────────────────────
describe('RunNowModal — warning', () => {
  it('shows a warning that the run executes the workflow with real side effects (Req 8.3)', () => {
    render(<RunNowModal workflow={makeWorkflow()} onClose={vi.fn()} onSuccess={vi.fn()} />);

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent(/for real/i);
    expect(alert).toHaveTextContent(/side effects/i);
  });
});

// ── Req 8.4: dismiss without confirm closes and makes NO API call ─────
describe('RunNowModal — dismiss without confirming', () => {
  it('closes via Cancel without initiating a run (Req 8.4)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onSuccess = vi.fn();

    render(<RunNowModal workflow={makeWorkflow()} onClose={onClose} onSuccess={onSuccess} />);

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onClose).toHaveBeenCalledTimes(1);
    expect(mockRunNowWorkflow).not.toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();
  });

  it('keeps confirm disabled until an entity is actively selected (Req 9.5 boundary)', () => {
    render(<RunNowModal workflow={makeWorkflow()} onClose={vi.fn()} onSuccess={vi.fn()} />);

    expect(screen.getByRole('button', { name: 'Run Now' })).toBeDisabled();
  });
});

// ── Req 10.2: confirm calls runNowWorkflow with workflow + entity ids ──
describe('RunNowModal — confirm submits the run', () => {
  it('calls runNowWorkflow with the workflow id and selected contact_id for a contact trigger (Req 10.2)', async () => {
    const user = userEvent.setup();
    render(
      <RunNowModal
        workflow={makeWorkflow({ id: 'wf-contact', trigger: { type: 'contact_created' } })}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    // The modal derived the contact kind from the trigger type.
    expect(screen.getByTestId('picker-kind')).toHaveTextContent('contact');

    await user.click(screen.getByRole('button', { name: 'Select sample entity' }));
    await user.click(screen.getByRole('button', { name: 'Run Now' }));

    expect(mockRunNowWorkflow).toHaveBeenCalledTimes(1);
    expect(mockRunNowWorkflow).toHaveBeenCalledWith('wf-contact', { contact_id: 'contact-123' });
  });

  it('calls runNowWorkflow with the workflow id and selected deal_id for a deal trigger (Req 10.2)', async () => {
    const user = userEvent.setup();
    render(
      <RunNowModal
        workflow={makeWorkflow({ id: 'wf-deal', trigger: { type: 'deal_stage_changed' } })}
        onClose={vi.fn()}
        onSuccess={vi.fn()}
      />,
    );

    // The modal derived the deal kind from the trigger type.
    expect(screen.getByTestId('picker-kind')).toHaveTextContent('deal');

    await user.click(screen.getByRole('button', { name: 'Select sample entity' }));
    await user.click(screen.getByRole('button', { name: 'Run Now' }));

    expect(mockRunNowWorkflow).toHaveBeenCalledTimes(1);
    expect(mockRunNowWorkflow).toHaveBeenCalledWith('wf-deal', { deal_id: 'deal-xyz' });
  });
});

// ── Req 10.3: success closes and surfaces the created run ─────────────
describe('RunNowModal — success', () => {
  it('reports the created run id and closes on success (Req 10.3)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onSuccess = vi.fn();
    mockRunNowWorkflow.mockResolvedValue({ id: 'run-789', status: 'pending' });

    render(<RunNowModal workflow={makeWorkflow()} onClose={onClose} onSuccess={onSuccess} />);

    await user.click(screen.getByRole('button', { name: 'Select sample entity' }));
    await user.click(screen.getByRole('button', { name: 'Run Now' }));

    await waitFor(() => expect(onSuccess).toHaveBeenCalledTimes(1));
    // The view-run affordance is driven by the created run id handed to the host.
    expect(onSuccess).toHaveBeenCalledWith('run-789');
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

// ── Req 10.4: failure shows the error and retains the selection ───────
describe('RunNowModal — failure', () => {
  it('shows the returned error message and keeps the selection for retry (Req 10.4)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onSuccess = vi.fn();
    mockRunNowWorkflow.mockRejectedValue(new Error('Sample entity not found'));

    render(<RunNowModal workflow={makeWorkflow()} onClose={onClose} onSuccess={onSuccess} />);

    await user.click(screen.getByRole('button', { name: 'Select sample entity' }));
    await user.click(screen.getByRole('button', { name: 'Run Now' }));

    // The server error is surfaced.
    await waitFor(() => expect(screen.getByText('Sample entity not found')).toBeInTheDocument());

    // The modal did not close and did not report success.
    expect(onClose).not.toHaveBeenCalled();
    expect(onSuccess).not.toHaveBeenCalled();

    // The selection is retained, so confirm is enabled again to retry.
    expect(screen.getByRole('button', { name: 'Run Now' })).toBeEnabled();

    // Retrying issues another request with the same selection.
    mockRunNowWorkflow.mockResolvedValue({ id: 'run-retry', status: 'pending' });
    await user.click(screen.getByRole('button', { name: 'Run Now' }));
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith('run-retry'));
    expect(mockRunNowWorkflow).toHaveBeenCalledTimes(2);
    expect(mockRunNowWorkflow).toHaveBeenLastCalledWith('wf-1', { contact_id: 'contact-123' });
  });
});

// ── Req 10.5: in-flight state disables confirm + prevents dup submits ──
describe('RunNowModal — in-flight submission', () => {
  it('disables confirm while submitting and prevents duplicate submissions (Req 10.5)', async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    const onSuccess = vi.fn();
    const pending = deferred<{ id: string; status: string }>();
    mockRunNowWorkflow.mockReturnValue(pending.promise);

    render(<RunNowModal workflow={makeWorkflow()} onClose={onClose} onSuccess={onSuccess} />);

    await user.click(screen.getByRole('button', { name: 'Select sample entity' }));
    await user.click(screen.getByRole('button', { name: 'Run Now' }));

    // While the request is in flight the confirm control indicates submission
    // and is disabled, and Cancel is disabled too.
    const submittingBtn = await screen.findByRole('button', { name: /Running/i });
    expect(submittingBtn).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();

    // Attempting to confirm again does not issue a second request.
    await user.click(submittingBtn);
    expect(mockRunNowWorkflow).toHaveBeenCalledTimes(1);

    // Settle the in-flight request so the success path completes cleanly.
    pending.resolve({ id: 'run-1', status: 'pending' });
    await waitFor(() => expect(onSuccess).toHaveBeenCalledWith('run-1'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

// ── canRunWorkflowNow — mirrors the backend authorizeRunNow permission model ──
describe('canRunWorkflowNow', () => {
  const wf = { created_by: 'creator-1' };

  it('allows a caller with workflows.run_any to run any workflow regardless of creator', () => {
    expect(canRunWorkflowNow(true, 'someone-else', wf)).toBe(true);
    expect(canRunWorkflowNow(true, undefined, wf)).toBe(true);
  });

  it('allows a caller without run_any to run only a workflow they created', () => {
    expect(canRunWorkflowNow(false, 'creator-1', wf)).toBe(true);
  });

  it('denies a caller without run_any on a workflow created by someone else', () => {
    expect(canRunWorkflowNow(false, 'someone-else', wf)).toBe(false);
  });

  it('denies when the caller id is unknown (never satisfies the creator check)', () => {
    expect(canRunWorkflowNow(false, undefined, wf)).toBe(false);
  });
});

// ── builderRunNowAvailability — in-builder show/enable gating ──────────────────
describe('builderRunNowAvailability', () => {
  const base = {
    workflowId: 'wf-1',
    createdBy: 'creator-1',
    trigger: { type: 'contact_created' } as { type: string } | null,
    isDirty: false,
    canRunAny: true,
    userId: 'someone' as string | undefined,
  };

  it('is hidden for an unsaved draft (no workflowId)', () => {
    expect(builderRunNowAvailability({ ...base, workflowId: null })).toEqual({
      visible: false,
      enabled: false,
    });
  });

  it('is hidden when there is no trigger', () => {
    expect(builderRunNowAvailability({ ...base, trigger: null })).toEqual({
      visible: false,
      enabled: false,
    });
  });

  it('is hidden when the caller is not authorized (no run_any, not creator)', () => {
    expect(
      builderRunNowAvailability({ ...base, canRunAny: false, userId: 'other', createdBy: 'creator-1' }),
    ).toEqual({ visible: false, enabled: false });
  });

  it('is visible but disabled while there are unsaved edits', () => {
    expect(builderRunNowAvailability({ ...base, isDirty: true })).toEqual({
      visible: true,
      enabled: false,
    });
  });

  it('is visible and enabled for a saved, clean, authorized workflow', () => {
    expect(builderRunNowAvailability(base)).toEqual({ visible: true, enabled: true });
  });

  it('honors the creator allowance: a creator without run_any sees it (enabled when clean)', () => {
    expect(
      builderRunNowAvailability({ ...base, canRunAny: false, userId: 'creator-1', createdBy: 'creator-1' }),
    ).toEqual({ visible: true, enabled: true });
  });
});
