import React, { useState } from 'react';
import { EntityPicker, type EntityCandidate } from './EntityPicker';
import Modal from '../../components/common/Modal';
import { runNowWorkflow } from './api';
import type { Workflow } from './types';
import { TRIGGER_LABELS } from './types';

export interface RunNowModalProps {
  /** The workflow this Run Now interaction targets. Only id/name/trigger are read, so
   *  callers without a full Workflow (e.g. the builder, working from store state) can
   *  pass just those fields. */
  workflow: Pick<Workflow, 'id' | 'name' | 'trigger'>;
  /** Called to dismiss the modal (cancel, backdrop, close button, or post-success). */
  onClose: () => void;
  /**
   * Called with the created Workflow_Run id after a successful run so the host
   * can surface a "view run" affordance (e.g. a toast linking to run history).
   */
  onSuccess: (runId: string) => void;
}

/**
 * Maps a workflow trigger type to the entity kind compatible with a Run Now,
 * mirroring the backend `entityKindForTrigger`: contact-triggered workflows
 * (`contact_created`, `contact_updated`, `webhook_inbound`) run against a
 * contact; `deal_stage_changed` runs against a deal. Any other trigger type is
 * unsupported for Run Now and yields `null`.
 *
 * `webhook_inbound` maps to a contact by design: the production inbound-webhook path
 * upserts a contact and emits a contact-shaped event, so Run Now reuses the contact picker
 * to reproduce it — rather than disabling Run Now or asking the user to paste raw JSON.
 */
export function entityKindForTrigger(triggerType: string | undefined): 'contact' | 'deal' | null {
  switch (triggerType) {
    case 'contact_created':
    case 'contact_updated':
    case 'webhook_inbound':
      return 'contact';
    case 'deal_stage_changed':
      return 'deal';
    default:
      return null;
  }
}

/**
 * Reports whether a caller may Run Now the given workflow, mirroring the backend
 * `authorizeRunNow` permission model so the UI can hide the control when the server would
 * reject it with 403. A caller holding the `workflows.run_any` capability (P6 — owner /
 * admin / manager by default, but any custom role an admin grants it to) may run any
 * workflow in the org; any other caller may run ONLY a workflow they created — the creator
 * allowance. An unknown caller id never satisfies the creator check.
 *
 * The backend remains the source of truth; this is a UX affordance, not the security
 * boundary (the endpoint still enforces authorization).
 */
export function canRunWorkflowNow(
  canRunAny: boolean,
  userId: string | undefined,
  workflow: { created_by: string | null },
): boolean {
  if (canRunAny) return true;
  return !!userId && userId === workflow.created_by;
}

/** Whether the in-builder "Run Now" control should be shown, and whether it's enabled. */
export interface RunNowAvailability {
  /** Render the control at all. */
  visible: boolean;
  /** Enabled vs. disabled-with-tooltip. */
  enabled: boolean;
}

/**
 * Computes the in-builder "Run Now" control state from builder store fields. The control
 * is visible only for a SAVED workflow (Run Now executes the persisted version, so an
 * unsaved draft has nothing to run) that the caller is authorized to run (mirroring
 * canRunWorkflowNow). It is disabled while there are unsaved edits — running would execute
 * the last-saved version, not what's on screen — prompting the user to save first.
 *
 * Pure (no React/store) so the builder's gating can be unit-tested without rendering it.
 */
export function builderRunNowAvailability(opts: {
  workflowId: string | null;
  createdBy: string | null;
  trigger: { type: string } | null;
  isDirty: boolean;
  canRunAny: boolean;
  userId: string | undefined;
}): RunNowAvailability {
  const visible =
    !!opts.workflowId &&
    !!opts.trigger &&
    canRunWorkflowNow(opts.canRunAny, opts.userId, { created_by: opts.createdBy });
  return { visible, enabled: visible && !opts.isDirty };
}

/**
 * RunNowModal — confirmation modal for manually executing a single workflow
 * against one real contact or deal. It warns that the run has real side effects,
 * hosts an EntityPicker constrained to the workflow's compatible entity kind, and
 * only enables the confirm control once the user has actively selected a sample
 * entity. On confirm it submits via `runNowWorkflow`, closes and surfaces the
 * created run on success, or shows the server error and keeps the selection for
 * retry on failure.
 */
