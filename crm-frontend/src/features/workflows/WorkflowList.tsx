import React, { useEffect, useState, useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useWorkflowsList, useToggleWorkflow, useDeleteWorkflow } from './queries';
import { RunNowModal, canRunWorkflowNow } from './RunNowModal';
import type { Workflow } from './types';
import { TRIGGER_LABELS, STATUS_COLORS } from './types';
import { useAuth, usePermissions } from '../../lib/auth';

/** Optional actionable link rendered inside a toast (e.g. "View run"). */
interface ToastAction {
  label: string;
  onClick: () => void;
}

export const WorkflowList: React.FC = () => {
  const [toast, setToast] = useState<{ message: string; type: 'error' | 'success'; action?: ToastAction } | null>(null);
  const [runNowTarget, setRunNowTarget] = useState<Workflow | null>(null);
  const navigate = useNavigate();
  const { user, hasCapability } = useAuth();
  const canRunAny = hasCapability('workflows.run_any');
  // Workflow writes (create/toggle/delete, and Duplicate — it opens the builder
  // on a create) plus every email-templates route (even the list GET) require
  // workflows.manage server-side; the list/detail/history GETs are open to any
  // member. Hide the affordances a non-manager would only 403 on. `can` is
  // false until the capability fetch settles, so the controls appear once it
  // loads rather than flashing for members who lack the grant.
  const { can } = usePermissions();
  const canManage = can('workflows.manage');

  // Search term, active/inactive filter, and page all live in the URL query string,
  // so they survive reload + back/forward and a narrowed list can be deep-linked.
  // The URL is the single source of truth; the text box mirrors `q` into local state
  // only so typing stays responsive and is debounced before it touches the URL.
  const [searchParams, setSearchParams] = useSearchParams();
  const q = (searchParams.get('q') ?? '').trim();
  const activeParam = searchParams.get('active');
  const filterActive: boolean | undefined =
    activeParam === null ? undefined : activeParam === 'true';
  const page = Math.max(1, Number(searchParams.get('page') ?? '1') || 1);
  const [searchInput, setSearchInput] = useState(q);

  // Server state via React Query (A3.4). keepPreviousData (in the hook) keeps the
  // current rows visible while a new page/search/filter loads.
  const { data, isLoading: loading, isFetching } = useWorkflowsList({ active: filterActive, q: q || undefined, page, size: 20 });
  const workflows = data?.workflows ?? [];
  const total = data?.total ?? 0;
  const toggleMutation = useToggleWorkflow();
  const deleteMutation = useDeleteWorkflow();

  // Patch the query string while preserving the other params, so search, filter, and
  // page never clobber one another. Empty values drop the key to keep URLs clean.
  const updateParams = useCallback(
    (patch: Record<string, string | null>, opts?: { replace?: boolean }) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(patch)) {
            if (v === null || v === '') next.delete(k);
            else next.set(k, v);
          }
          return next;
        },
        { replace: opts?.replace },
      );
    },
    [setSearchParams],
  );

  const showToast = (
    message: string,
    type: 'error' | 'success' = 'error',
    action?: ToastAction,
  ) => {
    setToast({ message, type, action });
    // Success toasts with an action stay a bit longer so the user can click through.
    setTimeout(() => setToast(null), action ? 6000 : 3000);
  };

  // Debounce the text box, then commit the trimmed term to the URL (?q=). Reset to
  // page 1 on a query change so results aren't hidden on a page the narrowed set no
  // longer has. replace:true keeps each keystroke out of the history stack.
  useEffect(() => {
    const t = setTimeout(() => {
      const next = searchInput.trim();
      if (next !== q) updateParams({ q: next || null, page: null }, { replace: true });
    }, 200);
    return () => clearTimeout(t);
  }, [searchInput, q, updateParams]);

  // Sync the text box when the URL query changes from outside typing — back/forward,
  // a cleared search, or a deep link. Leave an in-progress edit alone when its trimmed
  // value already matches the committed term (avoids clobbering the cursor).
  useEffect(() => {
    setSearchInput((prev) => (prev.trim() === q ? prev : q));
  }, [q]);

  // Toggle/delete run through React Query mutations (optimistic cache updates +
  // rollback live in the hooks); the component only surfaces errors as a toast.
  const handleToggle = (wf: Workflow) => {
    toggleMutation.mutate(wf, {
      onError: (e) => showToast((e as Error).message || 'Failed to toggle workflow', 'error'),
    });
  };

  const handleDelete = (wf: Workflow) => {
    if (!confirm(`Delete "${wf.name}"?`)) return;
    deleteMutation.mutate(wf.id, {
      onError: (e) => showToast((e as Error).message || 'Failed to delete workflow', 'error'),
    });
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
        {canManage && (
          <button
            onClick={() => navigate('/workflows/new')}
            className="px-4 py-2.5 rounded-xl bg-gradient-to-r from-indigo-500 to-purple-500 text-white text-sm font-medium hover:from-indigo-600 hover:to-purple-600 transition-all shadow-lg shadow-indigo-500/20"
          >
            + New Workflow
          </button>
        )}
      </div>

      {/* Tab nav (A5): switch to the email-templates library. Dark-styled to match
          this legacy list page; the templates pages render a token-styled variant. */}
      <div className="mb-6 flex items-center gap-5 border-b border-gray-700">
        <span className="inline-flex items-center gap-1.5 border-b-2 border-indigo-500 px-1 pb-2.5 text-sm font-medium text-white">
          Workflows
        </span>
        {/* Templates routes 403 even on GET without workflows.manage, so don't
            offer the tab to members who can't open it. */}
        {canManage && (
          <button
            onClick={() => navigate('/workflows/email-templates')}
            className="inline-flex items-center gap-1.5 border-b-2 border-transparent px-1 pb-2.5 text-sm font-medium text-gray-400 transition-colors hover:text-white"
          >
            Email Templates
          </button>
        )}
      </div>

      {/* Search */}
      <div className="relative mb-4">
        <span className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-500 text-sm pointer-events-none">🔍</span>
        <input
          type="text"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder="Search workflows by name…"
          className="w-full pl-9 pr-9 py-2.5 rounded-xl bg-gray-800/60 border border-gray-700 text-sm text-white placeholder-gray-500 focus:outline-none focus:border-indigo-500 transition-colors"
        />
        {searchInput && (
          <button
            onClick={() => { setSearchInput(''); updateParams({ q: null, page: null }); }}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-gray-500 hover:text-white text-sm"
            title="Clear search"
          >
            ✕
          </button>
        )}
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
            onClick={() => updateParams({ active: opt.value === undefined ? null : String(opt.value), page: null })}
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
          {q ? (
            <>
              <p className="text-lg mb-2">No workflows match '{q}'</p>
              <p className="text-sm">Try a different search term</p>
            </>
          ) : (
            <>
              <p className="text-lg mb-2">No workflows yet</p>
              {/* Don't invite a create the caller has no button (or permission) for. */}
              <p className="text-sm">
                {canManage
                  ? 'Create your first automation to get started'
                  : 'No automations have been set up in this workspace yet'}
              </p>
            </>
          )}
        </div>
      ) : (
        <div className={`space-y-3 transition-opacity ${isFetching ? 'opacity-60' : ''}`}>
          {workflows.map((wf) => (
            <div
              key={wf.id}
              className="bg-gray-800/60 border border-gray-700 rounded-xl p-4 hover:border-gray-600 transition-all group"
            >
              <div className="flex flex-wrap items-center gap-4">
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

                {/* Actions — always visible on touch/small screens, hover-revealed on desktop. */}
                <div className="flex flex-wrap items-center gap-2 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100">
                  {/* Run Now is shown only to callers the backend would authorize
                      (owner/admin/manager, or the workflow's creator) so a forbidden
                      caller isn't offered a control that would 403. */}
                  {canRunWorkflowNow(canRunAny, user?.id, wf) && (
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
                  {/* Duplicate/toggle/delete are all workflow writes (Duplicate
                      opens the builder on a create) — workflows.manage only. */}
                  {canManage && (
                    <>
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
                    </>
                  )}
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
            onClick={() => updateParams({ page: page - 1 <= 1 ? null : String(page - 1) })}
            disabled={page <= 1}
            className="px-3 py-1.5 rounded-lg text-sm bg-gray-800 text-gray-400 hover:text-white disabled:opacity-30"
          >
            ←
          </button>
          <span className="px-3 py-1.5 text-sm text-gray-500">
            Page {page} of {totalPages}
          </span>
          <button
            onClick={() => updateParams({ page: String(Math.min(totalPages, page + 1)) })}
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
