import React, { useEffect, useState, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { getWorkflows, deleteWorkflow, toggleWorkflow } from './api';
import { RunNowModal, canRunWorkflowNow } from './RunNowModal';
import type { Workflow } from './types';
import { TRIGGER_LABELS, STATUS_COLORS } from './types';
import { useAuth } from '../../lib/auth';

/** Optional actionable link rendered inside a toast (e.g. "View run"). */
interface ToastAction {
  label: string;
  onClick: () => void;
}

export const WorkflowList: React.FC = () => {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [filterActive, setFilterActive] = useState<boolean | undefined>(undefined);
  const [toast, setToast] = useState<{ message: string; type: 'error' | 'success'; action?: ToastAction } | null>(null);
  const [runNowTarget, setRunNowTarget] = useState<Workflow | null>(null);
  const navigate = useNavigate();
  const { user, currentRole } = useAuth();

  const showToast = (
    message: string,
    type: 'error' | 'success' = 'error',
    action?: ToastAction,
  ) => {
    setToast({ message, type, action });
    // Success toasts with an action stay a bit longer so the user can click through.
    setTimeout(() => setToast(null), action ? 6000 : 3000);
  };

  const fetchWorkflows = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await getWorkflows({ active: filterActive, page, size: 20 });
      setWorkflows(resp.workflows || []);
      setTotal(resp.total);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  }, [page, filterActive]);

  useEffect(() => {
    fetchWorkflows();
  }, [fetchWorkflows]);

  // Optimistic toggle — flip UI immediately, revert on error
  const handleToggle = async (wf: Workflow) => {
    const previousState = wf.is_active;
    // Optimistic update
    setWorkflows((prev) =>
      prev.map((w) => (w.id === wf.id ? { ...w, is_active: !previousState } : w))
    );
    try {
      await toggleWorkflow(wf.id);
    } catch (e: any) {
      // Revert on error
      setWorkflows((prev) =>
        prev.map((w) => (w.id === wf.id ? { ...w, is_active: previousState } : w))
      );
      showToast(e.message || 'Failed to toggle workflow', 'error');
    }
  };

  const handleDelete = async (wf: Workflow) => {
    if (!confirm(`Delete "${wf.name}"?`)) return;
    try {
      await deleteWorkflow(wf.id);
      setWorkflows((prev) => prev.filter((w) => w.id !== wf.id));
    } catch (e: any) {
      showToast(e.message || 'Failed to delete workflow', 'error');
    }
  };

  const totalPages = Math.ceil(total / 20);

  const statusLabel = (status: string | null) => {
    if (!status) return null;
    const color = STATUS_COLORS[status] || '#9CA3AF';
    return (
      <span
        className="inline-flex items-center gap-1.5 text-xs font-medium"
        style={{ color }}
      >
        <span
          className="w-1.5 h-1.5 rounded-full"
          style={{ backgroundColor: color }}
        />
        {status.charAt(0).toUpperCase() + status.slice(1)}
      </span>
    );
  };

  return (
    <div className="max-w-5xl mx-auto py-8 px-4">
      {/* Run Now modal — opens for the targeted workflow, clears target on close */}
      {runNowTarget && (
        <RunNowModal
          workflow={runNowTarget}
          onClose={() => setRunNowTarget(null)}
          onSuccess={(runId) => {
            const wf = runNowTarget;
            showToast(
              `Run started for "${wf.name}"`,
              'success',
              {
                label: 'View run',
                onClick: () => navigate(`/workflows/${wf.id}/history`, { state: { highlightRunId: runId } }),
              },
            );
          }}
        />
      )}

      {/* Toast notification */}
      {toast && (
        <div
          className={`fixed top-4 right-4 z-50 px-4 py-3 rounded-xl shadow-lg text-sm font-medium transition-all animate-in slide-in-from-top-2 flex items-center gap-3 ${
            toast.type === 'error'
              ? 'bg-red-500/90 text-white'
              : 'bg-emerald-500/90 text-white'
          }`}
        >
          <span>{toast.message}</span>
          {toast.action && (
            <button
              onClick={() => {
                toast.action!.onClick();
                setToast(null);
              }}
              className="underline underline-offset-2 font-semibold hover:opacity-80 transition-opacity whitespace-nowrap"
            >
              {toast.action.label}
            </button>
          )}
        </div>
      )}

      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-white">Workflow Automations</h1>
          <p className="text-sm text-gray-400 mt-1">Automate repetitive tasks with triggers and actions</p>
        </div>
        <button
          onClick={() => navigate('/workflows/new')}
          className="px-4 py-2.5 rounded-xl bg-gradient-to-r from-indigo-500 to-purple-500 text-white text-sm font-medium hover:from-indigo-600 hover:to-purple-600 transition-all shadow-lg shadow-indigo-500/20"
        >
          + New Workflow
        </button>
      </div>

      {/* Filter */}
      <div className="flex gap-2 mb-6">
        {[
          { label: 'All', value: undefined },
          { label: 'Active', value: true },
          { label: 'Inactive', value: false },
        ].map((opt) => (
          <button
            key={String(opt.value)}
            onClick={() => { setFilterActive(opt.value); setPage(1); }}
            className={`px-3 py-1.5 rounded-lg text-sm font-medium transition-colors ${
              filterActive === opt.value
                ? 'bg-indigo-500 text-white'
                : 'bg-gray-800 text-gray-400 hover:text-white'
            }`}
          >
            {opt.label}
          </button>
        ))}
        <span className="ml-auto text-sm text-gray-500 self-center">{total} workflow{total !== 1 ? 's' : ''}</span>
      </div>

      {/* Table */}
      {loading ? (
        <div className="flex justify-center py-16">
          <div className="w-8 h-8 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
        </div>
      ) : workflows.length === 0 ? (
        <div className="text-center py-16 text-gray-500">
          <p className="text-lg mb-2">No workflows yet</p>
          <p className="text-sm">Create your first automation to get started</p>
        </div>
      ) : (
        <div className="space-y-3">
          {workflows.map((wf) => (
            <div
              key={wf.id}
              className="bg-gray-800/60 border border-gray-700 rounded-xl p-4 hover:border-gray-600 transition-all group"
            >
              <div className="flex items-center gap-4">
                {/* Status dot */}
                <div className="relative">
                  <div
                    className={`w-3 h-3 rounded-full ${wf.is_active ? 'bg-emerald-500' : 'bg-gray-600'}`}
                  />
                  {wf.is_active && (
                    <div className="absolute inset-0 w-3 h-3 rounded-full bg-emerald-500 animate-ping opacity-30" />
                  )}
                </div>

                {/* Info */}
                <div
                  className="flex-1 min-w-0 cursor-pointer"
                  onClick={() => navigate(`/workflows/${wf.id}`)}
                >
                  <h3 className="text-sm font-semibold text-white truncate group-hover:text-indigo-400 transition-colors">
                    {wf.name}
                  </h3>
                  <div className="flex items-center gap-3 mt-1">
                    <span className="text-xs text-gray-500">
                      ⚡ {TRIGGER_LABELS[wf.trigger?.type] || 'Unknown'}
                    </span>
                    <span className="text-xs text-gray-500">
                      {wf.action_count || wf.actions?.length || 0} action{(wf.action_count || wf.actions?.length || 0) !== 1 ? 's' : ''}
                    </span>
                    <span className="text-xs text-gray-500">
                      v{wf.version}
                    </span>
                    {wf.last_run_status && (
                      <>
                        <span className="text-xs text-gray-700">|</span>
                        {statusLabel(wf.last_run_status)}
                        {wf.last_run_at && (
                          <span className="text-xs text-gray-600">
                            {new Date(wf.last_run_at).toLocaleDateString()}
                          </span>
                        )}
                      </>
                    )}
                    {!wf.last_run_status && (
                      <span className="text-xs text-gray-600 italic">No runs</span>
                    )}
                  </div>
                </div>

                {/* Actions */}
                <div className="flex items-center gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                  {/* Run Now is shown only to callers the backend would authorize
                      (owner/admin/manager, or the workflow's creator) so a forbidden
                      caller isn't offered a control that would 403. */}
                  {canRunWorkflowNow(currentRole, user?.id, wf) && (
                    <button
                      onClick={() => setRunNowTarget(wf)}
                      className="px-3 py-1.5 rounded-lg text-xs font-medium bg-indigo-500/10 text-indigo-300 hover:bg-indigo-500/20 transition-colors"
                      title="Run this workflow now against a sample record"
                    >
                      ▶ Run Now
                    </button>
                  )}
                  <button
                    onClick={() => navigate(`/workflows/${wf.id}/history`)}
                    className="px-3 py-1.5 rounded-lg text-xs bg-gray-700 text-gray-300 hover:text-white hover:bg-gray-600 transition-colors"
                    title="View run history"
                  >
                    📊 History
                  </button>
                  {/* Duplicate (P23): open the builder on a fresh, unsaved "Copy of …"
                      draft cloned from this workflow. The source id rides in router
                      state; the builder clones it on mount (see duplicateFrom). */}
                  <button
                    onClick={() => navigate('/workflows/new', { state: { duplicateFromId: wf.id } })}
                    className="px-3 py-1.5 rounded-lg text-xs bg-gray-700 text-gray-300 hover:text-white hover:bg-gray-600 transition-colors"
                    title="Duplicate this workflow"
                  >
                    ⧉ Duplicate
                  </button>
                  <button
                    onClick={() => handleToggle(wf)}
                    className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                      wf.is_active
                        ? 'bg-amber-500/10 text-amber-400 hover:bg-amber-500/20'
                        : 'bg-emerald-500/10 text-emerald-400 hover:bg-emerald-500/20'
                    }`}
                  >
                    {wf.is_active ? 'Deactivate' : 'Activate'}
                  </button>
                  <button
                    onClick={() => handleDelete(wf)}
                    className="px-3 py-1.5 rounded-lg text-xs bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors"
                  >
                    Delete
                  </button>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex justify-center gap-2 mt-6">
          <button
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            className="px-3 py-1.5 rounded-lg text-sm bg-gray-800 text-gray-400 hover:text-white disabled:opacity-30"
          >
            ←
          </button>
          <span className="px-3 py-1.5 text-sm text-gray-500">
            Page {page} of {totalPages}
          </span>
          <button
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
            className="px-3 py-1.5 rounded-lg text-sm bg-gray-800 text-gray-400 hover:text-white disabled:opacity-30"
          >
            →
          </button>
        </div>
      )}
    </div>
  );
};

export default WorkflowList;