export const RunNowModal: React.FC<RunNowModalProps> = ({ workflow, onClose, onSuccess }) => {
  const kind = entityKindForTrigger(workflow.trigger?.type);
  // The actively-selected sample entity. Starts null so confirm is disabled
  // until the user makes an explicit selection (never pre-populated).
  const [selected, setSelected] = useState<EntityCandidate | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const triggerLabel = TRIGGER_LABELS[workflow.trigger?.type] || workflow.trigger?.type || 'Unknown';

  // Confirm is enabled only while a valid entity is selected, the trigger is
  // supported, and no submit is in flight. If the selection is cleared or
  // becomes invalid, confirm re-disables.
  const canConfirm = !!selected && !!kind && !submitting;

  const handleDismiss = () => {
    // Dismissing without confirming closes without calling the API. Block
    // dismissal while a submit is in flight to avoid confusing mid-request state.
    if (submitting) return;
    onClose();
  };

  const handleConfirm = async () => {
    if (!canConfirm || !selected || !kind) return;
    setSubmitting(true);
    setError(null);
    try {
      const entity = kind === 'contact'
        ? { contact_id: selected.id }
        : { deal_id: selected.id };
      const result = await runNowWorkflow(workflow.id, entity);
      // Surface the created run, then close. These may occur independently.
      onSuccess(result.id);
      onClose();
    } catch (e) {
      // Keep the selection so the user can retry; show the returned error.
      setError(e instanceof Error ? e.message : 'Failed to run workflow');
      setSubmitting(false);
    }
  };

  return (
    // Shared Radix modal (U7): Escape, focus trap/restore and aria for free.
    // handleDismiss (not onClose) so every exit path — X, Escape, outside click —
    // keeps the existing "no dismissal mid-submit" guard.
    //
    // The panel used to be hardcoded dark (bg-gray-900); it's now the themed
    // bg-card, so the body's dark-only grays had to become semantic tokens or
    // they'd render light-on-light for anyone using the light theme.
    <Modal
      open
      onClose={handleDismiss}
      title="▶ Run Now"
      description={`${workflow.name} · ⚡ ${triggerLabel}`}
      size="lg"
      padded={false}
      dismissable={!submitting}
    >
      <>
        {/* Body */}
        <div className="px-6 py-5 space-y-4">
          {/* Real-side-effect warning banner (Req 8.3) */}
          <div
            role="alert"
            className="flex items-start gap-3 rounded-xl border border-amber-500/40 bg-amber-500/10 px-4 py-3"
          >
            <span className="text-amber-500 text-base leading-none mt-0.5" aria-hidden="true">⚠️</span>
            <p className="text-sm text-amber-700 dark:text-amber-200">
              This executes the workflow <strong>for real</strong> against the selected record.
              All side effects happen — emails are sent, tasks are created, fields are updated, and
              webhooks fire. A run will appear in this workflow&apos;s history.
            </p>
          </div>

          {kind ? (
            <div>
              <label className="block text-sm font-medium text-foreground mb-2">
                Select a {kind} to run against
              </label>
              <EntityPicker kind={kind} onSelect={setSelected} />
            </div>
          ) : (
            <p className="text-sm text-muted-foreground py-2">
              Run Now isn&apos;t available for the <span className="text-foreground">{triggerLabel}</span>{' '}
              trigger. It supports contact- and deal-triggered workflows only.
            </p>
          )}

          {selected && (
            <p className="text-xs text-muted-foreground">
              Selected: <span className="text-foreground font-medium">{selected.label}</span>
            </p>
          )}

          {/* Failure message (Req 10.4) — selection is retained for retry. */}
          {error && (
            <p role="alert" className="text-sm text-red-600 dark:text-red-400">
              {error}
            </p>
          )}
        </div>

        {/* Footer */}
        <div className="px-6 py-4 bg-muted/30 border-t border-border flex justify-end gap-3">
          <button
            type="button"
            onClick={handleDismiss}
            disabled={submitting}
            className="px-4 py-2 text-sm font-medium rounded-lg text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={handleConfirm}
            disabled={!canConfirm}
            className="px-4 py-2 text-sm font-medium rounded-lg bg-gradient-to-r from-indigo-500 to-purple-500 text-white hover:from-indigo-600 hover:to-purple-600 transition-all shadow-lg shadow-indigo-500/20 disabled:opacity-40 disabled:cursor-not-allowed disabled:shadow-none flex items-center gap-2"
          >
            {submitting && (
              <span className="w-4 h-4 border-2 border-white/40 border-t-white rounded-full animate-spin" />
            )}
            {submitting ? 'Running…' : 'Run Now'}
          </button>
        </div>
      </>
    </Modal>
  );
};

export default RunNowModal;
