import React, { useEffect, useState, useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import {
  AlertCircle,
  BarChart3,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  Copy,
  Play,
  Plus,
  Search,
  Trash2,
  X,
  Zap,
} from 'lucide-react';
import { useWorkflowsList, useToggleWorkflow, useDeleteWorkflow } from './queries';
import { RunNowModal, canRunWorkflowNow } from './RunNowModal';
import type { Workflow } from './types';
import { TRIGGER_LABELS, STATUS_BADGE_VARIANT } from './types';
import { useAuth, usePermissions } from '../../lib/auth';
import {
  Badge,
  Button,
  EmptyState,
  Input,
  PageHeader,
  SpinnerBlock,
} from '@/components/ui';

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
    return (
      <Badge variant={STATUS_BADGE_VARIANT[status] ?? 'secondary'}>
        {status.charAt(0).toUpperCase() + status.slice(1)}
      </Badge>
    );
  };

  return (
    <div className="mx-auto w-full max-w-6xl">
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
        <div className="fixed top-4 right-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg transition-all animate-in slide-in-from-top-2">
          {toast.type === 'error' ? (
            <AlertCircle aria-hidden className="h-4 w-4 shrink-0 text-destructive" />
          ) : (
            <CheckCircle2 aria-hidden className="h-4 w-4 shrink-0 text-primary" />
          )}
          <span>{toast.message}</span>
          {toast.action && (
            <button
              onClick={() => {
                toast.action!.onClick();
                setToast(null);
              }}
              className="whitespace-nowrap font-semibold text-primary underline underline-offset-2 transition-opacity hover:opacity-80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {toast.action.label}
            </button>
          )}
        </div>
      )}

      {/* Header */}
      <PageHeader
        title="Workflow Automations"
        description="Automate repetitive tasks with triggers and actions"
        actions={
          canManage ? (
            <Button onClick={() => navigate('/workflows/new')}>
              <Plus aria-hidden /> New Workflow
            </Button>
          ) : undefined
        }
      />

      {/* Tab nav (A5): switch to the email-templates library. */}
      <div className="mb-6 flex items-center gap-5 border-b border-border">
        <span className="inline-flex items-center gap-1.5 border-b-2 border-primary px-1 pb-2.5 text-sm font-medium text-foreground">
          Workflows
        </span>
        {/* Templates routes 403 even on GET without workflows.manage, so don't
            offer the tab to members who can't open it. */}
        {canManage && (
          <button
            onClick={() => navigate('/workflows/email-templates')}
            className="inline-flex items-center gap-1.5 border-b-2 border-transparent px-1 pb-2.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            Email Templates
          </button>
        )}
      </div>

      {/* Search */}
      <div className="relative mb-4">
        <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          type="text"
          value={searchInput}
          onChange={(e) => setSearchInput(e.target.value)}
          placeholder="Search workflows by name…"
          className="pl-9 pr-9"
        />
        {searchInput && (
          <button
            onClick={() => { setSearchInput(''); updateParams({ q: null, page: null }); }}
            className="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            title="Clear search"
          >
            <X aria-hidden className="h-4 w-4" />
          </button>
        )}
      </div>

      {/* Filter */}
      <div className="mb-6 flex gap-2">
        {[
          { label: 'All', value: undefined },
          { label: 'Active', value: true },
          { label: 'Inactive', value: false },
        ].map((opt) => (
          <button
            key={String(opt.value)}
            onClick={() => updateParams({ active: opt.value === undefined ? null : String(opt.value), page: null })}
            className={`rounded-lg px-3 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
              filterActive === opt.value
                ? 'bg-primary text-primary-foreground'
                : 'bg-muted text-muted-foreground hover:text-foreground'
            }`}
          >
            {opt.label}
          </button>
        ))}
        <span className="ml-auto self-center text-sm text-muted-foreground">{total} workflow{total !== 1 ? 's' : ''}</span>
      </div>

      {/* Table */}
      {loading ? (
        <SpinnerBlock label="Loading…" />
      ) : workflows.length === 0 ? (
        <EmptyState
          icon={Zap}
          title={q ? `No workflows match '${q}'` : 'No workflows yet'}
          description={
            q
              ? 'Try a different search term'
              : canManage
                ? 'Create your first automation to get started'
                : 'No automations have been set up in this workspace yet'
          }
        />
      ) : (
        <div className={`space-y-3 transition-opacity ${isFetching ? 'opacity-60' : ''}`}>
          {workflows.map((wf) => (
            <div
              key={wf.id}
              className="group rounded-xl border border-border bg-card p-4 transition-colors hover:border-ring/60"
            >
              <div className="flex flex-wrap items-center gap-4">
                {/* Status dot — brand-tinted "live" indicator when active. */}
                <div className="relative">
                  <div
                    className={`h-3 w-3 rounded-full ${wf.is_active ? 'bg-primary' : 'bg-muted-foreground/40'}`}
                  />
                  {wf.is_active && (
                    <div className="absolute inset-0 h-3 w-3 animate-ping rounded-full bg-primary opacity-30" />
                  )}
                </div>

                {/* Info */}
                <div
                  className="min-w-0 flex-1 cursor-pointer"
                  onClick={() => navigate(`/workflows/${wf.id}`)}
                >
                  <h3 className="truncate text-sm font-semibold text-foreground transition-colors group-hover:text-primary">
                    {wf.name}
                  </h3>
                  <div className="mt-1 flex items-center gap-3">
                    <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                      <Zap aria-hidden className="h-3.5 w-3.5" /> {TRIGGER_LABELS[wf.trigger?.type] || 'Unknown'}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      {wf.action_count || wf.actions?.length || 0} action{(wf.action_count || wf.actions?.length || 0) !== 1 ? 's' : ''}
                    </span>
                    <span className="text-xs text-muted-foreground">
                      v{wf.version}
                    </span>
                    {wf.last_run_status && (
                      <>
                        <span className="text-xs text-muted-foreground/50">|</span>
                        {statusLabel(wf.last_run_status)}
                        {wf.last_run_at && (
                          <span className="text-xs text-muted-foreground">
                            {new Date(wf.last_run_at).toLocaleDateString()}
                          </span>
                        )}
                      </>
                    )}
                    {!wf.last_run_status && (
                      <span className="text-xs italic text-muted-foreground">No runs</span>
                    )}
                  </div>
                </div>

                {/* Actions — always visible on touch/small screens, hover-revealed on desktop. */}
                <div className="flex flex-wrap items-center gap-2 opacity-100 transition-opacity sm:opacity-0 sm:group-hover:opacity-100 sm:focus-within:opacity-100">
                  {/* Run Now is shown only to callers the backend would authorize
                      (owner/admin/manager, or the workflow's creator) so a forbidden
                      caller isn't offered a control that would 403. */}
                  {canRunWorkflowNow(canRunAny, user?.id, wf) && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setRunNowTarget(wf)}
                      title="Run this workflow now against a sample record"
                    >
                      <Play aria-hidden /> Run Now
                    </Button>
                  )}
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => navigate(`/workflows/${wf.id}/history`)}
                    title="View run history"
                  >
                    <BarChart3 aria-hidden /> History
                  </Button>
                  {/* Duplicate/toggle/delete are all workflow writes (Duplicate
                      opens the builder on a create) — workflows.manage only. */}
                  {canManage && (
                    <>
                      {/* Duplicate (P23): open the builder on a fresh, unsaved "Copy of …"
                          draft cloned from this workflow. The source id rides in router
                          state; the builder clones it on mount (see duplicateFrom). */}
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => navigate('/workflows/new', { state: { duplicateFromId: wf.id } })}
                        title="Duplicate this workflow"
                      >
                        <Copy aria-hidden /> Duplicate
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => handleToggle(wf)}
                      >
                        {wf.is_active ? 'Deactivate' : 'Activate'}
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleDelete(wf)}
                        className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                      >
                        <Trash2 aria-hidden /> Delete
                      </Button>
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
        <div className="mt-6 flex items-center justify-center gap-2">
          <Button
            variant="outline"
            size="icon"
            onClick={() => updateParams({ page: page - 1 <= 1 ? null : String(page - 1) })}
            disabled={page <= 1}
            aria-label="Previous page"
          >
            <ChevronLeft aria-hidden />
          </Button>
          <span className="px-3 py-1.5 text-sm text-muted-foreground">
            Page {page} of {totalPages}
          </span>
          <Button
            variant="outline"
            size="icon"
            onClick={() => updateParams({ page: String(Math.min(totalPages, page + 1)) })}
            disabled={page >= totalPages}
            aria-label="Next page"
          >
            <ChevronRight aria-hidden />
          </Button>
        </div>
      )}
    </div>
  );
};

export default WorkflowList;
